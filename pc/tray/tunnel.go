package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type State int

const (
	StateNotConfigured State = iota // 配置没填好
	StatePaused                     // 用户暂停
	StateConnecting                 // 正在连接
	StateConnected                  // 已连上，VPS 已开始监听
	StateWaiting                    // 连接失败或断开，等待重连
)

// Status 是隧道当前状态，用来更新托盘图标和提示
type Status struct {
	State   State
	Since   time.Time // 进入当前状态的时间
	Err     string    // 最近一次失败原因
	RetryAt time.Time // StateWaiting 时下次重连的时间
}

const initialRetryDelay = 5 * time.Second

// stableUptime 连接持续超过这么久才断开的，重试间隔从头算
const stableUptime = 60 * time.Second

// Tunnel 保持一条反向隧道：VPS 上的 RemoteListen → 本机的 LocalTarget
type Tunnel struct {
	paths    Paths
	confirm  ConfirmFunc
	logger   *log.Logger
	onChange func(Status)

	mu     sync.Mutex
	cfg    Config
	cfgErr error
	paused bool
	// pauseErr 是自动暂停的原因（主机指纹问题），用户手动暂停时为空
	pauseErr string
	status   Status
	cancel   context.CancelFunc // 结束当前这次连接
	kicked   bool               // 当前连接是被用户操作打断的，不算失败
	wakeCh   chan struct{}
}

func NewTunnel(paths Paths, confirm ConfirmFunc, logger *log.Logger, onChange func(Status)) *Tunnel {
	return &Tunnel{
		paths:    paths,
		confirm:  confirm,
		logger:   logger,
		onChange: onChange,
		wakeCh:   make(chan struct{}, 1),
	}
}

func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *Tunnel) Config() Config {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cfg
}

// Reload 重新读取配置，并用新配置重连
func (t *Tunnel) Reload() {
	cfg, err := loadConfig(t.paths.Config)
	t.mu.Lock()
	t.cfg, t.cfgErr = cfg, err
	t.mu.Unlock()
	if err != nil {
		t.logger.Printf("读取配置: %v", err)
	} else {
		t.logger.Printf("已读取配置: %s@%s:%d，代理 %q，VPS 监听 %s → 本机 %s",
			cfg.User, cfg.Host, cfg.Port, cfg.Proxy, cfg.RemoteListen, cfg.LocalTarget)
	}
	t.interrupt()
}

// Reconnect 断开当前连接并立即重连
func (t *Tunnel) Reconnect() {
	t.logger.Print("手动重连")
	t.interrupt()
}

func (t *Tunnel) SetPaused(paused bool) {
	t.mu.Lock()
	t.paused, t.pauseErr = paused, ""
	t.mu.Unlock()
	if paused {
		t.logger.Print("暂停")
	} else {
		t.logger.Print("恢复")
	}
	t.interrupt()
}

func (t *Tunnel) interrupt() {
	t.mu.Lock()
	if t.cancel != nil {
		t.kicked = true
		t.cancel()
	}
	t.mu.Unlock()
	select {
	case t.wakeCh <- struct{}{}:
	default:
	}
}

func (t *Tunnel) setStatus(s Status) {
	t.mu.Lock()
	if s.State != t.status.State || s.Since.IsZero() {
		s.Since = time.Now()
	} else {
		s.Since = t.status.Since
	}
	t.status = s
	t.mu.Unlock()
	if t.onChange != nil {
		t.onChange(s)
	}
}

// Run 一直运行到 ctx 结束
func (t *Tunnel) Run(ctx context.Context) {
	delay := initialRetryDelay
	for ctx.Err() == nil {
		t.mu.Lock()
		cfg, cfgErr, paused, pauseErr := t.cfg, t.cfgErr, t.paused, t.pauseErr
		t.mu.Unlock()

		if cfgErr != nil || paused {
			if paused {
				t.setStatus(Status{State: StatePaused, Err: pauseErr})
			} else {
				t.setStatus(Status{State: StateNotConfigured, Err: cfgErr.Error()})
			}
			select {
			case <-t.wakeCh:
			case <-ctx.Done():
			}
			delay = initialRetryDelay
			continue
		}

		connCtx, cancel := context.WithCancel(ctx)
		t.mu.Lock()
		t.cancel, t.kicked = cancel, false
		t.mu.Unlock()

		start := time.Now()
		err := t.connectOnce(connCtx, cfg)
		cancel()
		uptime := time.Since(start)

		t.mu.Lock()
		t.cancel = nil
		kicked := t.kicked
		t.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		// 清掉打断前积攒的唤醒信号
		select {
		case <-t.wakeCh:
		default:
		}
		if kicked {
			delay = initialRetryDelay
			continue
		}
		var hkErr *hostKeyError
		if errors.As(err, &hkErr) {
			t.logger.Print(hkErr)
			t.mu.Lock()
			t.paused, t.pauseErr = true, hkErr.Error()
			t.mu.Unlock()
			continue
		}

		if uptime > stableUptime {
			delay = initialRetryDelay
		}
		t.logger.Printf("连接结束（持续 %s）: %v，%s 后重连", uptime.Round(time.Second), err, delay)
		t.setStatus(Status{State: StateWaiting, Err: err.Error(), RetryAt: time.Now().Add(delay)})
		select {
		case <-time.After(delay):
			maxDelay := time.Duration(cfg.MaxRetrySeconds) * time.Second
			delay = min(delay*2, max(maxDelay, initialRetryDelay))
		case <-t.wakeCh:
			delay = initialRetryDelay
		case <-ctx.Done():
			return
		}
	}
}

