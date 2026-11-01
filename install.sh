#!/bin/bash
# 在 VPS 上安装 claude-pc-bridge。
#   ./install.sh            安装脚本、生成配置文件、把钩子写入 Claude Code 设置
#   ./install.sh --service  另外安装并启动 pc-sshfs 挂载服务（需要 sudo）
# 环境变量 PREFIX 指定安装位置，默认 ~/.local/share/claude-pc-bridge
set -euo pipefail

SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
PREFIX="${PREFIX:-$HOME/.local/share/claude-pc-bridge}"
CONFIG_DIR="$HOME/.config/claude-pc-bridge"
SETTINGS="$HOME/.claude/settings.json"

for dep in jq perl ssh sshfs; do
  command -v "$dep" >/dev/null || { echo "缺少依赖: $dep" >&2; exit 1; }
done

# 1. 安装脚本。复制到本地而不是直接用仓库里的文件：仓库可能就在挂载目录里，隧道断开时钩子也要能运行
mkdir -p "$PREFIX"
cp -r "$SRC_DIR/bin" "$SRC_DIR/lib" "$PREFIX/"
chmod +x "$PREFIX"/bin/*
echo "已安装脚本到 $PREFIX"

# 2. 配置文件，已存在则不覆盖
mkdir -p "$CONFIG_DIR"
if [ ! -f "$CONFIG_DIR/config.env" ]; then
  cp "$SRC_DIR/config.example.env" "$CONFIG_DIR/config.env"
  echo "已生成 $CONFIG_DIR/config.env，请按自己的环境修改"
else
  echo "保留已有配置 $CONFIG_DIR/config.env"
fi

# 3. 钩子写入 Claude Code 设置，已有同一钩子则跳过
hook_cmd="$PREFIX/bin/pcrun-hook"
mkdir -p "$(dirname "$SETTINGS")"
[ -f "$SETTINGS" ] || echo '{}' > "$SETTINGS"
if jq -e --arg c "$hook_cmd" '[.hooks.PreToolUse[]?.hooks[]?.command] | index($c)' "$SETTINGS" >/dev/null; then
  echo "Claude Code 设置里已有该钩子"
else
  cp "$SETTINGS" "$SETTINGS.bak.$(date +%Y%m%d%H%M%S)"
  hook=$(sed "s|@PREFIX@|$PREFIX|g" "$SRC_DIR/claude/settings.hook.json")
  jq --argjson h "$hook" '.hooks.PreToolUse = ((.hooks.PreToolUse // []) + $h.hooks.PreToolUse)' \
    "$SETTINGS" > "$SETTINGS.tmp" && mv "$SETTINGS.tmp" "$SETTINGS"
  echo "已把钩子写入 $SETTINGS（原文件已备份）"
fi

# 4. 可选：挂载服务
if [ "${1:-}" = "--service" ]; then
  unit=$(sed -e "s|@USER@|$(id -un)|g" -e "s|@PREFIX@|$PREFIX|g" "$SRC_DIR/vps/pc-sshfs.service.in")
  echo "$unit" | sudo tee /etc/systemd/system/pc-sshfs.service >/dev/null
  sudo systemctl daemon-reload
  sudo systemctl enable --now pc-sshfs
  echo "已安装并启动 pc-sshfs 服务，查看状态: systemctl status pc-sshfs"
fi
