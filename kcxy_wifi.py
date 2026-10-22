#!/usr/bin/env python3
"""KCXY 校园网（锐捷 RG-SAM+ eportal）自动认证与掉线重连。

门户：http://10.147.103.3/eportal/
认证接口：/eportal/InterFace.do?method=login
密码使用 RSA 无填充加密（passwordEncrypt=true）。
"""

from __future__ import annotations

import argparse
import configparser
import fcntl
import logging
import random
import socket
import struct
import subprocess
import sys
import time
from pathlib import Path

import requests

LOG = logging.getLogger("kcxy")

DEFAULT_CONFIG = Path(__file__).with_name("config.ini")


def load_config(path: Path) -> configparser.ConfigParser:
    if not path.is_file():
        sys.exit(f"配置文件不存在: {path}")
    cfg = configparser.ConfigParser()
    cfg.read(path, encoding="utf-8")
    return cfg


def interface_ipv4(iface: str) -> str:
    """取网卡 IPv4 地址，失败抛 OSError。"""
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        packed = fcntl.ioctl(
            sock.fileno(), 0x8915, struct.pack("256s", iface[:15].encode())
        )
        return socket.inet_ntoa(packed[20:24])
    finally:
        sock.close()


def interface_mac(iface: str) -> str:
    return Path(f"/sys/class/net/{iface}/address").read_text().strip().lower()


def rsa_encrypt(message: str, modulus_hex: str, exponent_hex: str) -> str:
    """复刻 jsbn 的 RSAUtils.encryptedString：小端字节转大整数，无填充。

    biToHex 按 16 位一组补零输出，因此结果需要补齐到模数的十六进制长度。
    """
    modulus = int(modulus_hex, 16)
    exponent = int(exponent_hex, 16)
    data = message.encode("utf-8")
    value = int.from_bytes(data, "little")
    width = (len(modulus_hex) + 3) // 4 * 4
    return format(pow(value, exponent, modulus), f"0{width}x")


class Portal:
    def __init__(self, host: str, iface: str, wlanacname: str = "", timeout: float = 8.0):
        self.host = host
        self.iface = iface
        self.wlanacname = wlanacname
        self.timeout = timeout
        self.session = requests.Session()
        self.session.headers.update(
            {
                "User-Agent": (
                    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
                    "AppleWebKit/537.36 (KHTML, like Gecko) "
                    "Chrome/120.0.0.0 Safari/537.36"
                ),
                "Referer": f"http://{host}/eportal/",
            }
        )

    @property
    def base(self) -> str:
        return f"http://{self.host}/eportal"

    def _query_string(self) -> str:
        ip = interface_ipv4(self.iface)
        mac = interface_mac(self.iface)
        parts = [
            f"wlanuserip={ip}",
            f"wlanacname={self.wlanacname}",
            f"nasip={self.host}",
            f"mac={mac}",
            "t=wireless-v2",
            "url=http://www.baidu.com",
        ]
        return "&".join(parts)

    def _post_json(self, method: str, data: dict) -> dict:
        # 门户前端手工拼接表单体，queryString 里的 url 不做二次编码，
        # 这里同样按原始字符串提交，避免 requests 再转义一次
        body = "&".join(f"{k}={v}" for k, v in data.items())
        resp = self.session.post(
            f"{self.base}/InterFace.do?method={method}",
            data=body,
            headers={"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8"},
            timeout=self.timeout,
        )
        resp.raise_for_status()
        resp.encoding = "utf-8"
        return resp.json()

    def page_info(self) -> dict:
        return self._post_json("pageInfo", {"queryString": self._query_string()})

    def warmup(self) -> None:
        """先访问门户首页建立会话，否则加密登录会被判为密码错误。"""
        self.session.get(f"{self.base}/index.jsp", timeout=self.timeout)

    def login(self, username: str, password: str) -> dict:
        self.warmup()
        info = self.page_info()
        encrypt = str(info.get("passwordEncrypt", "")).lower() == "true"
        if encrypt:
            # 门户前端加密前会把密码反转：password.split("").reverse().join("")
            encrypted = rsa_encrypt(
                password[::-1],
                info["publicKeyModulus"],
                info.get("publicKeyExponent", "10001"),
            )
        else:
            encrypted = password
        return self._post_json(
            "login",
            {
                "userId": username,
                "password": encrypted,
                "service": "",
                "queryString": self._query_string(),
                "operatorPwd": "",
                "operatorUserId": "",
                "validcode": "",
                "passwordEncrypt": "true" if encrypt else "false",
            },
        )

    def online_info(self) -> dict:
        return self._post_json(
            "getOnlineUserInfo", {"queryString": self._query_string()}
        )

    def is_online(self) -> bool:
        try:
            return bool(self.online_info().get("userIndex"))
        except (requests.RequestException, ValueError):
            return False

    def logout(self) -> dict:
        return self._post_json("logout", {"queryString": self._query_string()})


