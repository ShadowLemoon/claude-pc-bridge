package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/atotto/clipboard"
	"github.com/gen2brain/beeep"
)

// notifyAfter 断开超过这么久还没重连上，才弹通知
const notifyAfter = time.Minute

type trayApp struct {
	paths   Paths
	dirFlag string
	logger  *log.Logger
	created bool
	tunnel  *Tunnel
	cancel  context.CancelFunc

	icons map[State][]byte

	mStatus, mReconnect, mPause, mAutostart *systray.MenuItem

	mu          sync.Mutex
	lastState   State
	downTimer   *time.Timer
	notifiedOff bool // 已经弹过断线通知，恢复时要再提示一次
}

func runTray(paths Paths, dirFlag string, logger *log.Logger, created bool) {
	app := &trayApp{
		paths: paths, dirFlag: dirFlag, logger: logger, created: created,
		lastState: -1,
		icons: map[State][]byte{
			StateNotConfigured: makeIcon(colorRed),
			StatePaused:        makeIcon(colorGray),
			StateConnecting:    makeIcon(colorYellow),
			StateConnected:     makeIcon(colorGreen),
			StateWaiting:       makeIcon(colorRed),
		},
	}
	systray.Run(app.onReady, app.onExit)
}

func (a *trayApp) onReady() {
	systray.SetIcon(a.icons[StateConnecting])
	systray.SetTooltip("claude-pc-bridge")

	a.mStatus = systray.AddMenuItem("启动中…", "")
	a.mStatus.Disable()
	systray.AddSeparator()
	a.mReconnect = systray.AddMenuItem("立即重连", "断开当前连接并马上重连")
	a.mPause = systray.AddMenuItem("暂停", "断开隧道，不再自动重连")
	systray.AddSeparator()
	mEdit := systray.AddMenuItem("编辑配置", "用记事本打开 tray.json，保存后自动生效")
	mCopyKey := systray.AddMenuItem("复制公钥", "复制本程序的公钥，加到 VPS 的 ~/.ssh/authorized_keys")
	mLog := systray.AddMenuItem("打开日志", "")
	mDir := systray.AddMenuItem("打开配置目录", "")
	systray.AddSeparator()
	a.mAutostart = systray.AddMenuItemCheckbox("登录时自动启动", "", autostartEnabled(a.dirFlag))
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "断开隧道并退出")

	a.tunnel = NewTunnel(a.paths, a.confirmHostKey, a.logger, a.onStatus)
	a.tunnel.Reload()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go a.tunnel.Run(ctx)
	go watchConfig(ctx, a.paths.Config, a.tunnel.Reload)
	go a.refreshLoop(ctx)

	if a.created {
		a.openConfig()
	}

	go func() {
		for {
			select {
			case <-a.mReconnect.ClickedCh:
				a.tunnel.Reconnect()
			case <-a.mPause.ClickedCh:
				paused := a.tunnel.Status().State != StatePaused
				a.tunnel.SetPaused(paused)
			case <-mEdit.ClickedCh:
				a.openConfig()
			case <-mCopyKey.ClickedCh:
				a.copyPublicKey()
			case <-mLog.ClickedCh:
				a.report(openInEditor(a.paths.Log))
			case <-mDir.ClickedCh:
				a.report(openPath(a.paths.Dir))
			case <-a.mAutostart.ClickedCh:
				a.toggleAutostart()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func (a *trayApp) onExit() {
	if a.cancel != nil {
		a.cancel()
	}
	a.logger.Print("退出")
}

func (a *trayApp) report(err error) {
	if err != nil {
		a.logger.Print(err)
		showError(err.Error())
	}
}

func (a *trayApp) openConfig() {
	a.report(openInEditor(a.paths.Config))
}

func (a *trayApp) copyPublicKey() {
	pub, err := os.ReadFile(a.paths.PubKey)
	if err == nil {
		err = clipboard.WriteAll(strings.TrimSpace(string(pub)))
	}
	if err != nil {
		a.report(fmt.Errorf("复制公钥失败: %w", err))
		return
	}
	notify("已复制公钥", "加到 VPS 的 ~/.ssh/authorized_keys 末尾")
}

func (a *trayApp) toggleAutostart() {
	enable := !a.mAutostart.Checked()
	if err := setAutostart(enable, a.dirFlag); err != nil {
		a.report(fmt.Errorf("设置自动启动失败: %w", err))
		return
	}
	if enable {
		a.mAutostart.Check()
	} else {
		a.mAutostart.Uncheck()
	}
}

func (a *trayApp) confirmHostKey(host, fp string) bool {
	ok := confirmDialog(fmt.Sprintf("第一次连接 %s\n\n主机指纹：%s\n\n确认这是你的 VPS 吗？可以在 VPS 上运行下面的命令核对：\nfor f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf $f; done", host, fp))
	a.logger.Printf("主机 %s 指纹 %s，用户选择%s", host, fp, map[bool]string{true: "信任", false: "不信任"}[ok])
	return ok
}

// onStatus 隧道状态变化时调用
func (a *trayApp) onStatus(s Status) {
	a.updateView(s)

	a.mu.Lock()
	defer a.mu.Unlock()
	prev := a.lastState
	a.lastState = s.State
	if prev == s.State {
		return
	}
	switch s.State {
	case StateConnected:
		if a.downTimer != nil {
			a.downTimer.Stop()
			a.downTimer = nil
		}
		if a.notifiedOff {
			a.notifiedOff = false
			notify("隧道已恢复", "")
		}
	case StateConnecting, StateWaiting:
		// 只在从“已连接”断开时开始计时；之后的重试不重复计时
		if prev == StateConnected && a.downTimer == nil {
			a.downTimer = time.AfterFunc(notifyAfter, a.notifyDown)
		}
	case StatePaused, StateNotConfigured:
		if a.downTimer != nil {
			a.downTimer.Stop()
			a.downTimer = nil
		}
		a.notifiedOff = false
	}
}

func (a *trayApp) notifyDown() {
	s := a.tunnel.Status()
	a.mu.Lock()
	a.downTimer = nil
	if s.State == StateConnected || s.State == StatePaused {
		a.mu.Unlock()
		return
	}
	a.notifiedOff = true
	a.mu.Unlock()
	notify("隧道已断开", s.Err)
}

// refreshLoop 定时刷新提示里的“已连接多久”“几秒后重试”
func (a *trayApp) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.updateView(a.tunnel.Status())
		}
	}
}

