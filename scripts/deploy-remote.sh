#!/bin/bash

set -euo pipefail

MODE=""
ENV="test"
PORT=""
RELEASE_MODE="standard"
ARTIFACT_SHA=""
INSTALLED_SERVER_SHA=""
EXPECTED_MIGRATION_CEILING=""
VERCEL_TOKEN="${VERCEL_TOKEN:-}"
VERCEL_ORG="${VERCEL_ORG:-team_CrV6muN0s3QNDJ3vrabttjLR}"
VERCEL_PROJECT="${VERCEL_PROJECT:-}"
API_URL="${API_URL:-}"
MIGRATION_FAILURE_HARD_STOP=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --env) MODE="backend"; ENV="$2"; shift 2 ;;
        --port) PORT="$2"; shift 2 ;;
        --release-mode) RELEASE_MODE="$2"; shift 2 ;;
        --artifact-sha) ARTIFACT_SHA="$2"; shift 2 ;;
        --installed-server-sha) INSTALLED_SERVER_SHA="$2"; shift 2 ;;
        --expected-migration-ceiling) EXPECTED_MIGRATION_CEILING="$2"; shift 2 ;;
        --frontend) MODE="frontend"; shift ;;
        --token) VERCEL_TOKEN="$2"; shift 2 ;;
        --api-url) API_URL="$2"; shift 2 ;;
        --help|-h)
            echo "Backend: $0 --env test|production --release-mode MODE --artifact-sha FULL_SHA [--expected-migration-ceiling VERSION]"
            echo "Frontend: $0 --frontend [--token TOKEN] [--api-url URL]"
            exit 0
            ;;
        *) echo "ERROR: unknown option: $1" >&2; exit 1 ;;
    esac
done

[[ -n "$MODE" ]] || { echo "ERROR: specify --env test or --frontend" >&2; exit 1; }

trace() {
    if [[ -n "${MONERA_DEPLOY_TRACE:-}" ]]; then
        printf '%s\n' "$1" >> "$MONERA_DEPLOY_TRACE"
    fi
}

fail_if_requested() {
    [[ "${MONERA_DEPLOY_FAIL_ACTION:-}" != "$1" ]]
}

