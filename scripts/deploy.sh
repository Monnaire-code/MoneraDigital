#!/bin/bash
# =============================================================================
# Monera Digital - 生产部署脚本（在生产服务器上执行）
#
# 用法:
#   ./deploy.sh              全量部署（编译→停服→迁移→启动）
#   ./deploy.sh build        仅编译+备份，不停服不部署
#   ./deploy.sh deploy       跳过编译，用已有 binary 部署（搭配 build 使用）
#   ./deploy.sh stop         停止服务
#   ./deploy.sh start        启动服务
#   ./deploy.sh restart      重启服务
#   ./deploy.sh --skip-migrate  跳过数据库迁移
#
# 前提：
#   - 在源码目录（含 go.mod）执行（build/deploy 时）
#   - /home/ec2-user/monera/.env 已配置
#   - ec2-user 有 sudo 权限（用于 systemctl）
#   - 服务器已安装 Go（版本需匹配 go.mod，build 时）
# =============================================================================

set -e

SERVICE_NAME="monera-digital"

# stop/start/restart 直接执行，不需要 go.mod 等预检
case "${1:-}" in
    stop)
        sudo systemctl stop "${SERVICE_NAME}"
        echo "✓ ${SERVICE_NAME} 已停止"
        exit 0
        ;;
    start)
        sudo systemctl start "${SERVICE_NAME}"
        echo "✓ ${SERVICE_NAME} 已启动"
        exit 0
        ;;
    restart)
        sudo systemctl restart "${SERVICE_NAME}"
        echo "✓ ${SERVICE_NAME} 已重启"
        exit 0
        ;;
esac

SKIP_BUILD=false
SKIP_MIGRATE=false
BUILD_ONLY=false

while [[ $# -gt 0 ]]; do
    case $1 in
        build)          BUILD_ONLY=true; shift ;;
        deploy)         SKIP_BUILD=true; shift ;;
        --skip-migrate) SKIP_MIGRATE=true; shift ;;
        --help|-h|help)
            echo "用法: ./deploy.sh [command] [options]"
            echo ""
            echo "Commands:"
            echo "  (无)      全量部署（编译→停服→迁移→启动）"
            echo "  build     仅编译+备份，不停服不部署"
            echo "  deploy    跳过编译，用已有 binary 部署"
            echo "  stop      停止服务"
            echo "  start     启动服务"
            echo "  restart   重启服务"
            echo ""
            echo "Options:"
            echo "  --skip-migrate  跳过数据库迁移"
            exit 0
            ;;
        *) echo "未知参数: $1（./deploy.sh help 查看用法）"; exit 1 ;;
    esac
done

APP_DIR="/home/ec2-user/monera"
BINARY_NAME="server"

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

# deploy 模式时确认 binary 已存在
if [ "$SKIP_BUILD" = true ]; then
    if [ ! -x "${APP_DIR}/${BINARY_NAME}" ]; then
        echo "FATAL: deploy 模式但 ${APP_DIR}/${BINARY_NAME} 不存在，请先执行 ./deploy.sh build"
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
        mv "${APP_DIR}/${BINARY_NAME}" "${APP_DIR}/${BINARY_NAME}.bak"
        echo "  已备份: ${APP_DIR}/${BINARY_NAME}.bak"
    fi
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -ldflags="-s -w" -o "${BINARY_NAME}" ./cmd/server/main.go
    echo "  编译完成: ${BINARY_NAME}"
else
    echo "[2/6] 跳过编译（deploy 模式）"
fi

# build 模式：仅编译+备份，不停服不部署
if [ "$BUILD_ONLY" = true ]; then
    echo ""
    echo "=============================================="
    echo "  编译完成（build 模式，服务未受影响）"
    echo "  新 binary:  $(pwd)/${BINARY_NAME}"
    echo "  备份:       ${APP_DIR}/${BINARY_NAME}.bak"
    echo "  下次部署:   ./deploy.sh deploy"
    echo "=============================================="
    exit 0
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
    chmod +x "${APP_DIR}/${BINARY_NAME}"
    echo "  已复制到 ${APP_DIR}"
fi

# -----------------------------------------------------------------------------
# 5. 运行数据库迁移
# -----------------------------------------------------------------------------
if [ "$SKIP_MIGRATE" = false ]; then
    echo "[5/6] 运行数据库迁移..."
    (set -a && source "${APP_DIR}/.env" && set +a && go run ./cmd/migrate/) || {
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
