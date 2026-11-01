#!/bin/bash
# 测试钩子的转发判断、路径换算和会话状态。不连接电脑，只需要 bash、perl 和 jq。
# 用法: bash test/test.sh
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

export HOME="$TMP/home"
mkdir -p "$HOME/work/pc-code/proj/sub" "$HOME/work/other"
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

M="$HOME/work/pc-code"
PCRUN="$ROOT/bin/pcrun"
SESSION="$ROOT/bin/pcrun-session"

# ---- 钩子：是否转发 ----
# 返回 "local" 或改写后的命令
route() {
  local out
  out=$(jq -n --arg c "$1" --arg m "$2" '{session_id:"s1",cwd:$c,tool_input:{command:$m}}' | bash "$ROOT/bin/pcrun-hook")
  if [ -z "$out" ]; then echo local; else jq -r .hookSpecificOutput.updatedInput.command <<<"$out"; fi
}
fwd() { local q="'\\''"; printf ". '%s' 's1' '%s'" "$SESSION" "${1//\'/$q}"; }

check "挂载目录外的普通命令留在本地" local "$(route "$HOME" 'ls')"
check "挂载目录下的命令转发" "$(fwd 'git status')" "$(route "$M/proj" 'git status')"
check "命令里有绝对路径时转发" "$(fwd "cd $M/proj && git status")" "$(route "$HOME" "cd $M/proj && git status")"
check "命令里有 ~/ 路径时转发" "$(fwd 'git -C ~/work/pc-code/proj log')" "$(route "$HOME" 'git -C ~/work/pc-code/proj log')"
check "命令里有 \$HOME 路径时转发" local "$(route "$HOME" 'ls $HOME/work/other')"
check "#local 单独一行时留在本地" local "$(route "$M/proj" $'#local\nls')"
check "#local 和命令同一行时去掉前缀、留在本地" "ls ~/work/pc-code" "$(route "$M/proj" '#local ls ~/work/pc-code')"
check "只有 #local 一行时不改写" local "$(route "$M/proj" '#local')"
check "已经是 pcrun 调用不再改写" local "$(route "$HOME" "$PCRUN git status")"
check "已经是 pcrun-session 调用不再改写" local "$(route "$M" "$(fwd 'ls')")"
check "相似但不同的目录不转发" local "$(route "$HOME" 'ls ~/work/other')"
check "以挂载路径开头的其他目录不转发" local "$(route "$HOME" 'ls ~/work/pc-code-old')"
check "相对路径落在挂载目录里时转发" "$(fwd 'cd pc-code/proj && git status')" "$(route "$HOME/work" 'cd pc-code/proj && git status')"
check "../ 相对路径落在挂载目录里时转发" "$(fwd 'ls ../pc-code')" "$(route "$HOME/work/other" 'ls ../pc-code')"
check "相对路径不在挂载目录里时留在本地" local "$(route "$HOME/work" 'ls other/x')"
check "引号里的整句话不算路径" local "$(route "$HOME/work" 'echo "pc-code is here"')"

# ---- 钩子：白名单里的程序始终留在 VPS ----
check "白名单程序在挂载目录下也留在本地" local "$(route "$M/proj" 'multica issue list')"
check "白名单程序写了完整路径" local "$(route "$M/proj" '/usr/local/bin/multica issue list')"
check "白名单程序在 cd 之后" local "$(route "$M/proj" 'cd ~/work/pc-code/proj && multica issue get 1')"
check "白名单程序在管道后面" local "$(route "$M/proj" 'git log | multica issue comment add 1')"
check "白名单程序带变量赋值和 env 前缀" local "$(route "$M/proj" 'FOO=1 env -u X multica x')"
check "白名单程序在 \$(...) 里" local "$(route "$M/proj" 'echo $(multica issue get 1)')"
check "白名单程序在下一行" local "$(route "$M/proj" $'git status\nsudo systemctl restart x')"
check "白名单程序名只作为参数时照常转发" "$(fwd 'git log --grep multica')" "$(route "$M/proj" 'git log --grep multica')"
check "重定向目标不算程序名" "$(fwd 'echo hi > multica')" "$(route "$M/proj" 'echo hi > multica')"
check "2>&1 后面的参数不算程序名" "$(fwd 'ls 2>&1 claude')" "$(route "$M/proj" 'ls 2>&1 claude')"
check "引号里的文字不算程序名" "$(fwd 'echo "x; multica"')" "$(route "$M/proj" 'echo "x; multica"')"
check "heredoc 正文里的白名单程序名不算" "$(fwd $'git commit -F - <<\'EOF\'\nsudo crontab sshfs\nEOF\ngit log')" \
  "$(route "$M/proj" $'git commit -F - <<\'EOF\'\nsudo crontab sshfs\nEOF\ngit log')"
