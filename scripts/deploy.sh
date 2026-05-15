#!/bin/bash
# =============================================================================
# Monera Digital - 生产部署脚本（在生产服务器上执行）
#
# 用法:
#   bash scripts/deploy.sh [--skip-build] [--skip-migrate]
#
# 前提：
#   - 在源码目录（含 go.mod）执行
#   - /home/ec2-user/monera/.env 已配置
#   - ec2-user 有 sudo 权限（用于 systemctl）
#   - 服务器已安装 Go（版本需匹配 go.mod）
# =============================================================================

set -e

SKIP_BUILD=false
SKIP_MIGRATE=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-build)   SKIP_BUILD=true; shift ;;
        --skip-migrate) SKIP_MIGRATE=true; shift ;;
        --help|-h)
            echo "用法: bash scripts/deploy.sh [--skip-build] [--skip-migrate]"
            exit 0
            ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

APP_DIR="/home/ec2-user/monera"
SERVICE_NAME="monera-digital"
BINARY_NAME="server"
MIGRATE_NAME="monera-migrate"

# 确认在源码目录
if [ ! -f "go.mod" ]; then
    echo "FATAL: go.mod not found. 请在源码目录执行此脚本。"
    exit 1
fi

if [ ! -f "${APP_DIR}/.env" ]; then
    echo "FATAL: ${APP_DIR}/.env 不存在，请先配置环境变量。"
    exit 1
fi
chmod 600 "${APP_DIR}/.env"

# --skip-build 时确认 binary 已存在
if [ "$SKIP_BUILD" = true ]; then
    if [ ! -x "${APP_DIR}/${BINARY_NAME}" ]; then
        echo "FATAL: --skip-build 指定但 ${APP_DIR}/${BINARY_NAME} 不存在，请先完整部署一次。"
        exit 1
    fi
fi

# 从 .env 读取端口，默认 8081
ACTUAL_PORT=$(grep '^PORT=' "${APP_DIR}/.env" | cut -d= -f2 | tr -d '[:space:]')
ACTUAL_PORT="${ACTUAL_PORT:-8081}"

echo "=== Monera Digital 生产部署 ==="
echo "  APP_DIR:  ${APP_DIR}"
echo "  SERVICE:  ${SERVICE_NAME}"
echo "  PORT:     ${ACTUAL_PORT}"
echo "  BRANCH:   $(git rev-parse --abbrev-ref HEAD)"
echo "  COMMIT:   $(git rev-parse --short HEAD)"
echo ""

# -----------------------------------------------------------------------------
# 1. 确认代码状态
# -----------------------------------------------------------------------------
echo "[1/6] 确认代码状态..."
git status --short

# -----------------------------------------------------------------------------
# 2. 编译
# -----------------------------------------------------------------------------
if [ "$SKIP_BUILD" = false ]; then
    echo "[2/6] 编译 Go binary..."
    # 编译前备份现有 binary，用于迁移失败时自动回滚
    if [ -f "${APP_DIR}/${BINARY_NAME}" ]; then
        cp "${APP_DIR}/${BINARY_NAME}" "${APP_DIR}/${BINARY_NAME}.bak"
        echo "  已备份: ${APP_DIR}/${BINARY_NAME}.bak"
    fi
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -ldflags="-s -w" -o "${BINARY_NAME}" ./cmd/server/main.go
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -ldflags="-s -w" -o "${MIGRATE_NAME}" ./cmd/migrate/
    echo "  编译完成: ${BINARY_NAME}, ${MIGRATE_NAME}"
else
    echo "[2/6] 跳过编译（--skip-build）"
fi

# -----------------------------------------------------------------------------
# 3. 停止服务（先 systemctl stop，再清理残留 nohup 进程）
# -----------------------------------------------------------------------------
echo "[3/6] 停止服务..."
if systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
    sudo systemctl stop "${SERVICE_NAME}"
    echo "  systemd service 已停止"
