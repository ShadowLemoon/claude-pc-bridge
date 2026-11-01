# claude-pc-bridge

让跑在 Linux VPS 上的 Claude Code 操作你 Windows 电脑上的代码：
**文件通过 sshfs 挂载到 VPS 读写，Bash 命令自动转发到电脑的 Git Bash 里执行。**

适合的情况：电脑上有项目依赖的工具链（Git for Windows、Python、uv、游戏客户端等），但想把 Claude Code 跑在一台常开的 VPS 上。

## 原理

```
 Windows 电脑                                   Linux VPS
┌──────────────────────┐   托盘程序建立隧道   ┌──────────────────────────────────┐
│ OpenSSH Server :22   │ ───────────────────▶ │ localhost:2222 → 电脑的 22 端口   │
│ 默认 shell: Git Bash │   (RemoteForward)    │                                  │
│                      │                      │ pc-sshfs 服务                     │
│ D:\Code  ◀───────────┼────── sshfs ─────────┤   挂载到 ~/work/pc-code           │
│                      │                      │                                  │
│ Git Bash 执行命令 ◀──┼────── pcrun ─────────┤ Claude Code                       │
└──────────────────────┘     (ssh 调用)       │   PreToolUse 钩子 pcrun-hook      │
                                              └──────────────────────────────────┘
```

- **反向隧道**：电脑上的托盘程序 `claude-pc-bridge-tray` 主动连 VPS，把 VPS 的 `localhost:2222` 转发到电脑的 22 端口。电脑不需要公网 IP，也不用开放入站端口。托盘程序自己实现 ssh 连接和代理，不依赖 `ssh.exe`、`connect.exe`，也不读写 `~/.ssh` 下的文件。
- **sshfs 挂载**：VPS 通过隧道把电脑的 `D:\Code` 挂到 `~/work/pc-code`。Claude Code 的读、改文件工具直接操作这个目录。
- **命令转发**：Claude Code 每次执行 Bash 命令前会调用钩子 `pcrun-hook`。满足以下任一条件时，钩子把命令改写成 `. pcrun-session <会话ID> '<命令>'`，通过 ssh 到电脑的 Git Bash 里执行：
  1. 会话工作目录在 `~/work/pc-code` 下；
  2. 命令里出现了挂载目录路径（`/home/<你>/work/pc-code`、`~/work/pc-code`、`$HOME/work/pc-code`），按路径边界匹配，`~/work/pc-code-old` 不算；
  3. 命令里的相对路径按当前目录解析后落在挂载目录里，如在 `~/work` 下执行 `cd pc-code/proj`。

  以下情况不转发，留在 VPS 上执行：
  - 命令以 `#local` 开头；
  - 命令用到了白名单里的程序（配置项 `PC_BRIDGE_LOCAL_PROGRAMS`）。默认是 `multica claude systemctl journalctl sudo crontab sshfs fusermount fusermount3 pc-sshfs`。只要命令里任何一处调用了这些程序，比如写在 `&&`、管道、`$(...)` 后面，或者前面带了 `FOO=1`、`env`，整条命令都留在 VPS 上。按程序名比较，`/usr/local/bin/multica` 也算；只作为参数出现（如 `git log --grep multica`）不算。
- **用起来像同一个 shell**：
  - **状态保留**：同一个 Claude 会话里，上一条命令的 `cd`、`export`、激活的虚拟环境、定义的函数和别名、`shopt` 选项，下一条命令都还在。每条命令结束时把这些保存到电脑上的 `~/.cache/claude-pc-bridge/sessions/<会话ID>.sh`，下一条命令开始前恢复，和 Claude Code 在本机执行命令的做法相同。
  - **目录同步**：电脑上 `cd` 到挂载目录里的其他位置，Claude 的工作目录也跟着变。`cd` 到挂载目录外（如 `/c/Users`）时，电脑上下一条命令还在那里，Claude 的工作目录不变。
  - **连接复用**：所有命令共用一条 ssh 连接（OpenSSH 的 ControlMaster），每条命令约 0.3 秒；以前每条都重新连接，约 1.2 秒。
- **路径换算**：
  - 命令里的挂载路径换成电脑上的路径，如 `~/work/pc-code/proj` → `/d/Code/proj`。
  - 输出里的电脑路径换回挂载路径，如 `D:\Code\proj\a.py`、`/d/Code/proj/a.py` → `/home/<你>/work/pc-code/proj/a.py`，Claude 可以直接拿去读、改文件。设置 `PC_BRIDGE_RAW_OUTPUT=1` 时不换。
  - 转发的命令里用到了只在 VPS 上存在的文件（`$HOME` 或 `/tmp` 下、不在挂载目录里），会提示这个文件在电脑上没有。

为什么不直接在 VPS 上对挂载目录跑 git：sshfs 上的文件所有者和 VPS 用户对不上，git 会报 `dubious ownership`；而且 `git status` 要逐个读取文件信息，隔着网络非常慢。

## 目录

