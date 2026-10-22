package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// PortalReachable 用 TCP 连接判断门户是否可达。
func PortalReachable(host string, timeoutSecond int) bool {
	conn, err := net.DialTimeout("tcp", host+":80", time.Duration(timeoutSecond)*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// connectivityTargets 是 captive portal 探测地址。
// 正常情况下应返回 204 且无响应体；被门户劫持时会返回 200 + HTML 或 302。
var connectivityTargets = []string{
	"http://connect.rom.miui.com/generate_204",
	"http://www.gstatic.com/generate_204",
}

// bindToDevice 让连接从指定网卡发出，避免被 USB 共享等其他默认路由抢走。
func bindToDevice(iface string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var bindErr error
		err := c.Control(func(fd uintptr) {
			bindErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		})
		if err != nil {
			return err
		}
		return bindErr
	}
}

func newBoundClient(iface string, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: bindToDevice(iface),
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// HasInternet 判断指定网卡是否真的能上外网。
// 只有返回 204 或 200 且没有被重定向到门户，才算真正连通。
func HasInternet(iface, portalHost string, timeout time.Duration) bool {
	client := newBoundClient(iface, timeout)
	for _, target := range connectivityTargets {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			continue
		}
		status := resp.StatusCode
		location := resp.Header.Get("Location")
		resp.Body.Close()

		if status != http.StatusNoContent && status != http.StatusOK {
			continue
		}
		// 被重定向到门户说明还在认证页，不算连通
		if portalHost != "" && containsHost(location, portalHost) {
			continue
		}
		return true
	}
	return false
}

func containsHost(location, host string) bool {
	if location == "" || host == "" {
		return false
	}
	return strings.Contains(location, host)
}
