# 计划：用户主机上的托盘程序

## 背景

现在电脑端要保持到 VPS 的反向隧道，有两种方式：手动开一个 `ssh -N vps-tunnel` 窗口，或者用 `pc/windows/tunnel.ps1` 加计划任务在后台跑。这两种方式有以下问题：

- 看不到隧道状态，要去翻日志或运行 `install-tunnel-task.ps1 -Action status`。
- 想临时断开、马上重连，只能去任务计划程序里操作。
- 断线了没有提醒，要等 Claude 的命令失败才发现。
- 依赖 Git for Windows 自带的 `connect.exe` 走代理。它是 MSYS 程序，计划任务里要手动把它加进 PATH；ssh 退出后它还占着管道，曾经让重连白等约一分钟。

目标：做一个常驻系统托盘的小程序，替代 `tunnel.ps1` 和计划任务，并且**不依赖 `ssh.exe` 和 `connect.exe`**。

## 需求

### 必须有

1. **程序自己建立隧道**：用 Go 的 ssh 库（`golang.org/x/crypto/ssh`）直接连 VPS，然后请求 VPS 监听 `localhost:2222`。VPS 上每来一个连接，程序就连到本机 `localhost:22`，双向转发数据。效果和 `ssh -N -R 2222:localhost:22` 一样，但不调用外部的 ssh 程序。
2. **内置代理支持**：程序自己通过 SOCKS5 或 HTTP CONNECT 代理连 VPS（库：`golang.org/x/net/proxy`），替代 `connect.exe`。代理是可选的，不填就直接连。
3. **自动重连**：连接断开或连不上时自动重试。重试间隔和 `tunnel.ps1` 一样：从 5 秒开始逐次翻倍，最长 60 秒；连接持续超过 60 秒后才断开的，间隔重新从 5 秒算。
4. **及时发现断线**：每 15 秒向 VPS 发一次心跳（OpenSSH 的 `keepalive@openssh.com` 请求），连续 3 次没回应就断开重连。效果等同于现在的 `ServerAliveInterval=15`、`ServerAliveCountMax=3`。
5. **主机指纹校验**：程序在自己的配置目录里保存 VPS 的主机指纹（`known_hosts` 文件），不读也不写 `~/.ssh/known_hosts`。指纹不一致就拒绝连接，并在状态里提示。第一次连接时弹窗显示指纹，由你确认后保存。
6. **托盘图标显示状态**，用三种颜色区分：
   - 绿：已连上，VPS 已开始监听 2222 端口。
   - 黄：正在连接，或者在等待重连。
   - 红：已暂停，或者连续失败。

   鼠标悬停时显示详情，例如"已连接 2 小时 13 分"或"重连失败：VPS 上 2222 端口已被占用，30 秒后重试"。
7. **托盘菜单**：
   - 状态行（不可点击）
   - 立即重连
   - 暂停 / 恢复（暂停时断开隧道，并且不再自动重连）
   - 打开日志
   - 编辑配置（用记事本打开 `tray.json`，保存后自动生效）
   - 复制公钥（把程序专用密钥的公钥复制到剪贴板，用来加到 VPS 上）
   - 开机登录时自动启动（勾选项）
   - 退出
8. **只运行一个实例**：重复启动时直接退出。
9. **不弹黑窗口**：编译成 Windows GUI 程序。
10. **日志**：写到配置目录里的 `tunnel.log`，超过 1MB 时轮换。

### 应该有

11. **断线通知**：连上后又断开、并且 1 分钟内没重连上时，弹一次系统通知；恢复后再提示一次。不要每次重试都弹。
12. **Linux 用户主机也能用**：同一份代码在 Linux 上编译运行。自动启动改用 `~/.config/autostart/*.desktop`。

### 以后再说

- 显示 VPS 端挂载状态：复用已有的 ssh 连接，在 VPS 上运行 `mountpoint ~/work/pc-code`。
- 图形化的设置界面。第一版通过菜单里的「编辑配置」直接改配置文件。
- 管理多条隧道。

## 配置

程序自己管理全部配置，**不读取也不修改** `~/.ssh` 下的任何文件（`config`、`known_hosts`、密钥）。所有文件放在程序自己的配置目录：Windows 上是 `%APPDATA%\claude-pc-bridge\`，Linux 上是 `~/.config/claude-pc-bridge/`。

| 文件 | 内容 |
|---|---|
| `tray.json` | 连接配置，见下面的示例 |
| `id_ed25519`、`id_ed25519.pub` | 程序专用的密钥，第一次启动时自动生成 |
| `known_hosts` | 已确认过的 VPS 主机指纹 |

第一次启动的流程：
1. 生成程序专用的密钥，并写一份带默认值的 `tray.json`。
2. 用记事本打开 `tray.json`，让你填 VPS 地址、用户名和代理。托盘图标显示红色，提示"请先完成配置"。
3. 你把公钥加到 VPS 的 `~/.ssh/authorized_keys`。点托盘菜单里的「复制公钥」就能复制到剪贴板。
4. 保存配置后，程序自动重新读取并开始连接。

之后改配置也一样：点「编辑配置」，保存后自动生效。

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

`proxy` 留空表示直接连接，也可以写成 `http://127.0.0.1:7890`。