| 路径 | 用途 |
|---|---|
| `bin/pcrun` | 在电脑上执行一条命令 |
| `bin/pcrun-hook` | Claude Code 钩子，决定是否转发 |
| `bin/pcrun-session` | 钩子改写后的命令 source 它：调用 `pcrun` 执行，再同步 Claude 的工作目录 |
| `lib/pathmap.pl` | 路径换算：转发判断、命令里的路径、输出里的路径 |
| `lib/remote-prelude.sh` | 在电脑上执行的脚本主体：恢复和保存会话状态 |
| `bin/pc-sshfs` | 挂载 / 卸载 / 检查隧道（给 systemd 服务用） |
| `lib/common.sh` | 读取配置等公共函数 |
| `config.example.env` | 配置示例 |
| `install.sh` | VPS 端安装脚本 |
| `vps/pc-sshfs.service.in` | systemd 服务模板 |
| `vps/sshd_config.d/45-reverse-tunnel.conf` | VPS sshd 配置：及时释放断开的隧道 |
| `pc/tray/` | 电脑端托盘程序（Go）：保持隧道、断线自动重连、显示状态 |
| `pc/tray/build.sh` | 编译 Windows 和 Linux 版本到 `pc/tray/dist/` |
| `pc/linux/pc-tunnel.service` | 没有桌面的 Linux 用户主机：用 systemd 用户服务运行托盘程序的命令行模式 |
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
4. **编译托盘程序**（需要 Go 1.26 以上），在 Git Bash 里：
   ```bash
   bash pc/tray/build.sh    # 输出 pc/tray/dist/claude-pc-bridge-tray.exe
   ```
   把 exe 放到一个固定位置（如 `%LOCALAPPDATA%\Programs\claude-pc-bridge\`）。开机自动启动记录的是 exe 的路径，移动后要重新勾选。
5. **配置隧道**：双击运行 exe，托盘里出现一个圆点图标。
   1. 第一次运行会生成程序专用的密钥，并用记事本打开配置文件 `%APPDATA%\claude-pc-bridge\tray.json`。填好 VPS 地址、用户名和代理，保存后自动生效：
      ```json
      {
        "host": "vps.example.com",
        "port": 22,
        "user": "your-vps-user",
        "proxy": "socks5://127.0.0.1:7890",
        "remote_listen": "localhost:2222",
        "local_target": "localhost:22",
        "keepalive_seconds": 15,
        "keepalive_max_missed": 3,
        "max_retry_seconds": 60
      }
      ```
      `proxy` 留空表示直接连接，也支持 `http://` 代理。
   2. 右键托盘图标 →「复制公钥」，把公钥追加到 VPS 上 `~/.ssh/authorized_keys` 的末尾。
   3. 程序第一次连上 VPS 时会弹窗显示主机指纹。在 VPS 上运行 `for f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf $f; done` 核对，一致再点「是」。
   4. 图标变绿表示隧道已建好。右键勾选「登录时自动启动」。

   图标颜色：绿色是已连接；黄色是正在连接；红色是连接失败、等待重试，或者配置没填好；灰色是已暂停。鼠标悬停可以看到详情。断线超过 1 分钟还没重连上时会弹系统通知，恢复后再提示一次。

   托盘程序会自动重连：每 15 秒发一次心跳，连续 3 次没回应就断开重连，睡眠唤醒、换网络后约 1 分钟内恢复。连续连不上时，重试间隔从 5 秒开始逐次翻倍，最长 60 秒。程序的配置、密钥、`known_hosts`（主机指纹）和日志 `tunnel.log` 都在 `%APPDATA%\claude-pc-bridge\`，右键菜单里可以直接打开。

   **用户主机是 Linux 时**：有桌面就同样运行 `dist/claude-pc-bridge-tray`，配置目录是 `~/.config/claude-pc-bridge/`；GNOME 需要装 AppIndicator 扩展才能显示托盘图标。没有桌面就用 systemd 用户服务 `pc/linux/pc-tunnel.service`，安装方法写在文件开头的注释里。另外，要把 VPS 端 `config.env` 里的 `PC_ROOT` 和 `PC_SFTP_PATH` 都改成 Linux 上的代码目录，并跳过上面第 2 步（设置 Git Bash 为默认 shell）。

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
  也可以和命令写在同一行：`#local ls ~/work/pc-code`。钩子会去掉这个前缀再执行；不然 bash 会把整行当成注释，什么都不执行。
- 也可以手动调用：`~/.local/share/claude-pc-bridge/bin/pcrun 'git status'`。手动调用不带会话 ID，不保存状态。
- 设置 `PC_BRIDGE_DRY_RUN=1` 时，`pcrun` 只打印要在电脑上执行的命令，不连接电脑，方便排查路径替换。
- 会话状态出问题（比如恢复出错误的环境变量）时，删掉电脑上的 `~/.cache/claude-pc-bridge/sessions/<会话ID>.sh` 即可重新开始。
- 复用的 ssh 连接在隧道断开后会失效，通常 ssh 会自动重建；如果命令一直报连接错误，删掉 VPS 上 `~/.cache/claude-pc-bridge/` 里的 `cm-*` 文件。

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

不需要连接电脑，只依赖 bash、perl 和 jq。覆盖钩子的转发判断、命令和输出里的路径换算，以及会话状态的保存和恢复（用本机 bash 模拟电脑执行）。

## 限制

- 不保留后台作业（`cmd &` 之后的 `jobs`、`fg`）和没有 `export` 的普通 shell 变量；需要交互输入的命令不能用。
- 同一会话里并行执行的命令共用一份状态，最后结束的那条命令保存的状态生效。
- 判断相对路径时，按 Claude 的工作目录解析。电脑上 `cd` 到挂载目录外之后，命令里的相对路径是相对电脑上的目录，不会被换算。
- 输出里的路径换算只认 `PC_ROOT` 的几种写法，路径中间有空格时只换到空格前。
- 白名单只看命令里直接写出来的程序名，通过别名、函数或者 `bash -c '...'` 间接调用的认不出来。
- 读、改文件走 sshfs，大范围扫描（如对整个挂载目录 `grep -r`）很慢，尽量在项目目录里、经转发在电脑上执行。
