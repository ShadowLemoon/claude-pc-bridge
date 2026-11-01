# shellcheck shell=bash
# 公共函数：读取配置，计算挂载目录在命令里可能出现的几种写法。
# 由 bin/ 下的脚本 source，不单独执行。

pc_bridge_load_config() {
  local config="${PC_BRIDGE_CONFIG:-$HOME/.config/claude-pc-bridge/config.env}"
  # 始终在 VPS 上执行的程序。在配置文件里先设好默认值，配置文件可以整个改写，也可以用 += 追加
  PC_BRIDGE_LOCAL_PROGRAMS="multica claude systemctl journalctl sudo crontab sshfs fusermount fusermount3 pc-sshfs"
  if [ -f "$config" ]; then
    # shellcheck disable=SC1090
    . "$config"
  fi

  PC_SSH_USER="${PC_SSH_USER:-}"
  PC_SSH_HOST="${PC_SSH_HOST:-localhost}"
  PC_SSH_PORT="${PC_SSH_PORT:-2222}"
  PC_SSH_KEY="${PC_SSH_KEY:-$HOME/.ssh/id_ed25519_pc}"
  MOUNT="${MOUNT:-$HOME/work/pc-code}"
  MOUNT="${MOUNT%/}"
  PC_ROOT="${PC_ROOT:-/d/Code}"
  PC_ROOT="${PC_ROOT%/}"
  PC_SFTP_PATH="${PC_SFTP_PATH:-/D:/Code}"
  # ssh 连接复用的控制文件放在这里
  PC_BRIDGE_CACHE="${PC_BRIDGE_CACHE:-$HOME/.cache/claude-pc-bridge}"

  # 挂载目录的几种写法：绝对路径，以及在 $HOME 下时的 ~/...、$HOME/...、${HOME}/...
  MOUNT_ALIASES=("$MOUNT")
  case "$MOUNT/" in
    "$HOME"/*)
      local rel="${MOUNT#"$HOME"}"
      MOUNT_ALIASES+=("~$rel" "\$HOME$rel" "\${HOME}$rel")
      ;;
  esac
}

pc_bridge_require_user() {
  if [ -z "$PC_SSH_USER" ]; then
    echo "claude-pc-bridge: 未配置 PC_SSH_USER，请先编辑 ${PC_BRIDGE_CONFIG:-$HOME/.config/claude-pc-bridge/config.env}" >&2
    exit 2
  fi
}

# 登录 PC 的 ssh 公共参数
pc_bridge_ssh_opts() {
  SSH_OPTS=(-i "$PC_SSH_KEY" -p "$PC_SSH_PORT" -o BatchMode=yes -o IdentitiesOnly=yes
            -o ServerAliveInterval=15 -o ServerAliveCountMax=3)
}
