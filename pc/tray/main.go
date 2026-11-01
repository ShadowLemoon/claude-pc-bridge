// claude-pc-bridge-tray：在用户主机上保持到 VPS 的反向隧道，常驻系统托盘。
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
)

func main() {
	dirFlag := flag.String("dir", "", "配置目录（默认 Windows 上是 %APPDATA%\\claude-pc-bridge，Linux 上是 ~/.config/claude-pc-bridge）")
	console := flag.Bool("console", false, "不显示托盘，在命令行里运行，用于调试")
	flag.Parse()
	consoleMode = *console

	dir := *dirFlag
	if dir == "" {
		var err error
		if dir, err = defaultDir(); err != nil {
			fatal(err)
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	paths := newPaths(dir)
	created, err := ensureFiles(paths)
	if err != nil {
		fatal(err)
	}

	release, err := singleInstance(paths.Dir)
	if errors.Is(err, errAlreadyRunning) {
		if *console {
			fatal(err)
		}
		showInfo("claude-pc-bridge 已经在运行，见系统托盘。")
		return
	}
	if err != nil {
		fatal(err)
	}
	defer release()

	logFile, err := openLog(paths.Log)
	if err != nil {
		fatal(err)
	}
	var out io.Writer = logFile
	if *console {
		out = io.MultiWriter(logFile, os.Stderr)
	}
	logger := log.New(out, "", log.LstdFlags)

	if *console {
		runConsole(paths, logger, created)
	} else {
		runTray(paths, *dirFlag, logger, created)
	}
}

var errAlreadyRunning = errors.New("已经有一个实例在运行")

func runConsole(paths Paths, logger *log.Logger, created bool) {
	if created {
		fmt.Fprintf(os.Stderr, "已生成配置 %s，请填写后重新运行。\n公钥（加到 VPS 的 ~/.ssh/authorized_keys）:\n", paths.Config)
		pub, _ := os.ReadFile(paths.PubKey)
		os.Stderr.Write(pub)
		return
	}
	stdin := bufio.NewReader(os.Stdin)
	confirm := func(host, fp string) bool {
		fmt.Fprintf(os.Stderr, "第一次连接 %s，主机指纹 %s\n信任这台主机吗？[y/N] ", host, fp)
		line, _ := stdin.ReadString('\n')
		return strings.EqualFold(strings.TrimSpace(line), "y")
	}
	t := NewTunnel(paths, confirm, logger, nil)
	t.Reload()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go watchConfig(ctx, paths.Config, t.Reload)
	t.Run(ctx)
	logger.Print("退出")
}

// consoleMode 为 true 时出错只打印到命令行，不弹窗
var consoleMode bool

func fatal(err error) {
	if consoleMode {
		fmt.Fprintln(os.Stderr, err)
	} else {
		showError(err.Error())
	}
	os.Exit(1)
}
