package main

import (
	"os"
	"path/filepath"
	"testing"
)

const testModulus = "94dd2a8675fb779e6b9f7103698634cd400f27a154afa67af6166a43fc26417222a79506d34cacc7641946abda1785b7acf9910ad6a0978c91ec84d40b71d2891379af19ffb333e7517e390bd26ac312fe940c340466b4a5d4af1d65c3b5944078f96a1a51a5a53e4bc302818b7c9f63c4a1b07bd7d874cef1c3d4b2f5eb7871"

func TestRSAEncryptMatchesPortalJS(t *testing.T) {
	// 期望值由门户 js/security.js 的 RSAUtils.encryptedString 复刻得出
	const want = "3635bfa0cf35109876cb56e271d2a9a750b52b5a465665fd84d04eadc4377d0c" +
		"ebe92a69291f6b65543c4f9de6cb4f5a9a9050cea8150613b16319669c1a99547" +
		"0c9dd80ba455d37976958cdefe28466a652473628401151e83e5f849b98550539" +
		"8279d6996e3a2cc846669e0dd6e8d1ac4436f88fa0c62f776a4ba7840f538e"

	got, err := RSAEncrypt("123456", testModulus, "10001")
	if err != nil {
		t.Fatalf("RSAEncrypt 出错: %v", err)
	}
	if got != want {
		t.Fatalf("加密结果不一致\n got: %s\nwant: %s", got, want)
	}
}

func TestRSAEncryptRejectsOversizedPassword(t *testing.T) {
	// 模数 128 字节，超过即无法加密
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := RSAEncrypt(string(long), testModulus, "10001"); err == nil {
		t.Fatal("超长密码应当报错")
	}
}

func TestReverseRunes(t *testing.T) {
	cases := map[string]string{
		"secret123": "321terces",
		"abc":       "cba",
		"a":         "a",
		"":          "",
		"密码123":     "321码密",
	}
	for in, want := range cases {
		if got := reverseRunes(in); got != want {
			t.Errorf("reverseRunes(%q) = %q, want %q", in, got, want)
		}
	}
}

// 门户登录前会反转密码再加密，这里锁定「反转后加密」的结果，
// 防止以后误改回直接加密（那样服务端会报密码错误）。
func TestRSAEncryptReversedPassword(t *testing.T) {
	const want = "7efd8fd98fa4fde686ee35e7f2d2a914a82548af54e0e69c973dd1dc1c3819aa" +
		"982ad50ee65011687b6652ca12e66a6377c3a9539fb3ca1543fb7cf1e88eb56e5" +
		"946620d2b09ed454bfe8451fb7f430018af0c0a82e1f70bcf317cb1b398a5f541" +
		"ad8215c3ae963d7ea45d46aeade264fad40bc2aab7dd375e0b8c2ddc378aa7"

	got, err := RSAEncrypt(reverseRunes("secret123"), testModulus, "10001")
	if err != nil {
		t.Fatalf("RSAEncrypt 出错: %v", err)
	}
	if got != want {
		t.Fatalf("反转密码加密结果不一致\n got: %s\nwant: %s", got, want)
	}
}

func TestRSAEncryptDefaultsExponent(t *testing.T) {
	withDefault, err := RSAEncrypt("test", testModulus, "")
	if err != nil {
		t.Fatalf("缺省指数应当使用 10001: %v", err)
	}
	explicit, _ := RSAEncrypt("test", testModulus, "10001")
	if withDefault != explicit {
		t.Fatal("缺省指数与 10001 结果应一致")
	}
}

func TestParseConfig(t *testing.T) {
	cfg := DefaultConfig()
	accountsFile := ""
	assign := func(k, v string) {
		if err := assignConfig(&cfg, &accountsFile, k, v); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	assign("accounts_file", "accounts.json")
	assign("check_interval", "15")
	assign("wifi_interface", "wlp2s0")

	if accountsFile != "accounts.json" {
		t.Fatalf("账号文件解析错误: %s", accountsFile)
	}
	if cfg.CheckInterval != 15 {
		t.Fatalf("间隔解析错误: %d", cfg.CheckInterval)
	}
	if cfg.Iface != "wlp2s0" {
		t.Fatalf("网卡解析错误: %s", cfg.Iface)
	}
}

func TestParseInlineAccount(t *testing.T) {
	cfg := DefaultConfig()
	accountsFile := ""
	if err := assignConfig(&cfg, &accountsFile, "username", "20240001"); err != nil {
		t.Fatal(err)
	}
	if err := assignConfig(&cfg, &accountsFile, "password", "secret"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("应当解析出 1 个账号，实际 %d", len(cfg.Accounts))
	}
	if cfg.Accounts[0].Username != "20240001" || cfg.Accounts[0].Password != "secret" {
		t.Fatalf("账号解析错误: %+v", cfg.Accounts[0])
	}
}

func TestLoadAccountsWrapper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	data := `{"accounts":[{"username":"a","password":"1"},{"username":"b","password":"2","remark":"备用"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	accounts, err := LoadAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("应当读取 2 个账号，实际 %d", len(accounts))
	}
	if accounts[1].Remark != "备用" {
		t.Fatalf("备注解析错误: %+v", accounts[1])
	}
}

func TestLoadAccountsPlainArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	data := `[{"username":"a","password":"1"},{"username":"","password":"x"}]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	accounts, err := LoadAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("空账号应被过滤，实际 %d 个", len(accounts))
	}
}

func TestDedupeAccounts(t *testing.T) {
	list := []Account{
		{Username: "a", Password: "1"},
		{Username: "a", Password: "1"},
		{Username: "a", Password: "2"},
	}
	if got := dedupeAccounts(list); len(got) != 2 {
		t.Fatalf("去重后应为 2 个，实际 %d", len(got))
	}
}

func TestMaskUsername(t *testing.T) {
	if got := maskUsername("20240001"); got != "202***01" {
		t.Fatalf("脱敏结果错误: %s", got)
	}
	if got := maskUsername("abc"); got != "***" {
		t.Fatalf("短账号脱敏错误: %s", got)
	}
}

func TestIsRiskControlMessage(t *testing.T) {
	positive := []string{
		"验证码错误.",
		"认证设备响应超时,请稍后再试!",
		"操作过于频繁",
	}
	for _, msg := range positive {
		if !isRiskControlMessage(msg) {
			t.Errorf("%q 应被识别为风控", msg)
		}
	}
	negative := []string{
		"用户不存在或者密码错误!",
		"校园网认证失败：账号不存在，请到联系校园网服务中心开通。",
		"",
	}
	for _, msg := range negative {
		if isRiskControlMessage(msg) {
			t.Errorf("%q 不应被识别为风控", msg)
		}
	}
}
