#!/bin/bash
# =============================================================================
# Monera Digital - Unified Deployment Script (Test Env)
#
# Backend:  bash deploy-remote.sh --env test [--port PORT]
# Frontend: bash deploy-remote.sh --frontend [--token TOKEN] [--api-url URL]
# =============================================================================

set -e

MODE=""
ENV="test"
PORT=""
VERCEL_TOKEN="${VERCEL_TOKEN:-}"
VERCEL_ORG="${VERCEL_ORG:-team_CrV6muN0s3QNDJ3vrabttjLR}"
VERCEL_PROJECT="${VERCEL_PROJECT:-}"
API_URL="${API_URL:-}"

while [[ $# -gt 0 ]]; do
    case $1 in
        --env)       MODE="backend"; ENV="$2"; shift 2 ;;
        --port)      PORT="$2"; shift 2 ;;
        --frontend)  MODE="frontend"; shift ;;
        --token)     VERCEL_TOKEN="$2"; shift 2 ;;
        --api-url)   API_URL="$2"; shift 2 ;;
        --help|-h)
            echo "Usage:"
            echo "  Backend:  $0 --env test [--port PORT]"
            echo "  Frontend: $0 --frontend [--token TOKEN] [--api-url URL]"
            exit 0
            ;;
        *)  echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [ -z "$MODE" ]; then
    echo "ERROR: specify --env test (backend) or --frontend (Vercel)"
    exit 1
fi

# =============================================================================
# Backend deployment (runs on remote server)
# =============================================================================
deploy_backend() {
    case "$ENV" in
        test)
            APP_DIR="/opt/monera-digital"
            SERVICE_NAME="monera-digital"
            PORT="${PORT:-8086}"
            ;;
        *)
            echo "ERROR: only --env test is supported. Production deployment is not enabled."
            exit 1
            ;;
    esac

    BINARY_NAME="monera-server"
    MIGRATE_NAME="monera-migrate"

    echo "=== Deploying monera-digital backend [${ENV}] ==="
    echo "  APP_DIR:  ${APP_DIR}"
    echo "  SERVICE:  ${SERVICE_NAME}"
    echo "  PORT:     ${PORT}"

    chmod +x "${APP_DIR}/${BINARY_NAME}"
    [ -f "${APP_DIR}/${MIGRATE_NAME}" ] && chmod +x "${APP_DIR}/${MIGRATE_NAME}"

    if [ ! -f "${APP_DIR}/.env" ]; then
        echo "FATAL: .env file not found in ${APP_DIR}!"
        echo "Creating a template .env file..."
        cat > "${APP_DIR}/.env" << EOF
# Monera Digital ${ENV} Environment
PORT=${PORT}
GIN_MODE=release
APP_ENV=${ENV}
DATABASE_URL=postgresql://user:pass@host:5432/monera_${ENV}?sslmode=require
JWT_SECRET=change-me
ENCRYPTION_KEY=change-me-64-hex-chars
# Safeheron
SAFEHERON_API_KEY=
SAFEHERON_API_BASE_URL=https://api.safeheron.com
SAFEHERON_PRIVATE_KEY_PATH=./secrets/safeheron-private.pem
SAFEHERON_PLATFORM_PUBLIC_KEY_PATH=./secrets/safeheron-platform-pub.pem
SAFEHERON_WEBHOOK_PUBLIC_KEY_PATH=./secrets/safeheron-webhook-pub.pem
SAFEHERON_WEBHOOK_PRIVATE_KEY_PATH=./secrets/safeheron-webhook-priv.pem
SAFEHERON_WEBHOOK_ALLOWED_IPS=
# KYT
KYT_ENABLED=true
KYT_TIMEOUT=20m
AML_FIRST_POLL_DELAY=5m
AML_POLL_INTERVAL=60s
# Alert
ALERT_WEBHOOK_URL=
ALERT_EMAIL_RECIPIENTS=
EOF
        chmod 600 "${APP_DIR}/.env"
        echo "Please edit ${APP_DIR}/.env with correct credentials, then re-run this script."
        exit 1
    fi

    if [ -f "${APP_DIR}/${MIGRATE_NAME}" ]; then
        echo "Running database migration..."
        cd "${APP_DIR}"
        ./${MIGRATE_NAME} || {
            echo "FATAL: Migration failed. Aborting deployment."
            exit 1
        }
    fi

    echo "Updating systemd service..."
    sudo bash -c "cat > /etc/systemd/system/${SERVICE_NAME}.service" << EOF