check "<<- heredoc 正文也跳过" "$(fwd $'cat <<-X\n\tmultica\n\tX')" "$(route "$M/proj" $'cat <<-X\n\tmultica\n\tX')"
check "heredoc 结束后的白名单程序照样认出" local "$(route "$M/proj" $'cat <<EOF\nhi\nEOF\nmultica x')"
check "<<< 后面不当 heredoc" local "$(route "$M/proj" $'cat <<< hi\nmultica x')"
check "配置里追加的程序也留在本地" local "$(echo 'PC_BRIDGE_LOCAL_PROGRAMS+=" mytool"' >>"$PC_BRIDGE_CONFIG"; route "$M/proj" 'mytool run')"

# ---- 命令里的路径换算 ----
dry() { (cd "$1" && PC_BRIDGE_DRY_RUN=1 bash "$PCRUN" "$2"); }
check "挂载目录下 cd 到电脑对应目录" "cd /d/Code/proj && git status" "$(dry "$M/proj" 'git status')"
check "挂载目录外从 PC_ROOT 开始" "cd /d/Code && git status" "$(dry "$HOME" 'git status')"
check "替换绝对路径" "cd /d/Code && cd /d/Code/proj" "$(dry "$HOME" "cd $M/proj")"
check "替换 ~/ 路径" "cd /d/Code && git -C /d/Code/proj log" "$(dry "$HOME" 'git -C ~/work/pc-code/proj log')"
check "替换 \${HOME} 路径" "cd /d/Code && ls /d/Code" "$(dry "$HOME" 'ls ${HOME}/work/pc-code')"
check "不替换 pc-code-old" "cd /d/Code && ls $M-old /d/Code/a" "$(dry "$HOME" "ls $M-old $M/a")"
check "替换相对路径，保留通配符" "cd /d/Code && ls /d/Code/proj/*.py" "$(dry "$HOME/work" 'ls pc-code/proj/*.py')"
check "替换引号里的相对路径" "cd /d/Code && cat '/d/Code/proj/a b.txt'" "$(dry "$HOME/work" "cat 'pc-code/proj/a b.txt'")"
check "替换 --opt=相对路径" "cd /d/Code && tool --dir=/d/Code/proj" "$(dry "$HOME/work" 'tool --dir=pc-code/proj')"
check "替换 ../ 相对路径" "cd /d/Code && ls /d/Code" "$(dry "$HOME/work/other" 'ls ../pc-code')"

touch "$HOME/vps-only.txt"
warn=$(cd "$M" && PC_BRIDGE_DRY_RUN=1 bash "$PCRUN" "cat $HOME/vps-only.txt $HOME/missing.txt" 2>&1 >/dev/null)
check "提示只在 VPS 上存在的文件" 1 "$(grep -c "$HOME/vps-only.txt 是 VPS 上的文件" <<<"$warn")"
check "VPS 上也不存在的文件不提示" 0 "$(grep -c missing.txt <<<"$warn")"

# ---- 输出里的路径换算 ----
out() { printf '%s\n' "$1" | MOUNT="$M" PC_ROOT=/d/Code perl "$ROOT/lib/pathmap.pl" filter-out; }
check "输出: Git Bash 路径" "$M/proj/a.py:3: error" "$(out '/d/Code/proj/a.py:3: error')"
check "输出: 反斜杠路径" "File \"$M/proj/a.py\", line 3" "$(out 'File "D:\Code\proj\a.py", line 3')"
check "输出: 正斜杠盘符路径，大小写不同" "$M/proj/x" "$(out 'd:/code/proj/x')"
check "输出: Python repr 里的双反斜杠" "'$M/proj/a.py'" "$(out "'D:\\\\Code\\\\proj\\\\a.py'")"
check "输出: 只有根目录" "cwd=$M" "$(out 'cwd=/d/Code')"
check "输出: 名字相近的目录不换" "/d/Codex/a X:/Code/a" "$(out '/d/Codex/a X:/Code/a')"
check "输出: PC_BRIDGE_RAW_OUTPUT=1 不换" "/d/Code/a" "$(printf '/d/Code/a\n' | PC_BRIDGE_RAW_OUTPUT=1 MOUNT="$M" PC_ROOT=/d/Code perl "$ROOT/lib/pathmap.pl" filter-out)"