// connectOnce 建立一次连接，一直阻塞到连接断开，返回断开原因
func (t *Tunnel) connectOnce(ctx context.Context, cfg Config) error {
	t.setStatus(Status{State: StateConnecting})
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	t.logger.Printf("连接 %s", addr)

	signer, err := loadSigner(t.paths.Key)
	if err != nil {
		return fmt.Errorf("读取密钥失败: %w", err)
	}

	conn, err := dial(cfg.Proxy, addr, 15*time.Second)
	if err != nil {
		return err
	}
	// ctx 结束（暂停、重连、退出）时立即断开
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback(t.paths.KnownHosts, t.confirm),
		Timeout:         15 * time.Second,
	}
	// 握手设超时；但第一次连接时要等用户确认指纹，所以给足时间
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		var hkErr *hostKeyError
		if errors.As(err, &hkErr) {
			return hkErr
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return fmt.Errorf("VPS 拒绝登录：请确认已把本程序的公钥（托盘菜单「复制公钥」）加到 %s@%s 的 ~/.ssh/authorized_keys", cfg.User, cfg.Host)
		}
		return fmt.Errorf("ssh 登录失败: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	ln, err := client.Listen("tcp", cfg.RemoteListen)
	if err != nil {
		return fmt.Errorf("VPS 上监听 %s 失败（端口可能被占用）: %w", cfg.RemoteListen, err)
	}
	// 不调用 ln.Close()：它要等 VPS 回复取消监听，VPS 没响应时会一直卡住。
	// 上面 defer 的 client.Close / conn.Close 会直接断开连接，VPS 那边随之释放端口

	t.logger.Printf("已连接，VPS 上 %s → 本机 %s", cfg.RemoteListen, cfg.LocalTarget)
	t.setStatus(Status{State: StateConnected})

	done := make(chan error, 3)
	go func() { done <- client.Wait() }()
	go func() { done <- t.acceptLoop(ln, cfg.LocalTarget) }()
	go func() { done <- keepalive(ctx, client, cfg, t.logger) }()

	select {
	case err := <-done:
		if err == nil || errors.Is(err, io.EOF) {
			err = errors.New("VPS 断开了连接")
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Tunnel) acceptLoop(ln net.Listener, target string) error {
	for {
		remote, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("转发中断: %w", err)
		}
		go t.forward(remote, target)
	}
}

func (t *Tunnel) forward(remote net.Conn, target string) {
	defer remote.Close()
	local, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		t.logger.Printf("连接本机 %s 失败: %v", target, err)
		return
	}
	defer local.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); copyAndCloseWrite(local, remote) }()
	go func() { defer wg.Done(); copyAndCloseWrite(remote, local) }()
	wg.Wait()
}

// copyAndCloseWrite 复制完一个方向后只关闭对端的写，另一个方向继续传
func copyAndCloseWrite(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	} else {
		_ = dst.Close()
	}
}

// keepalive 定时向 VPS 发心跳，连续 KeepaliveMaxMissed 次没回应就返回错误
func keepalive(ctx context.Context, client *ssh.Client, cfg Config, logger *log.Logger) error {
	interval := time.Duration(max(cfg.KeepaliveSeconds, 1)) * time.Second
	maxMissed := max(cfg.KeepaliveMaxMissed, 1)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	missed := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		reply := make(chan error, 1)
		go func() {
			// OpenSSH 不认识这个请求，会回复失败；收到回复（不管成功失败）就说明连接还活着
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		select {
		case err := <-reply:
			if err != nil {
				return fmt.Errorf("心跳失败: %w", err)
			}
			missed = 0
		case <-time.After(interval):
			missed++
			logger.Printf("心跳没有回应（第 %d 次）", missed)
			if missed >= maxMissed {
				return fmt.Errorf("连续 %d 次心跳没有回应", missed)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