[Unit]
Description=Monera Digital (${ENV})
After=network.target

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=${APP_DIR}
EnvironmentFile=${APP_DIR}/.env
ExecStart=${APP_DIR}/${BINARY_NAME}
Restart=always
RestartSec=10
Environment=PORT=${PORT}
Environment=GIN_MODE=release

[Install]
WantedBy=multi-user.target
EOF

    echo "Restarting service..."
    sudo systemctl daemon-reload
    sudo systemctl enable "${SERVICE_NAME}"
    sudo systemctl restart "${SERVICE_NAME}"

    sleep 3
    echo "Verifying service status..."
    if ! systemctl is-active --quiet "${SERVICE_NAME}"; then
        echo "ERROR: Service failed to start. Last 50 lines of logs:"
        sudo journalctl -u "${SERVICE_NAME}" --no-pager -n 50
        exit 1
    fi

    systemctl status "${SERVICE_NAME}" --no-pager

    echo ""
    echo "================================================="
    echo "  Backend [${ENV}] deployed on port ${PORT}"
    echo "  Logs: journalctl -u ${SERVICE_NAME} -f"
    echo "================================================="
}

# =============================================================================
# Frontend deployment (runs locally, pushes to Vercel)
# =============================================================================
deploy_frontend() {
    if ! command -v vercel &>/dev/null; then
        echo "ERROR: 'vercel' CLI not found. Install: npm i -g vercel"
        exit 1
    fi

    # Locate project root (script may be invoked from anywhere)
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

    if [ ! -f "${PROJECT_DIR}/package.json" ]; then
        echo "ERROR: package.json not found in ${PROJECT_DIR}"
        exit 1
    fi

    TEMP_DIR=$(mktemp -d)
    trap 'rm -rf "${TEMP_DIR}"' EXIT

    echo "=== Deploying monera-digital frontend [test] ==="

    cd "${PROJECT_DIR}"

    cp package.json package-lock.json "${TEMP_DIR}/"
    cp vite.config.ts tsconfig.json tsconfig.node.json "${TEMP_DIR}/"
    cp tailwind.config.ts postcss.config.js "${TEMP_DIR}/"
    cp index.html "${TEMP_DIR}/"
    cp -r src "${TEMP_DIR}/"
    cp -r public "${TEMP_DIR}/"

    if [ -f vercel.json ]; then
        cp vercel.json "${TEMP_DIR}/"
    else
        echo "ERROR: vercel.json not found — SPA routes will 404!"
        exit 1
    fi

    [ -f components.json ] && cp components.json "${TEMP_DIR}/"

    if [ -n "${API_URL}" ]; then
        echo "VITE_API_BASE_URL=${API_URL}" > "${TEMP_DIR}/.env"
        echo "  API_URL: ${API_URL}"
    fi

    if [ -n "${VERCEL_PROJECT}" ]; then
        mkdir -p "${TEMP_DIR}/.vercel"
        cat > "${TEMP_DIR}/.vercel/project.json" << EOF
{
  "projectId": "${VERCEL_PROJECT}",
  "orgId": "${VERCEL_ORG}"
}
EOF
    fi

    echo "  Package size: $(du -sh "${TEMP_DIR}" | cut -f1)"

    cd "${TEMP_DIR}"

    VERCEL_ARGS=()
    [ -n "${VERCEL_TOKEN}" ] && VERCEL_ARGS+=(--token="${VERCEL_TOKEN}")

    vercel "${VERCEL_ARGS[@]}"

    echo ""
    echo "================================================="
    echo "  Frontend [test] deployed to Vercel"
    echo "================================================="
}

# =============================================================================
# Dispatch
# =============================================================================
case "$MODE" in
    backend)  deploy_backend ;;
    frontend) deploy_frontend ;;
esac
