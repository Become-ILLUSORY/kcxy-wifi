package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	logger = log.New(os.Stdout, "", log.LstdFlags)
)

func logInfo(format string, args ...any)  { logger.Printf("INFO  "+format, args...) }
func logWarn(format string, args ...any)  { logger.Printf("WARN  "+format, args...) }
func logError(format string, args ...any) { logger.Printf("ERROR "+format, args...) }

func second(n int) time.Duration { return time.Duration(n) * time.Second }

// resolveConfigPath 依次尝试：显式参数、系统配置、当前目录、用户配置。
// 系统级安装（pacman）和直接下载二进制两种用法都能开箱即用。
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, candidate := range []string{
		"/etc/kcxy-wifi/config.ini",
		"config.ini",
		filepath.Join(userConfigDir(), "kcxy-wifi", "config.ini"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "config.ini"
}

func userConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return dir
	}
	return "."
}

// version 由构建时注入：-ldflags "-X main.version=1.0.0"
var version = "dev"

func main() {
	var (
		configPath = flag.String("c", "", "配置文件路径，默认 /etc/kcxy-wifi/config.ini 或当前目录 config.ini")
		once       = flag.Bool("once", false, "只执行一次认证")
		status     = flag.Bool("status", false, "只查询在线状态")
		offline    = flag.Bool("offline", false, "下线：注销并暂停自动认证")
		resume     = flag.Bool("resume", false, "恢复自动认证")
		showVer    = flag.Bool("version", false, "显示版本")
		timeout    = flag.Int("timeout", 8, "单次请求超时（秒）")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("kcxy-wifi %s\n", version)
		return
	}

	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		logError("读取配置失败: %v", err)
		os.Exit(2)
	}

	portal := NewPortal(cfg.PortalHost, cfg.Iface, cfg.WlanACName, second(*timeout))

	switch {
	case *offline:
		os.Exit(doOffline(cfg, portal))
	case *resume:
		os.Exit(doResume(cfg))
	case *status:
		if isPaused(cfg) {
			fmt.Printf("离线（已暂停自动认证，%s 起）\n", pausedSince(cfg))
			return
		}
		info, err := portal.OnlineInfo()
		if err != nil {
			logError("查询失败: %v", err)
			os.Exit(1)
		}
		if info.UserIndex != nil && *info.UserIndex != "" {
			fmt.Println("在线")
		} else {
			fmt.Println("离线")
		}
		return
	}

	if len(cfg.Accounts) == 0 {
		logError("没有可用账号：在 config.ini 指定 accounts_file，或设置 KCXY_USERNAME / KCXY_PASSWORD")
		os.Exit(2)
	}

	os.Exit(run(cfg, portal, *once))
}

// doOffline 注销并把进程置为暂停，常驻进程看到标记后不再自动重连。
func doOffline(cfg Config, portal *Portal) int {
	result, err := portal.Logout()
	if err != nil {
		logError("注销请求失败: %v", err)
	} else if result.Result != "success" {
		logWarn("注销返回: %s", result.Message)
	}

	if err := setPaused(cfg, "用户手动下线"); err != nil {
		logError("写入下线状态失败: %v", err)
		return 1
	}
	fmt.Println("已下线，自动认证已暂停。恢复请执行：kcxy-wifi -resume")
	return 0
}

func doResume(cfg Config) int {
	if !isPaused(cfg) {
		fmt.Println("当前未处于暂停状态")
		return 0
	}
	if err := clearPaused(cfg); err != nil {
		logError("清除下线状态失败: %v", err)
		return 1
	}
	fmt.Println("已恢复自动认证")
	return 0
}

func run(cfg Config, portal *Portal, once bool) int {
	failures := 0
	// 记住上次成功的账号，下次优先尝试，避免每次都从失败账号开始
	start := 0

	for {
		if isPaused(cfg) {
			logInfo("处于下线状态，跳过自动认证（执行 -resume 恢复）")
			if once {
				return 0
			}
			time.Sleep(second(cfg.CheckInterval))
			continue
		}

		if !waitForLink(cfg, portal) {
			logWarn("校园网未就绪，%d 秒后重试", cfg.RetryInterval)
			reconnectWifi(cfg)
			if once {
				return 1
			}
			time.Sleep(second(cfg.RetryInterval))
			continue
		}

		online := portal.IsOnline()
		if online && internetOK(cfg.TestURL, cfg.PortalHost) {
			logInfo("在线状态正常")
			failures = 0
			if once {
				return 0
			}
			time.Sleep(second(cfg.CheckInterval))
			continue
		}
		if online {
			logWarn("门户显示在线但外网不通，重新认证")
		} else {
			logInfo("检测到未认证或已掉线，开始登录")
		}

		ok := false
		for i := 0; i < len(cfg.Accounts); i++ {
			idx := (start + i) % len(cfg.Accounts)
			account := cfg.Accounts[idx]
			logInfo("尝试账号 %s（%d/%d）", account.Label(), i+1, len(cfg.Accounts))

			result, err := portal.Login(account.Username, account.Password)
			if err != nil {
				logError("账号 %s 登录请求异常: %v", account.Label(), err)
				continue
			}
			if result.Result == "success" || portal.IsOnline() {
				logInfo("账号 %s 认证成功", account.Label())
				start = idx
				ok = true
				break
			}
			logWarn("账号 %s 认证失败: %s", account.Label(), result.Message)
		}

		if ok {
			failures = 0
			if once {
				return 0
			}
			time.Sleep(second(cfg.CheckInterval))
			continue
		}

		failures++
		logWarn("所有 %d 个账号均认证失败（第 %d 次）", len(cfg.Accounts), failures)
		if once {
			return 1
		}
		if failures >= cfg.MaxRetries {
			logWarn("连续失败 %d 次，退避 %d 秒", failures, cfg.BackoffSecond)
			time.Sleep(second(cfg.BackoffSecond))
			failures = 0
		} else {
			time.Sleep(second(cfg.RetryInterval) + time.Duration(rand.Intn(2000))*time.Millisecond)
		}
	}
}

func waitForLink(cfg Config, portal *Portal) bool {
	deadline := time.Now().Add(second(30))
	for time.Now().Before(deadline) {
		ip, err := InterfaceIPv4(cfg.Iface)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		if PortalReachable(cfg.PortalHost, 3) {
			logInfo("网卡 %s 已就绪，IP %s", cfg.Iface, ip)
			return true
		}
		logWarn("门户 %s 不可达，等待中", cfg.PortalHost)
		time.Sleep(2 * time.Second)
	}
	return false
}

// internetOK 做真实联网检测，避免门户显示在线但实际被劫持。
func internetOK(testURL, portalHost string) bool {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(testURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return false
	}
	return !strings.Contains(resp.Header.Get("Location"), portalHost)
}

func reconnectWifi(cfg Config) {
	if cfg.SSID == "" {
		return
	}
	logWarn("尝试重连无线 %s", cfg.SSID)
	cmd := exec.Command("nmcli", "device", "connect", cfg.Iface)
	if out, err := cmd.CombinedOutput(); err != nil {
		logWarn("nmcli 重连失败: %s", strings.TrimSpace(string(out)))
	}
}
