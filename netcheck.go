package main

import (
	"net"
)

// PortalReachable 用 TCP 连接判断门户是否可达。
func PortalReachable(host string, timeoutSecond int) bool {
	conn, err := net.DialTimeout("tcp", host+":80", second(timeoutSecond))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