validate_release_input() {
    [[ "$ENV" == "test" || "$ENV" == "production" ]] || { echo "ERROR: backend supports only --env test or production" >&2; return 1; }
    [[ "$RELEASE_MODE" =~ ^(migration-only|workers-off-current|server-dark|workers-on-installed|standard)$ ]] || { echo "ERROR: invalid release mode" >&2; return 1; }
    [[ "$ARTIFACT_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "ERROR: artifact SHA must be 40 lowercase hexadecimal characters" >&2; return 1; }
    if [[ -n "$INSTALLED_SERVER_SHA" ]]; then
        [[ "$RELEASE_MODE" == "workers-off-current" && "$INSTALLED_SERVER_SHA" =~ ^[0-9a-f]{40}$ ]] || {
            echo "ERROR: installed server SHA is valid only for workers-off-current and must be 40 lowercase hexadecimal characters" >&2
            return 1
        }
    fi
    if [[ "$RELEASE_MODE" == "migration-only" ]]; then
        [[ "$EXPECTED_MIGRATION_CEILING" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { echo "ERROR: migration-only requires an expected ceiling" >&2; return 1; }
    fi
}

verify_approved_release_source() {
    case "$RELEASE_MODE" in
        standard|migration-only|server-dark|workers-off-current) ;;
        *) return 0 ;;
    esac
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" && "${MONERA_DEPLOY_TEST_REQUIRE_APPROVED_SOURCE:-0}" != "1" ]]; then
        return 0
    fi
    local identity_file="$DEPLOY_SRC/artifact-sha" source_sha
    [[ -f "$identity_file" ]] || { echo "ERROR: approved artifact identity is missing" >&2; return 1; }
    source_sha=$(cat "$identity_file")
    [[ "$source_sha" =~ ^[0-9a-f]{40}$ && "$source_sha" == "$ARTIFACT_SHA" ]] || {
        echo "ERROR: approved artifact identity does not match requested SHA" >&2
        return 1
    }
}

atomic_env_set() {
    local key="$1" value="$2" tmp
    tmp=$(mktemp "${ENV_FILE}.XXXXXX")
    awk -v key="$key" -v value="$value" '
        BEGIN { found=0 }
        $0 ~ "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=" {
            if (!found) print key "=" value
            found=1
            next
        }
        { print }
        END { if (!found) print key "=" value }
    ' "$ENV_FILE" > "$tmp"
    chmod --reference="$ENV_FILE" "$tmp" 2>/dev/null || chmod 600 "$tmp"
    mv -f "$tmp" "$ENV_FILE"
}

require_normalized_env_bool() {
    local key="$1" expected="$2"
    awk -v key="$key" -v expected="$expected" '
        BEGIN { assignments=0; exact=0 }
        $0 ~ "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=" { assignments++ }
        $0 == key "=" expected { exact++ }
        END { exit !(assignments == 1 && exact == 1) }
    ' "$ENV_FILE"
}

require_server_dark_env() {
    require_normalized_env_bool COMPANY_FUND_ENABLED true || {
        echo "ERROR: server-dark requires exactly one normalized COMPANY_FUND_ENABLED=true" >&2
        return 1
    }
    require_normalized_env_bool COMPANY_FUND_START_BACKGROUND_WORKERS false || {
        echo "ERROR: server-dark requires exactly one normalized COMPANY_FUND_START_BACKGROUND_WORKERS=false" >&2
        return 1
    }
    require_normalized_routing_mode || return 1
}

require_normalized_routing_mode() {
    awk '
        BEGIN { assignments=0; valid=0 }
        /^[[:space:]]*(export[[:space:]]+)?SAFEHERON_TRANSACTION_ROUTING_MODE[[:space:]]*=/ { assignments++ }
        $0 == "SAFEHERON_TRANSACTION_ROUTING_MODE=capture-only" || $0 == "SAFEHERON_TRANSACTION_ROUTING_MODE=routing-authoritative" { valid++ }
        END { exit !(assignments == 1 && valid == 1) }
    ' "$ENV_FILE" || {
        echo "ERROR: SAFEHERON_TRANSACTION_ROUTING_MODE must be exactly capture-only or routing-authoritative" >&2
        return 1
    }
}

set_workers() {
    local enabled="$1"
    trace "env-workers-$([[ "$enabled" == "true" ]] && echo on || echo off)"
    fail_if_requested "env-workers-$([[ "$enabled" == "true" ]] && echo on || echo off)" || return 1
    require_normalized_routing_mode || return 1
    atomic_env_set COMPANY_FUND_ENABLED true
    atomic_env_set COMPANY_FUND_START_BACKGROUND_WORKERS "$enabled"
}

set_routing_mode() {
    local mode="$1"
    [[ "$mode" == "capture-only" || "$mode" == "routing-authoritative" ]] || return 1
    trace "env-routing-$mode"
    fail_if_requested "env-routing-$mode" || return 1
    atomic_env_set SAFEHERON_TRANSACTION_ROUTING_MODE "$mode"
}

release_state_enforced() {
    [[ "$ENV" == "production" || "${MONERA_DEPLOY_ENFORCE_RELEASE_STATE:-0}" == "1" ]]
}

read_release_state() {
    [[ -f "$RELEASE_STATE_FILE" ]] || return 1
    RELEASE_STATE_SHA=$(awk -F '\t' 'NR==1 { print $1 }' "$RELEASE_STATE_FILE")
    RELEASE_STATE_PHASE=$(awk -F '\t' 'NR==1 { print $2 }' "$RELEASE_STATE_FILE")
    [[ "$RELEASE_STATE_SHA" =~ ^[0-9a-f]{40}$ && -n "$RELEASE_STATE_PHASE" ]]
}

require_release_state() {
    local expected_phase="$1"
    release_state_enforced || return 0
    read_release_state || { echo "ERROR: controlled release state is missing" >&2; return 1; }
    [[ "$RELEASE_STATE_SHA" == "$ARTIFACT_SHA" && "$RELEASE_STATE_PHASE" == "$expected_phase" ]] || {
        echo "ERROR: controlled release requires $expected_phase for artifact $ARTIFACT_SHA" >&2
        return 1
    }
}

require_release_start() {
    release_state_enforced || return 0
    if ! read_release_state; then
        return 0
    fi
    [[ ( "$RELEASE_STATE_SHA" == "$ARTIFACT_SHA" && "$RELEASE_STATE_PHASE" == "migration-056" ) ||
       "$RELEASE_STATE_PHASE" == "workers-on-installed" ]] || {
        echo "ERROR: another controlled release is incomplete at $RELEASE_STATE_PHASE" >&2
        return 1
    }
}

write_release_state() {
    local phase="$1" tmp
    release_state_enforced || return 0
    trace "release-state-$phase"
    tmp=$(mktemp "$APP_DIR/.release-state.XXXXXX")
    printf '%s\t%s\n' "$ARTIFACT_SHA" "$phase" > "$tmp"
    chmod 600 "$tmp"
    mv -f "$tmp" "$RELEASE_STATE_FILE"
}

installed_sha() {
    sed -n 's/.*"server_sha"[[:space:]]*:[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p' "$MANIFEST_FILE" | head -1
}

verify_installed_sha() {
    local expected_sha="${INSTALLED_SERVER_SHA:-$ARTIFACT_SHA}"
    trace "verify-installed-sha"
    if [[ -f "$MANIFEST_FILE" ]]; then
        [[ "$(installed_sha)" == "$expected_sha" ]] || { echo "ERROR: installed server SHA does not match approved current SHA" >&2; return 1; }
        return 0
    fi
    [[ "$RELEASE_MODE" == "workers-off-current" ]] || { echo "ERROR: release manifest is missing" >&2; return 1; }
    trace "verify-legacy-embedded-sha"
    local legacy_binary="$APP_DIR/monera-server" legacy_short="${expected_sha:0:7}"
    if [[ "$ENV" == "production" && ! -f "$legacy_binary" && -f "$APP_DIR/server" ]]; then
        legacy_binary="$APP_DIR/server"
    fi
    if [[ ! -f "$legacy_binary" ]] || { ! grep -aFq "$expected_sha" "$legacy_binary" && { [[ "$ENV" != "production" ]] || ! grep -aFq "$legacy_short" "$legacy_binary"; }; }; then
        echo "ERROR: legacy installed server does not contain the approved artifact SHA" >&2
        return 1
    fi
}

install_binary() {
    local name="$1"
    trace "install-${name#monera-}"
    fail_if_requested "install-${name#monera-}" || return 1
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        [[ ! -f "$DEPLOY_SRC/${name}.fake" ]] || cp -p "$DEPLOY_SRC/${name}.fake" "$APP_DIR/${name}"
        return 0
    fi
    [[ -f "$DEPLOY_SRC/${name}.gz" ]] || { echo "ERROR: missing $name artifact" >&2; return 1; }
    gunzip -c "$DEPLOY_SRC/${name}.gz" > "$APP_DIR/${name}.new"
    chmod +x "$APP_DIR/${name}.new"
    mv -f "$APP_DIR/${name}.new" "$APP_DIR/${name}"
}

write_manifest() {
    trace "write-manifest"
    fail_if_requested write-manifest || return 1
    local tmp
    tmp=$(mktemp "$APP_DIR/.release-manifest.XXXXXX")
    if [[ "$RELEASE_MODE" == "server-dark" ]]; then
        printf '{"server_sha":"%s","migration_ceiling":"057","routing_mode":"capture-only","safe_artifact":true}\n' "$ARTIFACT_SHA" > "$tmp"
    else
        printf '{"server_sha":"%s"}\n' "$ARTIFACT_SHA" > "$tmp"
    fi
    chmod 600 "$tmp"
    mv -f "$tmp" "$MANIFEST_FILE"
}

require_safe_dark_manifest() {
    [[ -f "$MANIFEST_FILE" ]] || { echo "ERROR: release manifest is missing" >&2; return 1; }
    grep -Eq '"server_sha"[[:space:]]*:[[:space:]]*"'"$ARTIFACT_SHA"'"' "$MANIFEST_FILE" &&
        grep -Eq '"migration_ceiling"[[:space:]]*:[[:space:]]*"057"' "$MANIFEST_FILE" &&
        grep -Eq '"routing_mode"[[:space:]]*:[[:space:]]*"capture-only"' "$MANIFEST_FILE" &&
        grep -Eq '"safe_artifact"[[:space:]]*:[[:space:]]*true' "$MANIFEST_FILE" || {
        echo "ERROR: installed server is not a validated dark release artifact" >&2
        return 1
    }
}

install_service() {
    trace "install-service"
    fail_if_requested install-service || return 1
    local tmp
    tmp=$(mktemp "$APP_DIR/.service-unit.XXXXXX")
    cat > "$tmp" <<EOF
[Unit]
Description=Monera Digital (${ENV})
After=network.target

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=${APP_DIR}
EnvironmentFile=${ENV_FILE}
ExecStart=${APP_DIR}/monera-server
Restart=always
RestartSec=10
Environment=PORT=${PORT}
Environment=GIN_MODE=release

[Install]
WantedBy=multi-user.target
EOF
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        mv -f "$tmp" "$SERVICE_FILE"
    else
        sudo install -m 0644 "$tmp" "$SERVICE_FILE"
        rm -f "$tmp"
    fi
    daemon_reload
    [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]] || sudo systemctl enable "$SERVICE_NAME"
}