def portal_reachable(portal_host: str, timeout: float = 3.0) -> bool:
    try:
        with socket.create_connection((portal_host, 80), timeout=timeout):
            return True
    except OSError:
        return False


def route_via(iface: str, dest: str) -> bool:
    """检查 dest 是否已经有走 iface 的路由（读 /proc/net/route）。"""
    target = int.from_bytes(socket.inet_aton(dest), "little")
    try:
        with open("/proc/net/route", encoding="ascii") as fh:
            next(fh)
            for line in fh:
                fields = line.split()
                if fields[0] != iface:
                    continue
                net = int(fields[1], 16)
                mask = int(fields[7], 16)
                if target & mask == net:
                    return True
    except (OSError, StopIteration):
        pass
    return False


def default_gateway(iface: str) -> str | None:
    """取该网卡的默认网关。"""
    try:
        with open("/proc/net/route", encoding="ascii") as fh:
            next(fh)
            for line in fh:
                fields = line.split()
                if fields[0] == iface and fields[1] == "00000000":
                    return socket.inet_ntoa(
                        int(fields[2], 16).to_bytes(4, "little")
                    )
    except (OSError, StopIteration):
        pass
    return None


def ensure_route(iface: str, portal_host: str) -> bool:
    """确保门户地址从校园网网卡走，避免被 USB 共享的默认路由抢走。"""
    if route_via(iface, portal_host):
        return True
    gateway = default_gateway(iface)
    if not gateway:
        LOG.warning("网卡 %s 没有默认网关，无法添加门户路由", iface)
        return False
    cmd = ["ip", "route", "add", f"{portal_host}/32", "via", gateway, "dev", iface]
    LOG.info("添加门户路由: %s", " ".join(cmd))
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True)
    except OSError as exc:
        LOG.error("执行 ip 失败: %s", exc)
        return False
    if proc.returncode != 0:
        LOG.error(
            "添加路由失败: %s（需要 root 权限，可用 sudo 运行本脚本）",
            proc.stderr.strip(),
        )
        return False
    return True


def wait_for_link(iface: str, portal_host: str, timeout: float = 120.0) -> bool:
    """等待网卡拿到地址且门户可达。"""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            ip = interface_ipv4(iface)
        except OSError:
            time.sleep(2)
            continue
        ensure_route(iface, portal_host)
        if portal_reachable(portal_host):
            LOG.info("网卡 %s 已就绪，IP %s", iface, ip)
            return True
        LOG.warning("门户 %s 不可达，等待中", portal_host)
        time.sleep(2)
    return False


def internet_ok(url: str, portal_host: str, timeout: float = 5.0) -> bool:
    """真实联网检测：被门户劫持时通常会 302 到门户或返回非 2xx。"""
    try:
        resp = requests.get(url, timeout=timeout, allow_redirects=False)
    except requests.RequestException:
        return False
    if resp.status_code not in (200, 204):
        return False
    location = resp.headers.get("Location", "")
    return portal_host not in location


