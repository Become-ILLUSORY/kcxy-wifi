# KCXY-WIFI

河南开封科技传媒学院（复兴大道校区）校园网自动认证工具。针对锐捷 RG-SAM+ eportal 门户
`http://10.147.103.3/eportal/`，支持掉线自动重连。

## 工作原理

门户认证流程：

1. `POST /eportal/InterFace.do?method=pageInfo` 获取公钥和认证策略
2. 用公钥对密码做 RSA 加密（无填充，明文按小端解释为大整数）
3. `POST /eportal/InterFace.do?method=login` 提交账号密码
4. 定期 `POST /eportal/InterFace.do?method=getOnlineUserInfo` 检测在线状态

检测掉线的判据有两条，任一不满足就重新认证：

- 门户的 `getOnlineUserInfo` 返回 `userIndex` 为空
- 访问 `test_url` 失败或被重定向回门户

## 使用

```bash
cp config.example.ini config.ini
cp accounts.example.json accounts.json
$EDITOR accounts.json       # 填写账号密码，可填多个

./kcxy-wifi                 # 常驻运行，自动检测和重连
./kcxy-wifi -once           # 只认证一次
./kcxy-wifi -status         # 查询在线状态
./kcxy-wifi -offline        # 下线：注销并暂停自动认证
./kcxy-wifi -resume         # 恢复自动认证
```

### 下线与恢复

`-offline` 会注销当前登录，并写入一个暂停标记文件。常驻进程每轮检查到该标记
就跳过认证，所以下线不会被自动重连顶回去。

```bash
./kcxy-wifi -offline        # 注销 + 暂停，之后不会再自动登录
./kcxy-wifi -status         # 显示「离线（已暂停自动认证）」
./kcxy-wifi -resume         # 清除标记，下一轮自动重新认证
```

标记文件默认是当前目录下的 `kcxy-wifi.paused`，可用 `state_file` 指定。
装成 systemd 服务时工作目录是 `/var/lib/kcxy-wifi`，标记文件也在那里，
所以手动执行 `-offline` / `-resume` 时要带同样的 `-c` 配置路径。

### 多账号

账号密码放在独立的 JSON 文件里，由 `config.ini` 的 `accounts_file` 指向。
每次认证会按顺序尝试，第一个能用的就停。记住上次成功的账号，下次优先用它。

支持两种格式：

```json
{"accounts": [
  {"username": "20240001", "password": "123456", "remark": "主账号"},
  {"username": "20240002", "password": "654321", "remark": "备用"}
]}
```

```json
[
  {"username": "20240001", "password": "123456"}
]
```

`remark` 只用于日志区分，可以省略。日志里账号会脱敏显示。

也可以用环境变量临时覆盖，优先级最高：

```bash
KCXY_USERNAME=20240001 KCXY_PASSWORD=123456 sudo -E ./kcxy-wifi
```

## USB 共享网络说明

如果电脑通过 USB 共享手机网络上网，USB 网卡的默认路由优先级会高于 WiFi，
此时门户 `10.147.103.3` 会被错误地从 USB 网卡发出而不可达。这是临时联网
对话场景下的限制，不影响正常使用——断开 USB 共享、只走校园网 WiFi 时门户
本来就直达，程序不做任何路由改动。

如果确实需要在 USB 共享的同时认证，手动加一条路由即可：

```bash
sudo ip route add 10.147.103.3/32 via <WiFi网关> dev wlan0
```

## 开机自启

```bash
sudo install -Dm755 kcxy-wifi /usr/local/bin/kcxy-wifi
sudo install -Dm600 config.ini /etc/kcxy-wifi/config.ini
sudo install -Dm644 kcxy-wifi.service /etc/systemd/system/kcxy-wifi.service
sudo systemctl daemon-reload
sudo systemctl enable --now kcxy-wifi
journalctl -u kcxy-wifi -f
```

## 构建

需要 Go 1.22+：

```bash
go build -trimpath -ldflags "-s -w" -o kcxy-wifi .
go test ./...
```

推送 `v*` 标签会触发 GitHub Actions 编译 amd64 / arm64 / armv7 / mipsle
四个平台并自动发布 Release。

## 说明

- 密码默认是身份证后六位，末尾 X 大写。首次使用需到校园网服务中心开通。
- 登录接口不需要验证码；如果门户将来开启验证码，脚本会返回门户给出的错误信息。
- 只依赖 Go 标准库，编译产物是单文件，可静态部署到路由器。