daemon_reload() {
    trace "daemon-reload"
    fail_if_requested daemon-reload || return 1
    [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]] || sudo systemctl daemon-reload
}

restart_service() {
    trace "restart"
    fail_if_requested restart || return 1
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        printf 'running\n' > "$SERVICE_STATE_FILE"
        return 0
    fi
    sudo systemctl restart "$SERVICE_NAME"
}

stop_service() {
    trace "stop"
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        [[ "${MONERA_DEPLOY_FAKE_STOP_FAILURE:-0}" != "1" ]] || return 1
        printf 'stopped\n' > "$SERVICE_STATE_FILE"
        return 0
    fi
    sudo systemctl stop "$SERVICE_NAME"
}

verify_service_inactive() {
    trace "verify-inactive"
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        [[ "${MONERA_DEPLOY_FAKE_INACTIVE_FAILURE:-0}" != "1" ]] || return 1
        [[ -f "$SERVICE_STATE_FILE" && "$(cat "$SERVICE_STATE_FILE")" == "stopped" ]]
        return
    fi
    if sudo systemctl is-active --quiet "$SERVICE_NAME"; then
        echo "ERROR: service remains active after stop" >&2
        return 1
    fi
}

health_check() {
    trace "health"
    fail_if_requested health || return 1
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        [[ ! -f "$SERVICE_STATE_FILE" || "$(cat "$SERVICE_STATE_FILE")" == "running" ]]
        return
    fi
    local _
    for _ in {1..8}; do
        if curl -fsS --max-time 4 "http://127.0.0.1:${PORT}/api/health" >/dev/null; then
            return 0
        fi
        sleep 5
    done
    sudo journalctl -u "$SERVICE_NAME" --no-pager -n 80 || true
    return 1
}