def reconnect_wifi(iface: str, ssid: str) -> None:
    """Linux 下用 nmcli 重连无线，失败不影响主流程。"""
    if not ssid:
        return
    LOG.warning("尝试重连无线 %s", ssid)
    try:
        subprocess.run(
            ["nmcli", "device", "connect", iface],
            capture_output=True,
            timeout=30,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        LOG.debug("nmcli 重连失败: %s", exc)


def run(cfg: configparser.ConfigParser, once: bool = False) -> int:
    username = cfg.get("account", "username", fallback="").strip()
    password = cfg.get("account", "password", fallback="").strip()
    if not username or not password:
        LOG.error("请先在 config.ini 中填写账号和密码")
        return 2

    iface = cfg.get("network", "wifi_interface", fallback="wlan0").strip()
    host = cfg.get("network", "portal_host", fallback="10.147.103.3").strip()
    wlanacname = cfg.get("network", "wlanacname", fallback="").strip()
    ssid = cfg.get("network", "ssid", fallback="").strip()
    test_url = cfg.get("network", "test_url", fallback="http://www.baidu.com").strip()

    interval = cfg.getint("behavior", "check_interval", fallback=30)
    retry = cfg.getint("behavior", "retry_interval", fallback=5)
    max_retries = cfg.getint("behavior", "max_retries", fallback=6)
    backoff = cfg.getint("behavior", "backoff_interval", fallback=120)

    portal = Portal(host, iface, wlanacname)
    failures = 0

    while True:
        if not wait_for_link(iface, host, timeout=30):
            LOG.warning("校园网未就绪，%d 秒后重试", retry)
            reconnect_wifi(iface, ssid)
            if once:
                return 1
            time.sleep(retry)
            continue

        online = portal.is_online()
        if online and internet_ok(test_url, host):
            LOG.info("在线状态正常")
            failures = 0
            if once:
                return 0
            time.sleep(interval)
            continue

        if online:
            LOG.warning("门户显示在线但外网不通，重新认证")
        else:
            LOG.info("检测到未认证/已掉线，开始登录")

        try:
            result = portal.login(username, password)
        except (requests.RequestException, ValueError) as exc:
            LOG.error("登录请求异常: %s", exc)
            result = {"result": "fail", "message": str(exc)}

        if result.get("result") == "success" or portal.is_online():
            LOG.info("认证成功")
            failures = 0
            if once:
                return 0
            time.sleep(interval)
            continue

        failures += 1
        LOG.warning("认证失败（第 %d 次）: %s", failures, result.get("message", "未知错误"))

        if once:
            return 1
        if failures >= max_retries:
            LOG.warning("连续失败 %d 次，退避 %d 秒", failures, backoff)
            time.sleep(backoff)
            failures = 0
        else:
            time.sleep(retry + random.uniform(0, 2))


def main() -> int:
    parser = argparse.ArgumentParser(description="KCXY 校园网自动认证")
    parser.add_argument("-c", "--config", type=Path, default=DEFAULT_CONFIG)
    parser.add_argument("--once", action="store_true", help="只执行一次")
    parser.add_argument("--status", action="store_true", help="只查询在线状态")
    parser.add_argument("--logout", action="store_true", help="注销当前登录")
    parser.add_argument("-v", "--verbose", action="store_true")
    args = parser.parse_args()

    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
        datefmt="%H:%M:%S",
    )

    cfg = load_config(args.config)
    iface = cfg.get("network", "wifi_interface", fallback="wlan0").strip()
    host = cfg.get("network", "portal_host", fallback="10.147.103.3").strip()
    wlanacname = cfg.get("network", "wlanacname", fallback="").strip()

    if args.status or args.logout:
        portal = Portal(host, iface, wlanacname)
        if args.logout:
            print(portal.logout())
        else:
            info = portal.online_info()
            print("在线" if info.get("userIndex") else "离线", info)
        return 0

    return run(cfg, once=args.once)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        LOG.info("已停止")
