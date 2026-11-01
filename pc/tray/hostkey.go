package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ConfirmFunc 第一次连接某台主机时，询问用户是否信任这个指纹
type ConfirmFunc func(host, fingerprint string) bool

// hostKeyError 是指纹没有确认或者对不上。遇到它要暂停，不能自动重试：
// 重试也不会变好，托盘模式下还会反复弹确认窗口
type hostKeyError struct{ msg string }

func (e *hostKeyError) Error() string { return e.msg }

var errHostKeyRejected = &hostKeyError{"没有确认 VPS 的主机指纹，已暂停。点「恢复」重新连接并确认"}

// knownHostsMu 防止同时写 known_hosts
var knownHostsMu sync.Mutex

// hostKeyCallback 用程序自己的 known_hosts 校验主机指纹
func hostKeyCallback(path string, confirm ConfirmFunc) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		knownHostsMu.Lock()
		defer knownHostsMu.Unlock()
		check, err := knownhosts.New(path)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", path, err)
		}
		err = check(hostname, remote, key)
		var keyErr *knownhosts.KeyError
		if err == nil || !errors.As(err, &keyErr) {
			return err
		}
		fp := ssh.FingerprintSHA256(key)
		if len(keyErr.Want) > 0 {
			return &hostKeyError{fmt.Sprintf("VPS 的主机指纹和保存的不一致（现在是 %s），可能被冒充，已暂停。确认 VPS 重装过的话，删除 %s 里对应的行后点「恢复」", fp, path)}
		}
		if confirm == nil || !confirm(hostname, fp) {
			return errHostKeyRejected
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
		return err
	}
}