run_migration() {
    trace "migrate"
    local actual migration_exit
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        trace "migration-print-ceiling"
        [[ "${MONERA_DEPLOY_FAKE_PRINT_CEILING_EXIT_CODE:-0}" == "0" ]] || return 1
        actual="${MONERA_DEPLOY_FAKE_MIGRATION_CEILING:-$EXPECTED_MIGRATION_CEILING}"
    else
        actual=$("$APP_DIR/monera-migrate" -print-ceiling -exact-version "$EXPECTED_MIGRATION_CEILING") || return 1
    fi
    if [[ -n "$EXPECTED_MIGRATION_CEILING" && "$actual" != "$EXPECTED_MIGRATION_CEILING" ]]; then
        echo "ERROR: migration ceiling $actual does not match $EXPECTED_MIGRATION_CEILING" >&2
        return 1
    fi

    trace "migration-runner-started"
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        migration_exit="${MONERA_DEPLOY_FAKE_MIGRATION_EXIT_CODE:-0}"
        [[ "${MONERA_DEPLOY_FAIL_ACTION:-}" != "migrate" ]] || migration_exit=1
    elif (cd "$APP_DIR" && EXPECTED_MIGRATION_CEILING="$EXPECTED_MIGRATION_CEILING" ./monera-migrate -exact-version "$EXPECTED_MIGRATION_CEILING"); then
        migration_exit=0
    else
        migration_exit=$?
    fi
    [[ "$migration_exit" == "0" ]] && return 0
    if [[ "$RELEASE_MODE" != "migration-only" || ( "$EXPECTED_MIGRATION_CEILING" != "052" && "$EXPECTED_MIGRATION_CEILING" != "053" ) ]]; then
        return 1
    fi
    classify_failed_migration "$migration_exit"
}

