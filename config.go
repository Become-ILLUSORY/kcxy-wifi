package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Account 是一个校园网账号。
type Account struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Remark 仅用于日志区分，可为空
	Remark string `json:"remark,omitempty"`
}

func (a Account) Label() string {
	if a.Remark != "" {
		return a.Remark
	}
	return maskUsername(a.Username)
}

// Config 是运行配置，来源优先级：环境变量 > 配置文件 > 默认值。
type Config struct {
	Accounts []Account

	Iface      string
	PortalHost string
	WlanACName string
	SSID       string
	TestURL    string
	// StateFile 记录下线状态，留空则用 kcxy-wifi.paused
	StateFile string

	CheckInterval int
	RetryInterval int
	MaxRetries    int
	BackoffSecond int
}

func DefaultConfig() Config {
	return Config{
		Iface:         "wlan0",
		PortalHost:    "10.147.103.3",
		TestURL:       "http://www.baidu.com",
		CheckInterval: 30,
		RetryInterval: 5,
		MaxRetries:    6,
		BackoffSecond: 120,
	}
}

// LoadConfig 读取 ini 与账号 JSON，环境变量可覆盖账号。
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	accountsFile := ""

	if path != "" {
		if err := parseINI(path, &cfg, &accountsFile); err != nil && !os.IsNotExist(err) {
			return cfg, err
		}
	}

	if accountsFile != "" {
		accounts, err := LoadAccounts(accountsFile)
		if err != nil {
			return cfg, err
		}
		cfg.Accounts = append(cfg.Accounts, accounts...)
	}

	if v := os.Getenv("KCXY_USERNAME"); v != "" {
		cfg.Accounts = append([]Account{{Username: v, Password: os.Getenv("KCXY_PASSWORD"), Remark: "env"}}, cfg.Accounts...)
	}
	if v := os.Getenv("KCXY_INTERFACE"); v != "" {
		cfg.Iface = v
	}
	if v := os.Getenv("KCXY_PORTAL_HOST"); v != "" {
		cfg.PortalHost = v
	}
	if v := os.Getenv("KCXY_SSID"); v != "" {
		cfg.SSID = v
	}

	cfg.Accounts = dedupeAccounts(cfg.Accounts)
	return cfg, nil
}

// LoadAccounts 支持两种写法：
//
//	[{"username": "...", "password": "..."}, ...]
//	{"accounts": [{"username": "...", "password": "..."}, ...]}
func LoadAccounts(path string) ([]Account, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取账号文件 %s: %w", path, err)
	}

	var list []Account
	if err := json.Unmarshal(data, &list); err == nil {
		return normalizeAccounts(list), nil
	}

	var wrapper struct {
		Accounts []Account `json:"accounts"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("解析账号文件 %s: %w", path, err)
	}
	return normalizeAccounts(wrapper.Accounts), nil
}

func normalizeAccounts(list []Account) []Account {
	out := make([]Account, 0, len(list))
	for _, a := range list {
		a.Username = strings.TrimSpace(a.Username)
		a.Password = strings.TrimSpace(a.Password)
		if a.Username == "" || a.Password == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func dedupeAccounts(list []Account) []Account {
	seen := make(map[string]bool, len(list))
	out := make([]Account, 0, len(list))
	for _, a := range list {
		key := a.Username + "\x00" + a.Password
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
	}
	return out
}

func maskUsername(name string) string {
	if len(name) <= 4 {
		return strings.Repeat("*", len(name))
	}
	return name[:3] + strings.Repeat("*", len(name)-5) + name[len(name)-2:]
}

func parseINI(path string, cfg *Config, accountsFile *string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if err := assignConfig(cfg, accountsFile, key, value); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return scanner.Err()
}

func assignConfig(cfg *Config, accountsFile *string, key, value string) error {
	intField := func(dst *int) error {
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		*dst = n
		return nil
	}
	switch key {
	case "accounts_file":
		*accountsFile = value
	case "username":
		cfg.Accounts = append(cfg.Accounts, Account{Username: value, Password: "", Remark: "config"})
	case "password":
		// 与 username 配对，兼容旧的单账号写法
		if len(cfg.Accounts) > 0 {
			cfg.Accounts[len(cfg.Accounts)-1].Password = value
		}
	case "wifi_interface":
		cfg.Iface = value
	case "portal_host":
		cfg.PortalHost = value
	case "wlanacname":
		cfg.WlanACName = value
	case "ssid":
		cfg.SSID = value
	case "test_url":
		cfg.TestURL = value
	case "state_file":
		cfg.StateFile = value
	case "check_interval":
		return intField(&cfg.CheckInterval)
	case "retry_interval":
		return intField(&cfg.RetryInterval)
	case "max_retries":
		return intField(&cfg.MaxRetries)
	case "backoff_interval":
		return intField(&cfg.BackoffSecond)
	}
	return nil
}
