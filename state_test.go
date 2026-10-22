package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPausedLifecycle(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.StateFile = filepath.Join(dir, "state", "kcxy-wifi.paused")

	if isPaused(cfg) {
		t.Fatal("初始不应处于暂停状态")
	}
	if err := setPaused(cfg, "测试下线"); err != nil {
		t.Fatalf("写入暂停状态失败: %v", err)
	}
	if !isPaused(cfg) {
		t.Fatal("写入后应处于暂停状态")
	}
	if pausedSince(cfg) == "" {
		t.Fatal("应记录下线时间")
	}
	if err := clearPaused(cfg); err != nil {
		t.Fatalf("清除暂停状态失败: %v", err)
	}
	if isPaused(cfg) {
		t.Fatal("清除后不应处于暂停状态")
	}
}

func TestClearPausedWhenMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateFile = filepath.Join(t.TempDir(), "missing.paused")
	if err := clearPaused(cfg); err != nil {
		t.Fatalf("文件不存在时应视为成功: %v", err)
	}
}

func TestPausedFileDefault(t *testing.T) {
	cfg := DefaultConfig()
	// 默认路径是系统级 /var/lib（可写时），否则退回用户状态目录；
	// 两种情况都不应该落到相对路径上
	got := pausedFile(cfg)
	if got == "" || got == "kcxy-wifi.paused" {
		t.Fatalf("默认状态文件应落在绝对路径: %s", got)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("状态文件路径不是绝对路径: %s", got)
	}
	cfg.StateFile = "/var/lib/kcxy-wifi/state"
	if got := pausedFile(cfg); got != "/var/lib/kcxy-wifi/state" {
		t.Fatalf("自定义状态文件路径未生效: %s", got)
	}
}

func TestStateFileFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.ini")
	content := "[network]\nstate_file = /var/lib/kcxy-wifi/state\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateFile != "/var/lib/kcxy-wifi/state" {
		t.Fatalf("state_file 未解析: %s", cfg.StateFile)
	}
}