fi
# 首次迁移：清理旧 nohup 进程
OLD_PIDS=$(pgrep -f "${APP_DIR}/server" 2>/dev/null || true)
if [ -n "$OLD_PIDS" ]; then
    echo "  停止残留 nohup 进程 (PID: ${OLD_PIDS})..."
    kill $OLD_PIDS 2>/dev/null || true
    sleep 2
    kill -9 $OLD_PIDS 2>/dev/null || true
    echo "  残留进程已清理"
fi

# -----------------------------------------------------------------------------
# 4. 复制 binary 到部署目录
# -----------------------------------------------------------------------------
echo "[4/6] 部署 binary..."
if [ "$SKIP_BUILD" = false ]; then
    cp "${BINARY_NAME}" "${APP_DIR}/${BINARY_NAME}"
    cp "${MIGRATE_NAME}" "${APP_DIR}/${MIGRATE_NAME}"
    chmod +x "${APP_DIR}/${BINARY_NAME}" "${APP_DIR}/${MIGRATE_NAME}"
    echo "  已复制到 ${APP_DIR}"
fi

# -----------------------------------------------------------------------------
# 5. 运行数据库迁移
# -----------------------------------------------------------------------------
if [ "$SKIP_MIGRATE" = false ]; then
    echo "[5/6] 运行数据库迁移..."
    (cd "${APP_DIR}" && ./${MIGRATE_NAME}) || {
        echo "FATAL: 迁移失败，自动回滚..."
        if [ -f "${APP_DIR}/${BINARY_NAME}.bak" ]; then
            cp "${APP_DIR}/${BINARY_NAME}.bak" "${APP_DIR}/${BINARY_NAME}"
            sudo systemctl start "${SERVICE_NAME}" \
                && echo "  旧版本已恢复并启动" \
                || echo "  ⚠ 旧版恢复启动失败，请手动处理"
        else
            echo "  无备份文件，无法自动回滚，请手动恢复"
        fi
        exit 1
    }
    echo "  迁移完成"
else
    echo "[5/6] 跳过迁移（--skip-migrate）"
fi

# -----------------------------------------------------------------------------
# 6. 安装/更新 systemd service 并启动
# -----------------------------------------------------------------------------
echo "[6/6] 配置 systemd service..."
sudo bash -c "cat > /etc/systemd/system/${SERVICE_NAME}.service" << EOF
[Unit]
Description=Monera Digital
After=network.target

[Service]
Type=simple
User=ec2-user
WorkingDirectory=${APP_DIR}
EnvironmentFile=${APP_DIR}/.env
ExecStart=${APP_DIR}/${BINARY_NAME}
Restart=always
RestartSec=10
Environment=GIN_MODE=release
Environment=PORT=${ACTUAL_PORT}
NoNewPrivileges=yes
PrivateTmp=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable "${SERVICE_NAME}"
sudo systemctl start "${SERVICE_NAME}"

# -----------------------------------------------------------------------------
# 健康检查（最多重试 5 次）
# -----------------------------------------------------------------------------
echo ""
echo "健康检查..."
for i in 1 2 3 4 5; do
    if curl -sf "http://localhost:${ACTUAL_PORT}/api/health" > /dev/null 2>&1; then
        echo "  ✓ 服务健康检查通过"
        break
    fi
    if [ $i -eq 5 ]; then
        echo "  ✗ 健康检查失败（5 次重试后），最近 50 行日志："
        sudo journalctl -u "${SERVICE_NAME}" --no-pager -n 50
        exit 1
    fi
    echo "  等待服务就绪 ($i/5)..."
    sleep 2
done

echo ""
echo "=============================================="
echo "  部署成功！"
echo "  Commit: $(git rev-parse --short HEAD)"
echo "  日志:   journalctl -u ${SERVICE_NAME} -f"
echo "  状态:   systemctl status ${SERVICE_NAME}"
echo "=============================================="
