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
            echo "Backend: $0 --env test|production --release-mode standard --artifact-sha FULL_SHA --expected-migration-ceiling VERSION"
            echo "Frontend: $0 --frontend [--token TOKEN] [--api-url URL]"
            exit 0
            ;;
        *) echo "ERROR: unknown option: $1" >&2; exit 1 ;;
    esac
done

if [[ -z "$MODE" && "${BASH_SOURCE[0]}" == "$0" ]]; then
    echo "ERROR: specify --env test or --frontend" >&2
    exit 1
fi

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
    [[ "$RELEASE_MODE" == "standard" ]] || {
        echo "ERROR: only --release-mode standard is supported (cutover multi-mode was removed)" >&2
        return 1
    }
    [[ "$ARTIFACT_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "ERROR: artifact SHA must be 40 lowercase hexadecimal characters" >&2; return 1; }
    [[ -z "$INSTALLED_SERVER_SHA" ]] || {
        echo "ERROR: installed server SHA is not used by standard deploy" >&2
        return 1
    }
    [[ "$EXPECTED_MIGRATION_CEILING" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || {
        echo "ERROR: standard deploy requires --expected-migration-ceiling" >&2
        return 1
    }
}

verify_approved_release_source() {
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" && "${MONERA_DEPLOY_FAKE_REQUIRE_APPROVED_SOURCE:-0}" != "1" ]]; then
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
    printf '{"server_sha":"%s","migration_ceiling":"%s"}\n' "$ARTIFACT_SHA" "$EXPECTED_MIGRATION_CEILING" > "$tmp"
    chmod 600 "$tmp"
    mv -f "$tmp" "$MANIFEST_FILE"
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

health_check() {
    trace "health"
    fail_if_requested health || return 1
    if [[ "${MONERA_DEPLOY_FAKE:-0}" == "1" ]]; then
        [[ ! -f "$SERVICE_STATE_FILE" || "$(cat "$SERVICE_STATE_FILE")" == "running" ]]
        return
    fi
    local attempt curl_output
    for attempt in {1..8}; do
        if curl_output=$(curl -fsS --max-time 4 "http://127.0.0.1:${PORT}/api/health" 2>&1); then
            return 0
        fi
        if (( attempt < 8 )); then
            echo "Waiting for service health check (attempt ${attempt}/8)" >&2
            sleep 5
        fi
    done
    echo "ERROR: service health check failed after 8 attempts" >&2
    [[ -z "$curl_output" ]] || echo "$curl_output" >&2
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
    return 1
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
    verify_approved_release_source
    [[ -f "$ENV_FILE" ]] || { echo "ERROR: $ENV_FILE is missing" >&2; return 1; }

    # Single standard path: migrate (controlled ceiling) → replace server → restart.
    backup_file "$APP_DIR/monera-migrate"
    backup_file "$APP_DIR/company-fund-release"
    if ! install_binary monera-migrate || ! install_binary company-fund-release || ! run_migration; then
        trace "rollback-migrate"
        restore_file "$APP_DIR/monera-migrate"
        restore_file "$APP_DIR/company-fund-release"
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
