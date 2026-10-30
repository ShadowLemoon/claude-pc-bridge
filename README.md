# claude-pc-bridge

让跑在 Linux VPS 上的 Claude Code 操作你 Windows 电脑上的代码：
**文件通过 sshfs 挂载到 VPS 读写，Bash 命令自动转发到电脑的 Git Bash 里执行。**

适合的情况：电脑上有项目依赖的工具链（Git for Windows、Python、uv、游戏客户端等），但想把 Claude Code 跑在一台常开的 VPS 上。

## 原理

```
 Windows 电脑                                   Linux VPS
┌──────────────────────┐  ssh -N vps-tunnel   ┌──────────────────────────────────┐
│ OpenSSH Server :22   │ ───────────────────▶ │ localhost:2222 → 电脑的 22 端口   │
│ 默认 shell: Git Bash │   (RemoteForward)    │                                  │
│                      │                      │ pc-sshfs 服务                     │
│ D:\Code  ◀───────────┼────── sshfs ─────────┤   挂载到 ~/work/pc-code           │
│                      │                      │                                  │
│ Git Bash 执行命令 ◀──┼────── pcrun ─────────┤ Claude Code                       │
└──────────────────────┘     (ssh 调用)       │   PreToolUse 钩子 pcrun-hook      │
                                              └──────────────────────────────────┘
```

- **反向隧道**：电脑主动连 VPS，把 VPS 的 `localhost:2222` 转发到电脑的 22 端口。电脑不需要公网 IP，也不用开放入站端口。
- **sshfs 挂载**：VPS 通过隧道把电脑的 `D:\Code` 挂到 `~/work/pc-code`。Claude Code 的读、改文件工具直接操作这个目录。
- **命令转发**：Claude Code 每次执行 Bash 命令前会调用钩子 `pcrun-hook`。满足以下任一条件时，钩子把命令改写成 `pcrun '<命令>'`，通过 ssh 到电脑的 Git Bash 里执行：
  1. 会话工作目录在 `~/work/pc-code` 下；
  2. 命令里出现了挂载目录路径（`/home/<你>/work/pc-code`、`~/work/pc-code`、`$HOME/work/pc-code`）。

  `pcrun` 会 `cd` 到电脑上对应的目录（如 `~/work/pc-code/proj` → `/d/Code/proj`），并把命令里的挂载路径换成电脑上的路径。

为什么不直接在 VPS 上对挂载目录跑 git：sshfs 上的文件所有者和 VPS 用户对不上，git 会报 `dubious ownership`；而且 `git status` 要逐个读取文件信息，隔着网络非常慢。

## 目录

| 路径 | 用途 |
|---|---|
| `bin/pcrun` | 在电脑上执行一条命令 |
| `bin/pcrun-hook` | Claude Code 钩子，决定是否转发 |
| `bin/pc-sshfs` | 挂载 / 卸载 / 检查隧道（给 systemd 服务用） |
| `lib/common.sh` | 读取配置等公共函数 |
| `config.example.env` | 配置示例 |
| `install.sh` | VPS 端安装脚本 |
| `vps/pc-sshfs.service.in` | systemd 服务模板 |
| `vps/sshd_config.d/45-reverse-tunnel.conf` | VPS sshd 配置：及时释放断开的隧道 |
| `pc/ssh_config.example` | 电脑端的隧道配置 |
| `claude/settings.hook.json` | 写入 Claude Code 设置的钩子片段 |
| `claude/CLAUDE.md.example` | 加到 `~/.claude/CLAUDE.md` 的工作规则：实际工作都在电脑上做 |
| `test/test.sh` | 转发判断和路径替换的测试 |

## 安装

### 1. 电脑端（Windows）

1. **安装 OpenSSH Server 和 Git for Windows**，并启动 sshd：
   ```powershell
   # 管理员 PowerShell
   Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
   Set-Service sshd -StartupType Automatic
   Start-Service sshd
   ```
2. **把 ssh 登录的默认 shell 设为 Git Bash**。只影响通过 ssh 登录时用的 shell，不影响本机终端：
   ```powershell
   # 管理员 PowerShell
   New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell -Value "C:\Program Files\Git\bin\bash.exe" -PropertyType String -Force
   New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShellCommandOption -Value "-c" -PropertyType String -Force
   Restart-Service sshd
   ```
