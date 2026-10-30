#!/bin/bash
# 测试钩子的转发判断和 pcrun 的路径替换。不连接 PC，只需要 bash 和 jq。
# 用法: bash test/test.sh
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

export HOME="$TMP/home"
mkdir -p "$HOME/work/pc-code/proj"
cat > "$TMP/config.env" <<'EOF'
PC_SSH_USER=tester
MOUNT=$HOME/work/pc-code
PC_ROOT=/d/Code
EOF
export PC_BRIDGE_CONFIG="$TMP/config.env"

pass=0
fail=0
check() {
  local name="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf '失败: %s\n  期望: %s\n  实际: %s\n' "$name" "$expected" "$actual"
  fi
}

# 返回 "local" 或改写后的命令
route() {
  local out
  out=$(jq -n --arg c "$1" --arg m "$2" '{cwd:$c,tool_input:{command:$m}}' | bash "$ROOT/bin/pcrun-hook")
  if [ -z "$out" ]; then echo local; else jq -r .hookSpecificOutput.updatedInput.command <<<"$out"; fi
}

M="$HOME/work/pc-code"
PCRUN="$ROOT/bin/pcrun"
check "挂载目录外的普通命令留在本地" local "$(route "$HOME" 'ls')"
check "挂载目录下的命令转发" "$PCRUN 'git status'" "$(route "$M/proj" 'git status')"
check "命令里有绝对路径时转发" "$PCRUN 'cd $M/proj && git status'" "$(route "$HOME" "cd $M/proj && git status")"
check "命令里有 ~/ 路径时转发" "$PCRUN 'git -C ~/work/pc-code/proj log'" "$(route "$HOME" 'git -C ~/work/pc-code/proj log')"
check "命令里有 \$HOME 路径时转发" "$PCRUN 'ls \$HOME/work/pc-code'" "$(route "$HOME" 'ls $HOME/work/pc-code')"
check "#local 留在本地" local "$(route "$M/proj" '#local ls')"
check "已经是 pcrun 调用不再改写" local "$(route "$HOME" "$PCRUN git status")"
check "相似但不同的目录不转发" local "$(route "$HOME" 'ls ~/work/other')"

dry() { (cd "$1" && PC_BRIDGE_DRY_RUN=1 bash "$PCRUN" "$2"); }
check "挂载目录下 cd 到 PC 对应目录" "cd /d/Code/proj && git status" "$(dry "$M/proj" 'git status')"
check "挂载目录外从 PC_ROOT 开始" "cd /d/Code && git status" "$(dry "$HOME" 'git status')"
check "替换绝对路径" "cd /d/Code && cd /d/Code/proj" "$(dry "$HOME" "cd $M/proj")"
check "替换 ~/ 路径" "cd /d/Code && git -C /d/Code/proj log" "$(dry "$HOME" 'git -C ~/work/pc-code/proj log')"
check "替换 \${HOME} 路径" "cd /d/Code && ls /d/Code" "$(dry "$HOME" 'ls ${HOME}/work/pc-code')"

echo "通过 $pass，失败 $fail"
[ "$fail" -eq 0 ]