## 技术选型

用 **Go**。依赖：
- `golang.org/x/crypto/ssh`：ssh 连接、远程端口转发。
- `golang.org/x/crypto/ssh/knownhosts`：主机指纹校验。
- `golang.org/x/net/proxy`：SOCKS5 代理。HTTP CONNECT 代理自己写，大约 30 行。
- `fyne.io/systray`：托盘图标和菜单。
- `github.com/gen2brain/beeep`：系统通知。
- `github.com/atotto/clipboard`：「复制公钥」用。
配置文件是否被保存过，每 2 秒检查一次修改时间，不额外引入文件监视库。

理由：
- 编译出来是单个 exe，不需要另装运行时；也不需要 `ssh.exe`、`connect.exe`，拷到哪台电脑都能用。
- ssh 连接在程序内部，是否连上、为什么失败都能直接拿到，不用解析 ssh 的输出，也不会有子进程残留。
- 同一份代码支持 Windows 和 Linux。
- 电脑上已经装了 Go 1.25。

没选的方案：
- 继续调用 `ssh.exe`，自己实现一个替代 `connect.exe` 的代理命令：还是要管子进程、解析 ssh 输出，问题只解决了一半。
- Python + pystray：要带 Python 运行时，打包后体积大、启动慢。
- C# WinForms：只支持 Windows。

## 目录结构

```
pc/tray/
  go.mod
  main.go               启动参数、单实例、命令行模式（-console）
  tray.go               托盘菜单、图标切换、断线通知、配置文件变化检查
  tunnel.go             ssh 连接、远程端口转发、心跳、重连
  dial.go               直接连接、SOCKS5 代理、HTTP CONNECT 代理
  hostkey.go            主机指纹校验、第一次连接时确认
  config.go             读写 tray.json、生成密钥
  log.go                日志写入和轮换
  icon.go               运行时画出绿、黄、红、灰四个圆点图标，不需要图片文件
  platform_windows.go   弹窗、命名互斥锁、注册表 Run 项
  platform_unix.go      zenity 弹窗、文件锁、~/.config/autostart 文件
  build.sh              编译 Windows 和 Linux 版本到 dist/
```

自动启动用注册表 Run 项，不再用计划任务：托盘程序要在用户登录后的桌面上运行，Run 项最简单，也不需要管理员权限。

## 实施步骤

1. **隧道核心**（`tunnel.go`、`dial.go`、`hostkey.go`、`config.go`、`log.go`）：先做成命令行版本，把状态变化打印出来。在你电脑上验证：
   - 通过 SOCKS5 代理连上 VPS，VPS 上 `ssh -p 2222 localhost` 能登录到电脑。
   - VPS 的 2222 端口被占用时，显示失败原因，并按 5、10、20 秒的间隔重试。
   - 断网再恢复（或者在 VPS 上结束这条 sshd 连接）后，约 45 秒内发现断线并重连。
   - 不设代理时也能直接连。
2. **托盘界面**（`main.go`、图标）：菜单、图标颜色、悬停提示、暂停 / 恢复、立即重连、打开日志。
3. **自动启动和单实例**。
4. **通知**。
5. **Linux 版本**：编译通过；如果手头有 Linux 桌面，就实际测一次，否则在 README 里写明没有实测。
6. **切换和清理**：
   - 在你电脑上运行托盘程序接管隧道，然后关掉手动开的隧道窗口。
   - 删除 `pc/windows/tunnel.ps1`、`install-tunnel-task.ps1`（还没提交过，可以直接删）。`pc/linux/pc-tunnel.service` 保留，给没有桌面的 Linux 主机用。
   - 更新 README：电脑端第 4、5 步改成"运行托盘程序并勾选自动启动"；删除 `pc/ssh_config.example`，改成说明怎么把程序的公钥加到 VPS；目录表同步更新。

## 切换时的注意事项

- 测试和切换期间，托盘程序会和手动开的隧道抢 VPS 的 2222 端口。第 1 步的测试可以先用另一个端口（比如 2223），不影响现在的隧道；正式切换时先关手动窗口，再启动托盘程序，中间约几秒到 45 秒 Claude 的命令没法转发到电脑上执行。
- 程序用的是自己生成的新密钥，测试前要先把它的公钥加到 VPS 的 `~/.ssh/authorized_keys`。这一步在 VPS 上做，我可以直接加，不影响现有密钥。现有隧道密钥等切换稳定后，由你决定是否从 VPS 上删掉。
- 第 6 步需要在你电脑上实际操作，我会先问你再做。

## 需要你确认

还没提交的 `tunnel.ps1`、`install-tunnel-task.ps1` 直接删掉，不进入 git 历史，可以吗？
