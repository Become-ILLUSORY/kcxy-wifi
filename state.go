package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pausedFile 是下线状态标记。存在即表示用户主动下线，
// 常驻进程检测到后不再自动认证，直到执行 resume。
//
// 系统级安装时统一放 /var/lib/kcxy-wifi，保证 systemd 服务和手动执行
// 看到的是同一个标记文件；普通用户权限不足时退回用户状态目录。
func pausedFile(cfg Config) string {
	if cfg.StateFile != "" {
		return cfg.StateFile
	}
	const systemPath = "/var/lib/kcxy-wifi/paused"
	if canWriteDir(filepath.Dir(systemPath)) {
		return systemPath
	}
	if dir, err := userStateDir(); err == nil {
		return filepath.Join(dir, "kcxy-wifi", "paused")
	}
	return "kcxy-wifi.paused"
}

func userStateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

func canWriteDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func isPaused(cfg Config) bool {
	_, err := os.Stat(pausedFile(cfg))
	return err == nil
}

func setPaused(cfg Config, reason string) error {
	path := pausedFile(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	content := time.Now().Format(time.RFC3339) + "\n" + strings.TrimSpace(reason) + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func clearPaused(cfg Config) error {
	err := os.Remove(pausedFile(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// pausedSince 读取下线时间，用于日志展示。
func pausedSince(cfg Config) string {
	data, err := os.ReadFile(pausedFile(cfg))
	if err != nil {
		return ""
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	return strings.TrimSpace(lines[0])
}
