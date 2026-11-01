package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	mbYesNo         = 0x4
	mbIconError     = 0x10
	mbIconQuestion  = 0x20
	mbIconInfo      = 0x40
	mbTopmost       = 0x40000
	mbSetForeground = 0x10000
	idYes           = 6
)

var procMessageBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

func messageBox(text string, flags uintptr) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("claude-pc-bridge")
	r, _, _ := procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)),
		flags|mbTopmost|mbSetForeground)
	return int(r)
}

func showError(msg string) {
	messageBox(msg, mbIconError)
}

func showInfo(msg string) {
	messageBox(msg, mbIconInfo)
}

func confirmDialog(msg string) bool {
	return messageBox(msg, mbYesNo|mbIconQuestion) == idYes
}

// singleInstance 同一个配置目录只允许运行一个实例。返回的函数在退出时释放
func singleInstance(dir string) (release func(), err error) {
	h := fnv.New64a()
	h.Write([]byte(dir))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Local\claude-pc-bridge-tray-%x`, h.Sum64()))
	m, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(m)
		return nil, errAlreadyRunning
	}
	if err != nil {
		return nil, err
	}
	return func() { windows.CloseHandle(m) }, nil
}

func openInEditor(path string) error {
	return startDetached("notepad.exe", path)
}

func openPath(path string) error {
	return startDetached("explorer.exe", path)
}

func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "claude-pc-bridge-tray"

// autostartCommand 是写入注册表的启动命令
func autostartCommand(dirFlag string) (string, error) {
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
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValue)
	want, _ := autostartCommand(dirFlag)
	return err == nil && v == want
}

func setAutostart(enabled bool, dirFlag string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enabled {
		err := k.DeleteValue(runValue)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	cmd, err := autostartCommand(dirFlag)
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, cmd)
}
