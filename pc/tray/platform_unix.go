//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// 有 zenity 时用它弹窗，没有就只打印到 stderr
func zenity(args ...string) (ok bool, ran bool) {
	if _, err := exec.LookPath("zenity"); err != nil {
		return false, false
	}
	return exec.Command("zenity", append(args, "--title=claude-pc-bridge")...).Run() == nil, true
}

func showError(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	zenity("--error", "--text="+msg)
}

func showInfo(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	zenity("--info", "--text="+msg)
}

func confirmDialog(msg string) bool {
	ok, ran := zenity("--question", "--text="+msg)
	if !ran {
		fmt.Fprintln(os.Stderr, "没有 zenity，无法弹窗确认；请用 -console 模式运行一次来确认主机指纹")
	}
	return ok
}

// singleInstance 用配置目录里的锁文件保证只运行一个实例
func singleInstance(dir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(dir, "tray.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errAlreadyRunning
	}
	return func() { f.Close() }, nil
}

func openInEditor(path string) error { return openPath(path) }

func openPath(path string) error {
	cmd := exec.Command("xdg-open", path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func autostartFile() string {
	base, _ := os.UserConfigDir()
	return filepath.Join(base, "autostart", "claude-pc-bridge-tray.desktop")
}

func autostartExec(dirFlag string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := `"` + exe + `"`
	if dirFlag != "" {
		cmd += ` -dir "` + dirFlag + `"`
	}
	return cmd, nil
}

func autostartEnabled(dirFlag string) bool {
	data, err := os.ReadFile(autostartFile())
	want, _ := autostartExec(dirFlag)
	return err == nil && strings.Contains(string(data), "Exec="+want+"\n")
}

func setAutostart(enabled bool, dirFlag string) error {
	path := autostartFile()
	if !enabled {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cmd, err := autostartExec(dirFlag)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "[Desktop Entry]\nType=Application\nName=claude-pc-bridge tray\nExec=" + cmd + "\nX-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(path, []byte(content), 0o644)
}
