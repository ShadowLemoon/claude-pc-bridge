package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Config 是 tray.json 的内容
type Config struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	User               string `json:"user"`
	Proxy              string `json:"proxy"`
	RemoteListen       string `json:"remote_listen"`
	LocalTarget        string `json:"local_target"`
	KeepaliveSeconds   int    `json:"keepalive_seconds"`
	KeepaliveMaxMissed int    `json:"keepalive_max_missed"`
	MaxRetrySeconds    int    `json:"max_retry_seconds"`
}

var errNotConfigured = errors.New("请先完成配置：填写 host 和 user")

func defaultConfig() Config {
	return Config{
		Port:               22,
		RemoteListen:       "localhost:2222",
		LocalTarget:        "localhost:22",
		KeepaliveSeconds:   15,
		KeepaliveMaxMissed: 3,
		MaxRetrySeconds:    60,
	}
}

// Paths 是程序自己管理的所有文件，都在同一个目录下
type Paths struct {
	Dir        string
	Config     string
	Key        string
	PubKey     string
	KnownHosts string
	Log        string
}

func newPaths(dir string) Paths {
	return Paths{
		Dir:        dir,
		Config:     filepath.Join(dir, "tray.json"),
		Key:        filepath.Join(dir, "id_ed25519"),
		PubKey:     filepath.Join(dir, "id_ed25519.pub"),
		KnownHosts: filepath.Join(dir, "known_hosts"),
		Log:        filepath.Join(dir, "tunnel.log"),
	}
}

// defaultDir: Windows 上是 %APPDATA%\claude-pc-bridge，Linux 上是 ~/.config/claude-pc-bridge
func defaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "claude-pc-bridge"), nil
}

// ensureFiles 第一次启动时创建目录、默认配置、专用密钥和空的 known_hosts。
// 返回 created=true 表示配置文件是新建的，需要用户填写
func ensureFiles(p Paths) (created bool, err error) {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return false, err
	}
	if _, err := os.Stat(p.Config); errors.Is(err, os.ErrNotExist) {
		data, _ := json.MarshalIndent(defaultConfig(), "", "  ")
		if err := os.WriteFile(p.Config, append(data, '\n'), 0o600); err != nil {
			return false, err
		}
		created = true
	}
	if _, err := os.Stat(p.Key); errors.Is(err, os.ErrNotExist) {
		if err := generateKey(p); err != nil {
			return created, fmt.Errorf("生成密钥失败: %w", err)
		}
	}
	if _, err := os.Stat(p.KnownHosts); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(p.KnownHosts, nil, 0o600); err != nil {
			return created, err
		}
	}
	return created, nil
}

func generateKey(p Paths) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	comment := "claude-pc-bridge-tray@" + hostname
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p.Key, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment + "\n"
	return os.WriteFile(p.PubKey, []byte(line), 0o644)
}

// loadConfig 读取配置，缺少的字段用默认值补上
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	// 记事本保存时可能带 UTF-8 BOM
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("配置文件格式错误: %w", err)
	}
	if cfg.Host == "" || cfg.User == "" {
		return cfg, errNotConfigured
	}
	return cfg, nil
}

func loadSigner(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}