func (a *trayApp) updateView(s Status) {
	icon := a.icons[s.State]
	if s.State == StatePaused && s.Err != "" {
		// 因为主机指纹问题自动暂停，显示红色提醒处理
		icon = a.icons[StateWaiting]
	}
	systray.SetIcon(icon)
	title := statusText(s)
	a.mStatus.SetTitle(title)
	tip := "claude-pc-bridge\n" + title
	if s.Err != "" && s.State != StateConnected {
		tip += "\n" + s.Err
	}
	// Windows 托盘提示最多 127 个字符
	if r := []rune(tip); len(r) > 120 {
		tip = string(r[:120]) + "…"
	}
	systray.SetTooltip(tip)
	if s.State == StatePaused {
		a.mPause.SetTitle("恢复")
	} else {
		a.mPause.SetTitle("暂停")
	}
	if s.State == StatePaused || s.State == StateNotConfigured {
		a.mReconnect.Disable()
	} else {
		a.mReconnect.Enable()
	}
}

func statusText(s Status) string {
	switch s.State {
	case StateNotConfigured:
		return "请先完成配置"
	case StatePaused:
		return "已暂停"
	case StateConnecting:
		return "正在连接…"
	case StateConnected:
		return "已连接 " + formatDuration(time.Since(s.Since))
	case StateWaiting:
		left := time.Until(s.RetryAt).Round(time.Second)
		if left < 0 {
			left = 0
		}
		return fmt.Sprintf("连接失败，%d 秒后重试", int(left.Seconds()))
	}
	return ""
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "不到 1 分钟"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d 小时 %d 分", int(d.Hours()), int(d.Minutes())%60)
	}
}

func notify(title, msg string) {
	_ = beeep.Notify(title, msg, "")
}

// watchConfig 每 2 秒检查一次配置文件，被保存过就重新读取
func watchConfig(ctx context.Context, path string, reload func()) {
	modTime := func() time.Time {
		st, err := os.Stat(path)
		if err != nil {
			return time.Time{}
		}
		return st.ModTime()
	}
	last := modTime()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m := modTime(); !m.Equal(last) {
				last = m
				reload()
			}
		}
	}
}
