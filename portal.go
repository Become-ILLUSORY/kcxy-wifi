package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// Portal 封装锐捷 RG-SAM+ eportal 的认证接口。
type Portal struct {
	Host       string
	Iface      string
	WlanACName string
	Client     *http.Client
}

// PageInfo 是 pageInfo 接口返回里我们用到的字段。
// 门户把这些开关都序列化成字符串，不能直接用 bool。
type PageInfo struct {
	PasswordEncrypt   string `json:"passwordEncrypt"`
	PublicKeyExponent string `json:"publicKeyExponent"`
	PublicKeyModulus  string `json:"publicKeyModulus"`
	LoginText         string `json:"loginText"`
	ValidCodeURL      string `json:"validCodeUrl"`
}

func (p *PageInfo) EncryptsPassword() bool {
	return strings.EqualFold(strings.TrimSpace(p.PasswordEncrypt), "true")
}

// LoginResult 是 login 接口的返回。
type LoginResult struct {
	UserIndex string `json:"userIndex"`
	Result    string `json:"result"`
	Message   string `json:"message"`
}

// OnlineInfo 是 getOnlineUserInfo 的返回，只取判断在线所需字段。
type OnlineInfo struct {
	UserIndex *string `json:"userIndex"`
	Result    string  `json:"result"`
	Message   string  `json:"message"`
	UserID    *string `json:"userId"`
	UserIP    *string `json:"userIp"`
}

func NewPortal(host, iface, wlanACName string, timeout time.Duration) *Portal {
	return &Portal{
		Host:       host,
		Iface:      iface,
		WlanACName: wlanACName,
		Client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			// 门户依赖 JSESSIONID 把 pageInfo 和 login 关联起来，
			// 缺了 cookie 会直接返回「用户不存在或者密码错误」
			Jar: newSessionJar(),
		},
	}
}

// sessionJar 是一个不做持久化的最小 cookie 容器，只在本进程内保持会话。
func newSessionJar() http.CookieJar {
	jar, _ := cookiejar.New(nil)
	return jar
}

func (p *Portal) base() string {
	return "http://" + p.Host + "/eportal"
}

// QueryString 构造门户要求的接入参数，字段顺序与浏览器一致。
func (p *Portal) QueryString() (string, error) {
	ip, err := InterfaceIPv4(p.Iface)
	if err != nil {
		return "", err
	}
	mac, err := InterfaceMAC(p.Iface)
	if err != nil {
		return "", err
	}
	parts := []string{
		"wlanuserip=" + ip,
		"wlanacname=" + p.WlanACName,
		"nasip=" + p.Host,
		"mac=" + mac,
		"t=wireless-v2",
		// 这里的 URL 保持原样：整串 queryString 在表单提交时会被统一编码一次，
		// 若提前转义会被二次编码，门户解不出来会判定认证失败
		"url=http://www.baidu.com",
	}
	return strings.Join(parts, "&"), nil
}

func (p *Portal) postJSON(method string, form url.Values, out any) error {
	return p.postForm(method, form.Encode(), out)
}