read_migration_schema_report() {
    local inspector="$APP_DIR/company-fund-release" installed_database_url
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" && -n "${MONERA_DEPLOY_SCHEMA_INSPECTOR_COMMAND:-}" ]]; then
        inspector="$MONERA_DEPLOY_SCHEMA_INSPECTOR_COMMAND"
    fi
    installed_database_url=$(read_installed_database_url) || return 1
    DATABASE_URL="$installed_database_url" RUN_COMPANY_FUND_SCHEMA_FINGERPRINT=1 "$inspector" schema-fingerprint
}

read_installed_database_url() {
    local assignments line value
    assignments=$(awk '
        /^[[:space:]]*(export[[:space:]]+)?DATABASE_URL[[:space:]]*=/ { count++ }
        END { print count + 0 }
    ' "$ENV_FILE")
    [[ "$assignments" == "1" ]] || return 1
    line=$(awk '/^[[:space:]]*(export[[:space:]]+)?DATABASE_URL[[:space:]]*=/ { print }' "$ENV_FILE")
    [[ "$line" == DATABASE_URL=* ]] || return 1
    value="${line#DATABASE_URL=}"
    [[ "$value" == postgres://* || "$value" == postgresql://* ]] || return 1
    [[ "$value" != *[[:space:]]* && "$value" != *\`* && "$value" != *\$\(* && "$value" != *\'* && "$value" != *\"* ]] || return 1
    printf '%s' "$value"
}

classify_failed_migration() {
    local migration_exit="$1" report state recorded052 recorded053
    report=$(read_migration_schema_report 2>/dev/null || true)
    state=$(printf '%s' "$report" | sed -E -n 's/.*"state"[[:space:]]*:[[:space:]]*"([A-Z]+)".*/\1/p' | head -1)
    recorded052=$(printf '%s' "$report" | sed -E -n 's/.*"migration_052_recorded"[[:space:]]*:[[:space:]]*(true|false).*/\1/p' | head -1)
    recorded053=$(printf '%s' "$report" | sed -E -n 's/.*"migration_053_recorded"[[:space:]]*:[[:space:]]*(true|false).*/\1/p' | head -1)
    if [[ "$EXPECTED_MIGRATION_CEILING" == "052" ]]; then
        if [[ "$state:$recorded052:$recorded053:$migration_exit" == "A:true:false:75" ]]; then
            trace "migration-a-commit-reconciled"
            return 0
        fi
        if [[ "$state:$recorded052:$recorded053" == "A:true:false" ]]; then
            trace "migration-a-unexpected-commit"
            return 1
        fi
        if [[ -z "$state" || -z "$recorded052" || -z "$recorded053" ]]; then
            hard_stop_uncertain_migration "a" "UNKNOWN" "unknown"
        else
            hard_stop_uncertain_migration "a" "$state" "$recorded052"
        fi
        return 1
    fi
    if [[ "$EXPECTED_MIGRATION_CEILING" == "053" ]]; then
        if [[ "$state:$recorded052:$recorded053" == "A:true:false" ]]; then
            trace "migration-b-atomic-failure"
            return 1
        fi
        if [[ "$state:$recorded052:$recorded053:$migration_exit" == "B:true:true:75" ]]; then
            trace "migration-b-commit-reconciled"
            return 0
        fi
        if [[ "$state:$recorded052:$recorded053" == "B:true:true" ]]; then
            trace "migration-b-unexpected-commit"
            return 1
        fi
        if [[ -z "$state" || -z "$recorded052" || -z "$recorded053" ]]; then
            hard_stop_uncertain_migration "b" "UNKNOWN" "unknown"
        else
            hard_stop_uncertain_migration "b" "$state" "$recorded053"
        fi
        return 1
    fi
    hard_stop_uncertain_migration "unknown" "UNKNOWN" "unknown"
    return 1
}

hard_stop_uncertain_migration() {
    local phase="$1" state="$2" recorded="$3" invocation="unknown"
    MIGRATION_FAILURE_HARD_STOP=1
    trace "migration-$phase-non-atomic-hard-stop"
    trace "migration-schema-$state-recorded-$recorded"
    trace "alarm-migration-state-uncertain"
    echo "ERROR: migration state $state recorded=$recorded requires manual quiescence" >&2
    if ! stop_service; then
        trace "alarm-service-stop-failed"
        echo "ERROR: migration state is uncertain and the service could not be stopped; manual quiescence is mandatory" >&2
    fi
    if ! verify_service_inactive; then
        trace "alarm-inactive-verification-failed"
        echo "ERROR: migration state is uncertain and inactive service state could not be verified; manual quiescence is mandatory" >&2
    fi
    atomic_env_set COMPANY_FUND_START_BACKGROUND_WORKERS false
    printf '%s recorded=%s\n' "$state" "$recorded" > "$MIGRATION_SCHEMA_MARKER_FILE"
    if [[ -f "$MIGRATION_INVOCATION_FILE" ]]; then
        invocation=$(tr -d '\r\n' < "$MIGRATION_INVOCATION_FILE")
    fi
    printf 'migration-%s-non-atomic %s state=%s recorded=%s\n' "$phase" "$invocation" "$state" "$recorded" > "$MANUAL_QUIESCE_FILE"
}

backup_file() {
    local path="$1"
    rm -f "$path.release-backup"
    [[ -f "$path" ]] && cp -p "$path" "$path.release-backup"
    return 0
}

restore_file() {
    local path="$1"
    if [[ -f "$path.release-backup" ]]; then
        mv -f "$path.release-backup" "$path"
    else
        rm -f "$path"
    fi
}

restore_service() {
    trace "restore-service"
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        restore_file "$SERVICE_FILE"
    elif sudo test -f "$SERVICE_FILE.release-backup"; then
        sudo mv -f "$SERVICE_FILE.release-backup" "$SERVICE_FILE"
    else
        sudo rm -f "$SERVICE_FILE"
    fi
    daemon_reload
}

backup_service() {
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        backup_file "$SERVICE_FILE"
    else
        sudo rm -f "$SERVICE_FILE.release-backup"
        if sudo test -f "$SERVICE_FILE"; then
            sudo cp -p "$SERVICE_FILE" "$SERVICE_FILE.release-backup"
        fi
    fi
}

discard_service_backup() {
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        rm -f "$SERVICE_FILE.release-backup"
    else
        sudo rm -f "$SERVICE_FILE.release-backup"
    fi
}

recover_service_or_stop() {
    if restart_service && health_check; then
        return 0
    fi
    stop_service || true
    return 1
}

rollback_server_state() {
    restore_file "$APP_DIR/monera-server"
    restore_file "$MANIFEST_FILE"
    if [[ "$RELEASE_MODE" == "standard" ]]; then
        restore_file "$APP_DIR/monera-migrate"
    fi
    if ! restore_service; then
        stop_service || true
        return 1
    fi
    recover_service_or_stop
}

persist_release_script() {
    [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]] && return 0
    if [[ "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")" != "$APP_DIR/deploy-remote.sh" ]]; then
        install -m 0755 "${BASH_SOURCE[0]}" "$APP_DIR/deploy-remote.sh"
    fi
}

deploy_backend() {
    validate_release_input
    local default_app_dir="/opt/monera-digital" default_deploy_src="/tmp/monera-stage-deploy/package"
    if [[ "$ENV" == "production" ]]; then
        default_app_dir="/home/ec2-user/monera"
        default_deploy_src="/tmp/monera-production-deploy/package"
    fi
    APP_DIR="${MONERA_DEPLOY_APP_DIR:-$default_app_dir}"
    DEPLOY_SRC="${MONERA_DEPLOY_SRC:-$default_deploy_src}"
    SERVICE_NAME="${MONERA_DEPLOY_SERVICE_NAME:-monera-digital}"
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        SERVICE_FILE="${MONERA_DEPLOY_SERVICE_FILE:-$APP_DIR/${SERVICE_NAME}.service}"
    else
        SERVICE_FILE="${MONERA_DEPLOY_SERVICE_FILE:-/etc/systemd/system/${SERVICE_NAME}.service}"
    fi
    SERVICE_STATE_FILE="${MONERA_DEPLOY_SERVICE_STATE:-$APP_DIR/.service-state}"
    PORT="${PORT:-8086}"
    ENV_FILE="$APP_DIR/.env"
    MANIFEST_FILE="$APP_DIR/release-manifest.json"
    RELEASE_STATE_FILE="${MONERA_DEPLOY_RELEASE_STATE_FILE:-$APP_DIR/release-state.tsv}"
    MIGRATION_SCHEMA_MARKER_FILE="${MONERA_DEPLOY_SCHEMA_MARKER:-$APP_DIR/.schema-marker}"
    MIGRATION_INVOCATION_FILE="${MONERA_DEPLOY_INVOCATION_FILE:-$APP_DIR/.invocation-id}"
    MANUAL_QUIESCE_FILE="${MONERA_DEPLOY_MANUAL_QUIESCE_FILE:-$APP_DIR/.manual-quiesce-required}"
    verify_approved_release_source
    [[ -f "$ENV_FILE" ]] || { echo "ERROR: $ENV_FILE is missing" >&2; return 1; }

    case "$RELEASE_MODE" in
        migration-only)
            if [[ "$EXPECTED_MIGRATION_CEILING" == "056" ]]; then
                require_release_start
            elif [[ "$EXPECTED_MIGRATION_CEILING" == "057" ]]; then
                require_release_state migration-056
            fi
            install_binary monera-migrate
            install_binary company-fund-release
            run_migration
            if [[ "$EXPECTED_MIGRATION_CEILING" == "056" ]]; then
                write_release_state migration-056
            elif [[ "$EXPECTED_MIGRATION_CEILING" == "057" ]]; then
                write_release_state migration-057
            fi
            ;;
        workers-off-current)
            require_release_state migration-057
            verify_installed_sha
            cp -p "$ENV_FILE" "$ENV_FILE.release-backup"
            if ! set_routing_mode capture-only || ! set_workers false; then
                mv -f "$ENV_FILE.release-backup" "$ENV_FILE"
                return 1
            fi
            if ! stop_service || ! verify_service_inactive; then
                mv -f "$ENV_FILE.release-backup" "$ENV_FILE"
                stop_service || true
                return 1
            fi
            rm -f "$ENV_FILE.release-backup"
            write_release_state workers-off-current
            ;;
        server-dark)
            require_release_state workers-off-current
            trace "require-workers-off"
            require_server_dark_env
            backup_file "$APP_DIR/monera-server"
            backup_file "$MANIFEST_FILE"
            backup_service
            if ! install_binary monera-server || ! write_manifest || ! install_service || ! restart_service || ! health_check; then
                trace "fail-closed-server"
                stop_service || true
                return 1
            fi
            rm -f "$APP_DIR/monera-server.release-backup" "$MANIFEST_FILE.release-backup"
            discard_service_backup
            persist_release_script
            write_release_state server-dark
            ;;
        workers-on-installed)
            require_release_state server-dark
            verify_installed_sha
            require_safe_dark_manifest
            cp -p "$ENV_FILE" "$ENV_FILE.release-backup"
            if ! set_routing_mode routing-authoritative || ! set_workers true || ! restart_service || ! health_check; then
                mv -f "$ENV_FILE.release-backup" "$ENV_FILE"
                if ! recover_service_or_stop; then
                    stop_service || true
                fi
                return 1
            fi
            rm -f "$ENV_FILE.release-backup"
            write_release_state workers-on-installed
            ;;
        standard)
            backup_file "$APP_DIR/monera-migrate"
            backup_file "$APP_DIR/company-fund-release"
            if ! install_binary monera-migrate || ! install_binary company-fund-release || ! run_migration; then
                if [[ "$MIGRATION_FAILURE_HARD_STOP" == "0" ]]; then
                    trace "rollback-migrate"
                    restore_file "$APP_DIR/monera-migrate"
                    restore_file "$APP_DIR/company-fund-release"
                fi
                return 1
            fi
            backup_file "$APP_DIR/monera-server"
            backup_file "$MANIFEST_FILE"
            backup_service
            if ! install_binary monera-server || ! write_manifest || ! install_service || ! restart_service || ! health_check; then
                trace "fail-closed-server"
                stop_service || true
                return 1
            fi
            rm -f "$APP_DIR/monera-server.release-backup" "$APP_DIR/monera-migrate.release-backup" "$APP_DIR/company-fund-release.release-backup" "$MANIFEST_FILE.release-backup"
            discard_service_backup
            persist_release_script
            ;;
    esac
}

deploy_frontend() {
    command -v vercel >/dev/null 2>&1 || { echo "ERROR: vercel CLI not found" >&2; return 1; }
    local script_dir project_dir temp_dir
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    project_dir="$(cd "$script_dir/.." && pwd)"
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' RETURN
    cd "$project_dir"
    cp package.json package-lock.json vite.config.ts tsconfig.json tsconfig.node.json "$temp_dir/"
    cp tailwind.config.ts postcss.config.js index.html vercel.json "$temp_dir/"
    cp -r src public "$temp_dir/"
    [[ -f components.json ]] && cp components.json "$temp_dir/"
    if [[ -n "$API_URL" ]]; then printf 'VITE_API_BASE_URL=%s\n' "$API_URL" > "$temp_dir/.env"; fi
    if [[ -f .vercel/project.json ]]; then
        mkdir -p "$temp_dir/.vercel"; cp .vercel/project.json "$temp_dir/.vercel/project.json"
    elif [[ -n "$VERCEL_PROJECT" ]]; then
        mkdir -p "$temp_dir/.vercel"
        printf '{"projectId":"%s","orgId":"%s"}\n' "$VERCEL_PROJECT" "$VERCEL_ORG" > "$temp_dir/.vercel/project.json"
    else
        echo "ERROR: Vercel project is not linked" >&2; return 1
    fi
    cd "$temp_dir"
    local args=(--prod)
    [[ -n "$VERCEL_TOKEN" ]] && args+=("--token=$VERCEL_TOKEN")
    vercel "${args[@]}"
}

case "$MODE" in
    backend) deploy_backend ;;
    frontend) deploy_frontend ;;
esac