3. **允许 VPS 的密钥登录电脑**：在 VPS 上生成密钥 `ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_pc`，把 `id_ed25519_pc.pub` 的内容加到电脑上：
   - 普通用户：`C:\Users\<你>\.ssh\authorized_keys`
   - **管理员组用户**：Windows OpenSSH 默认读的是 `C:\ProgramData\ssh\administrators_authorized_keys`（权限只能有 Administrators 和 SYSTEM），写在用户目录下不生效。
4. **配置反向隧道**：把 `pc/ssh_config.example` 的内容追加到电脑的 `~/.ssh/config`，改成你的 VPS 地址、用户和密钥。然后开启隧道：
   ```bash
   ssh -N vps-tunnel
   ```
   这个窗口要一直开着。电脑重启或睡眠后要重新执行。

### 2. VPS 端（Linux）

1. **安装依赖**：`sudo apt install sshfs jq`
2. **首次连接电脑，记录主机指纹**（之后都是非交互连接，没记录指纹会直接失败）：
   ```bash
   ssh -i ~/.ssh/id_ed25519_pc -p 2222 <电脑用户名>@localhost true
   ```
3. **安装**：
   ```bash
   ./install.sh
   ```
   会把脚本复制到 `~/.local/share/claude-pc-bridge`，生成 `~/.config/claude-pc-bridge/config.env`，并把钩子写入 `~/.claude/settings.json`（会先备份）。脚本要复制出来再用，因为仓库本身可能就在挂载目录里，隧道断开时钩子也必须能运行。
4. **编辑配置** `~/.config/claude-pc-bridge/config.env`，至少填好 `PC_SSH_USER`。然后检查：
   ```bash
   ~/.local/share/claude-pc-bridge/bin/pc-sshfs check
   ```
5. **安装挂载服务**（需要 sudo）：
   ```bash
   ./install.sh --service
   ```
   服务启动时会先清掉残留的失效挂载，等隧道可用后再挂载；隧道断开或 sshfs 崩溃后 5 秒自动重启。
6. **（推荐）VPS sshd 配置**：把 `vps/sshd_config.d/45-reverse-tunnel.conf` 放到 `/etc/ssh/sshd_config.d/`，再 `sudo systemctl reload ssh`。电脑掉线后 VPS 约 45 秒就能释放 2222 端口，重连时不会因为端口还被占用而失败。
7. **（推荐）告诉 Claude 在电脑上干活**：把 `claude/CLAUDE.md.example` 的内容加到 VPS 的 `~/.claude/CLAUDE.md`。这个文件每次会话都会加载，让 Claude 把构建、测试、git、大范围搜索都放到挂载目录里执行（也就是在电脑上执行），不占用 VPS 的资源。

## 使用

- 用 Claude Code 的 `/cd ~/work/pc-code/<项目>` 进入项目，之后的 Bash 命令都会在电脑上执行。
- 在 VPS 上执行、但命令里带挂载路径的操作（如 `ls ~/work/pc-code` 检查挂载、`fusermount -u ~/work/pc-code`），要在命令开头加 `#local`，否则会被转发到电脑：
  ```bash
  #local
  ls ~/work/pc-code
  ```
- 也可以手动调用：`~/.local/share/claude-pc-bridge/bin/pcrun 'git status'`。
- 设置 `PC_BRIDGE_DRY_RUN=1` 时，`pcrun` 只打印要在电脑上执行的命令，不连接电脑，方便排查路径替换。

服务管理：

```bash
systemctl status pc-sshfs
sudo systemctl restart pc-sshfs
journalctl -u pc-sshfs -f
```

## 测试

```bash
bash test/test.sh
```

不需要连接电脑，只依赖 bash 和 jq。覆盖钩子的转发判断（何时转发、何时留在本地）和 `pcrun` 的路径替换。

## 限制

- 每条命令是一次独立的 ssh 调用，不保留上一条命令的 `cd` 和环境变量；需要交互输入的命令不能用。
- 钩子看的是**会话**工作目录，不是命令里的 `cd`。命令里有挂载路径时也会转发，但写成相对路径（如在 `~/work` 下执行 `cd pc-code/proj`）不会被识别。
- 判断是否转发用的是字符串匹配，`~/work/pc-code-old` 这类以挂载路径开头的其他目录也会被转发。
- 命令里有中文时，经过 ssh 传输偶尔会出现编码问题；写 git 提交信息这类长中文文本，建议先写进文件再用 `git commit -F <文件>`。
- 读、改文件走 sshfs，大范围扫描（如对整个挂载目录 `grep -r`）很慢，尽量在项目目录里、经转发在电脑上执行。
