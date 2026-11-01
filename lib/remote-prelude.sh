# shellcheck shell=bash disable=SC2154
# 在电脑上执行的脚本主体，由 pcrun 拼在几个变量定义后面一起发送：
#   __pcb_cmd     要执行的命令
#   __pcb_start   Claude 当前目录对应的电脑目录（不在挂载目录下时是 PC_ROOT）
#   __pcb_claude  同上，但不在挂载目录下时为空，用来判断 Claude 这边的目录有没有变
#   __pcb_state   会话状态文件，为空表示不保存状态
# 发送前会去掉以 # 开头的注释行和空行，减小命令长度。

# 恢复上一条命令保存的状态。必须在最外层 source：在函数里 source 的话，declare 定义的变量会变成局部变量
# Git Bash 里每启动一个外部程序要几十毫秒，这里尽量只用 bash 内置命令
if [ -n "$__pcb_state" ]; then
  [ -d "${__pcb_state%/*}" ] || mkdir -p "${__pcb_state%/*}" 2>/dev/null
  if [ -f "$__pcb_state" ]; then
    . "$__pcb_state" >/dev/null 2>&1
  fi
  # 偶尔清理 7 天没用过的会话状态
  if [ $((RANDOM % 50)) -eq 0 ]; then
    find "${__pcb_state%/*}" -name '*.sh' -mtime +7 -delete 2>/dev/null
  fi
fi

# 当前目录：Claude 这边的目录没变，就回到电脑上一条命令结束时的目录；变了（如用户执行了 /cd）就用 Claude 的
if [ -n "${__pcb_saved_cwd-}" ] && [ "${__pcb_saved_claude-}" = "$__pcb_claude" ] &&
  cd "$__pcb_saved_cwd" 2>/dev/null; then
  :
elif ! cd "$__pcb_start"; then
  exit 1
fi
unset __pcb_saved_cwd __pcb_saved_claude

__pcb_save() {
  __pcb_rc=$?
  trap - EXIT
  if [ -n "$__pcb_state" ]; then
    # 每次连接都会变的变量不保存
    export -n SSH_CLIENT SSH_CONNECTION SSH_TTY SHLVL PWD 2>/dev/null
    unset -f __pcb_save
    # 直接覆盖写，不用临时文件加 mv（mv 是外部程序）。同一会话并行执行的命令偶尔可能读到写了一半的状态，
    # 影响只是少恢复几个变量
    {
      printf '__pcb_saved_cwd=%q\n__pcb_saved_claude=%q\nOLDPWD=%q\n' "$PWD" "$__pcb_claude" "${OLDPWD-}"
      export -p
      declare -f
      alias -p
      shopt -p
    } >"$__pcb_state" 2>/dev/null
  fi
  # 告诉 pcrun 命令结束时的目录，由它从标准错误里取出，不会显示出来
  printf '\036PCB_CWD:%s\n' "$PWD" >&2
  exit "$__pcb_rc"
}

shopt -s expand_aliases
# 用 trap EXIT 保存状态：命令里执行了 exit 也能保存
trap __pcb_save EXIT
eval "$__pcb_cmd"