// postForm 直接发送已拼接好的表单体，不再做一次编码。
func (p *Portal) postForm(method, body string, out any) error {
	req, err := http.NewRequest(
		http.MethodPost,
		p.base()+"/InterFace.do?method="+method,
		strings.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", p.base()+"/")

	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回状态码 %d", method, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (p *Portal) PageInfo() (*PageInfo, error) {
	qs, err := p.QueryString()
	if err != nil {
		return nil, err
	}
	var info PageInfo
	err = p.postJSON("pageInfo", url.Values{"queryString": {qs}}, &info)
	return &info, err
}

// Warmup 先访问一次门户首页建立会话。
// 门户要求登录前已有 JSESSIONID 关联的会话上下文，直接 POST login 时
// 明文密码还能通过，但 RSA 加密的密码会被判为「用户不存在或者密码错误」。
func (p *Portal) Warmup() error {
	req, err := http.NewRequest(http.MethodGet, p.base()+"/index.jsp", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (p *Portal) OnlineInfo() (*OnlineInfo, error) {
	qs, err := p.QueryString()
	if err != nil {
		return nil, err
	}
	var info OnlineInfo
	err = p.postJSON("getOnlineUserInfo", url.Values{"queryString": {qs}}, &info)
	return &info, err
}

func (p *Portal) IsOnline() bool {
	info, err := p.OnlineInfo()
	if err != nil {
		return false
	}
	return info.UserIndex != nil && *info.UserIndex != ""
}

func (p *Portal) Login(username, password string) (*LoginResult, error) {
	if err := p.Warmup(); err != nil {
		return nil, err
	}
	info, err := p.PageInfo()
	if err != nil {
		return nil, err
	}
	encrypted := password
	encrypt := info.EncryptsPassword()
	if encrypt {
		// 门户前端在加密前会把密码按字符反转：
		//   password.split("").reverse().join("")
		// 少了这一步服务端解出来的密码是反的，会报「密码错误」
		encrypted, err = RSAEncrypt(reverseRunes(password), info.PublicKeyModulus, info.PublicKeyExponent)
		if err != nil {
			return nil, err
		}
	}
	qs, err := p.QueryString()
	if err != nil {
		return nil, err
	}
	// 门户前端是手工拼接表单体，queryString 里的 url 参数不做二次编码
	body := strings.Join([]string{
		"userId=" + username,
		"password=" + encrypted,
		"service=",
		"queryString=" + qs,
		"operatorPwd=",
		"operatorUserId=",
		"validcode=",
		"passwordEncrypt=" + fmt.Sprintf("%t", encrypt),
	}, "&")
	var result LoginResult
	if err := p.postForm("login", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// reverseRunes 按字符（而非字节）反转，与 JS 的 split("").reverse() 对齐。
func reverseRunes(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func (p *Portal) Logout() (*LoginResult, error) {
	qs, err := p.QueryString()
	if err != nil {
		return nil, err
	}
	var result LoginResult
	err = p.postJSON("logout", url.Values{"queryString": {qs}}, &result)
	return &result, err
}

// RSAEncrypt 复刻门户 JS 的 RSAUtils.encryptedString：
// 明文按 UTF-8 取字节后小端解释为大整数，无填充，指数固定 0x10001。
func RSAEncrypt(plain, modulusHex, exponentHex string) (string, error) {
	if modulusHex == "" {
		return "", errors.New("门户未返回公钥模数")
	}
	if exponentHex == "" {
		exponentHex = "10001"
	}
	modulus, ok := new(big.Int).SetString(modulusHex, 16)
	if !ok {
		return "", errors.New("公钥模数格式错误")
	}
	exponent, ok := new(big.Int).SetString(exponentHex, 16)
	if !ok {
		return "", errors.New("公钥指数格式错误")
	}
	if modulus.Sign() <= 0 {
		return "", errors.New("公钥模数非法")
	}

	data := []byte(plain)
	// 小端字节序转大整数：反转后按大端解释
	reversed := make([]byte, len(data))
	for i := range data {
		reversed[len(data)-1-i] = data[i]
	}
	value := new(big.Int).SetBytes(reversed)
	if value.Cmp(modulus) >= 0 {
		return "", errors.New("密码过长，超出 RSA 模数范围")
	}

	cipher := new(big.Int).Exp(value, exponent, modulus)
	// 门户 JS 的 biToHex 按 16 位十六进制补零，这里对齐到模数长度
	width := (len(modulusHex) + 3) / 4 * 4
	out := cipher.Text(16)
	if len(out) < width {
		out = strings.Repeat("0", width-len(out)) + out
	}
	return out, nil
}

// InterfaceIPv4 取网卡 IPv4 地址。
func InterfaceIPv4(iface string) (string, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return "", err
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return ipnet.IP.To4().String(), nil
		}
	}
	return "", fmt.Errorf("网卡 %s 没有 IPv4 地址", iface)
}

// InterfaceMAC 取网卡 MAC 地址。
func InterfaceMAC(iface string) (string, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return "", err
	}
	if len(ifc.HardwareAddr) == 0 {
		return "", fmt.Errorf("网卡 %s 没有 MAC 地址", iface)
	}
	return strings.ToLower(ifc.HardwareAddr.String()), nil
}

func randomString(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