f="$TMP/cwdfile"
err=$(printf 'warn\n\036PCB_CWD:/d/Code/proj/sub\n' | MOUNT="$M" PC_ROOT=/d/Code perl "$ROOT/lib/pathmap.pl" filter-err "$f")
check "标准错误: 去掉目录标记" "warn" "$err"
check "标准错误: 目录换算成挂载路径" "$M/proj/sub" "$(cat "$f")"
printf 'oops\036PCB_CWD:/c/Users/x\n' | MOUNT="$M" PC_ROOT=/d/Code perl "$ROOT/lib/pathmap.pl" filter-err "$f" >/dev/null 2>&1
check "标准错误: 目录不在 PC_ROOT 下时写空" "" "$(cat "$f")"

# ---- 会话状态：用本机 bash 模拟电脑执行 ----
# 用一个 Linux 路径当 PC_ROOT，同时覆盖用户主机是 Linux 的情况
PCDIR="$TMP/pc"
mkdir -p "$PCDIR/proj/sub"
cat > "$TMP/local.env" <<EOF
MOUNT=\$HOME/work/pc-code
PC_ROOT=$PCDIR
EOF
sess() { (cd "$1" && PC_BRIDGE_CONFIG="$TMP/local.env" PC_BRIDGE_LOCAL_EXEC=1 . "$SESSION" t1 "$2" && pwd); }
check "cd 后同步 Claude 的目录" "$M/proj/sub" "$(sess "$M/proj" 'cd sub')"
check "下一条命令还在 cd 后的目录" "$M/proj/sub
$M/proj/sub" "$(sess "$M/proj/sub" 'pwd')"
sess "$M/proj/sub" 'export FOO=bar; greet() { echo "hi $1"; }; alias ll="echo LL"; shopt -s globstar' >/dev/null
check "保留环境变量" "bar" "$(sess "$M/proj/sub" 'echo $FOO' | head -1)"
check "保留函数" "hi x" "$(sess "$M/proj/sub" 'greet x' | head -1)"
check "保留别名" "LL" "$(sess "$M/proj/sub" 'll' | head -1)"
check "保留 shopt 选项" "on" "$(sess "$M/proj/sub" 'shopt globstar | cut -f2' | head -1 | tr -d ' ')"
check "Claude 的目录变了时用 Claude 的目录" "$M/proj
$M/proj" "$(sess "$M/proj" 'pwd')"
sess "$M/proj" 'cd /' >/dev/null
check "cd 到挂载目录外时 Claude 不跟着动" "/
$M/proj" "$(sess "$M/proj" 'pwd')"
sess "$M/proj" "cd $PCDIR/proj" >/dev/null
check "exit 也能保存状态并返回退出码" "3" "$(sess "$M/proj" 'export N=3; exit 3' >/dev/null; echo $?)"
check "exit 前设置的变量保留" "3" "$(sess "$M/proj" 'echo $N' | head -1)"
check "语法错误不影响状态保存" "3" "$(sess "$M/proj" 'if then' >/dev/null 2>&1; sess "$M/proj" 'echo $N' | head -1)"
check "不同会话互不影响" "" "$(cd "$M/proj" && PC_BRIDGE_CONFIG="$TMP/local.env" PC_BRIDGE_LOCAL_EXEC=1 . "$SESSION" t2 'echo $N')"
check "中文参数原样传到电脑" "提交说明" "$(sess "$M/proj" 'echo 提交说明' | head -1)"
check "命令输出里的电脑路径换成挂载路径" "$M/proj/sub/a.py" "$(sess "$M/proj" "echo $PCDIR/proj/sub/a.py" | head -1)"
check "反斜杠、引号、中文原样传到电脑" "a\\|b x\"y it's 中文 end\\" \
  "$(cd "$M/proj" && PC_BRIDGE_CONFIG="$TMP/local.env" PC_BRIDGE_LOCAL_EXEC=1 bash "$PCRUN" "echo 'a\\|b' \"x\\\"y\" \"it's\" 中文 'end\\'")"
check "管道输入能传到电脑上的命令" "piped" "$(echo piped | (cd "$M/proj" && PC_BRIDGE_CONFIG="$TMP/local.env" PC_BRIDGE_LOCAL_EXEC=1 bash "$PCRUN" 'cat'))"

echo "通过 $pass，失败 $fail"
[ "$fail" -eq 0 ]
