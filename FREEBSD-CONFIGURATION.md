# sing-box FreeBSD 配置指南（1.14f）

> 适用：vxzman/sing-box fork 的 `freebsd` 分支构建的 `1.14f` 版本。支持 FreeBSD 12.4+（建议 15+，见文末"版本差异"）。

## 通用前提

- **必须 root 运行**：FreeBSD 没有 setcap 等价物，tun 创建/路由写入/sysctl/pf 全是 uid 级特权检查。降权只有 VNET jail 一条路（另见开发文档）
- 二进制是交叉编译的静态 ELF，直接拷贝即可：`scp sing-box freebsd:/usr/local/bin/`
- 两个模式是**互斥**的：tun 管本机（+可选网关模式管局域网），redirect 只能管局域网入站流量

---

## 部署为 rc 服务

目录约定：二进制 `/usr/local/bin/sing-box`，工作目录 `/usr/local/etc/sing-box/`（`-D`：规则集、cache_file、dashboard 等运行时文件都落在该目录），配置 `/usr/local/etc/sing-box/config.json`。

`/usr/local/etc/rc.d/sing_box`：

```sh
#!/bin/sh

# PROVIDE: sing_box
# REQUIRE: LOGIN DAEMON NETWORKING
# BEFORE:  securelevel
# KEYWORD: shutdown

. /etc/rc.subr

name="sing_box"
rcvar="sing_box_enable"

# 用 daemon(8) 包装：sing-box 自身不写 pidfile，daemon 负责守护与 pid 管理。
# 注意必须 root 运行（FreeBSD 无 setcap，tun/pf/sysctl 需要特权）。
command="/usr/sbin/daemon"
command_args="-p /var/run/${name}.pid -o /var/log/${name}.log /usr/local/bin/sing-box run -D /usr/local/etc/sing-box -c /usr/local/etc/sing-box/config.json"
procname="/usr/local/bin/sing-box"
pidfile="/var/run/${name}.pid"

load_rc_config "${name}"

: "${sing_box_enable:="NO"}"

run_rc_command "$1"
```

安装与启用：

```sh
chmod +x /usr/local/etc/rc.d/sing_box
sysrc sing_box_enable=YES
service sing_box start
service sing_box status
```

**系统级配置（按模式选做，都是持久化配置）**：

1. **Tun 模式 —— `/boot/loader.conf`**（FIB tunable，只读于运行时，必须写文件 + 重启一次；sing-box 启动时会先尝试运行时写入，写不进去才报错提示这里）：

   ```
   net.fibs=2023               # 必须 > iproute2_table_index（2022）
   net.add_addr_allfibs=1      # tun 地址对所有 FIB 可见（回环避免依赖）
   ```

2. **网关模式 —— `/etc/sysctl.conf`**（局域网设备走本机代理时才需要；仅本机使用**不要开**）：

   ```
   net.inet.ip.forwarding=1
   ```

   配合：局域网客户机把网关/DNS 指向本机。不需要 IP 转发时（tun 只管本机、或 redirect 模式）保持默认 0。

3. **Redirect 模式 —— pf 服务 + rc 启动顺序**：pf 必须先于 sing-box 就绪（redirect 入站依赖 `/dev/pf` 做 DIOCNATLOOK）。rc 脚本的 REQUIRE 行改为：

   ```sh
   # REQUIRE: LOGIN DAEMON NETWORKING pf
   ```

   并 `sysrc pf_enable=YES`（pf 规则本身已写入 `/etc/pf.conf`，随 pf 服务加载；pf 内核模块随服务自动 kldload）。

**日志**：`daemon -o` 把 sing-box 输出追加到 `/var/log/sing_box.log`；配 logrotate（`newsyslog`）或接受持续增长，按需。

---

## 模式一：Tun 模式（本机 + 可扩展为网关）

### 配置

```jsonc
{
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "interface_name": "tun0",          // FreeBSD 惯例 tunN（不要用 macOS 的 utunN）
      "address": ["172.19.0.1/30", "2001:470:f9da:fdfa::1/64"],
      "mtu": 1500,
      "stack": "gvisor",                  // gvisor 或 system 均可
      "auto_route": true,
      // 注意：不要写 auto_redirect（Linux 专属，FreeBSD 上会报错拒绝）
      // iproute2_table_index 可选，默认 2022：
      // 在 FreeBSD 上它就是"回环隔离用的 FIB 编号"，一般不用改
    }
  ]
}
```

### 系统要求（启动时自动处理）

回环避免依赖一个独立 FIB。sing-box 启动时会自动尝试运行时写入：

```
net.fibs=2023               # 必须 > iproute2_table_index（2022）
net.add_addr_allfibs=1      # tun 地址对所有 FIB 可见
```

如果运行时写不进去（只读 tunable），启动会报错并提示你写 `/boot/loader.conf` 后重启：

```
# /boot/loader.conf
net.fibs=2023
net.add_addr_allfibs=1
```

### 验证

```sh
sysctl net.fibs net.add_addr_allfibs        # 2023 / 1
netstat -rn -F 0 | head                     # 主表：大量子网段捕获路由 → tun0
netstat -rn -F 2022 | head                  # 隔离表：default <真网关> UGS em0
ifconfig tun0                               # UP + 地址 + nd6 NO_DAD
curl -4 https://www.google.com              # 全链路 + 无回环（CPU 应接近 idle）
```

### 本机 DNS（重要）

tun 模式下公网 DNS 查询会被捕获劫持进 sing-box DNS（返回 fakeip）。但**私网 DNS（如 192.168.5.53）不参与捕获**，会直连出去拿污染答案。本机要用 fakeip 就手动指向 tun 的 DNS 地址（tun 地址 +1）：

```sh
echo 'nameserver 172.19.0.2' > /etc/resolv.conf    # 仅在 tun 起来后可达
```

### 扩展为网关模式（局域网设备走代理）

持久化开启转发（见"部署为 rc 服务"一节的系统级配置）：

```sh
sysrc -f /etc/sysctl.conf net.inet.ip.forwarding=1   # 或直接编辑文件
sysctl net.inet.ip.forwarding=1                      # 当前立即生效
# 局域网客户机把网关指向这台 FreeBSD 的 IP 即可
```

---

## 模式二：Redirect 模式（**仅对局域网设备有效**）

### ⚠️ 核心限制：redirect 只对"入站"数据包生效

这是 pf 与 iptables/nftables 的根本设计差异：

- pf 的 `rdr-to` 是**入站翻译**；**出站方向 rdr 到本地地址明确不受支持**（man page 原文："If applied outbound, rdr-to to a local IP address is not supported"）
- 因此：**本机自己的流量无法用 redirect 接管**（本机流量请用模式一 tun）；redirect 的目标是"流经这台机器的局域网设备流量"
- bsd-box 的模型相同：redirect 服务于 pfSense/OPNsense 上的端口转发场景

### 配置

```jsonc
{
  "inbounds": [
    {
      "type": "redirect",
      "tag": "redirect-in",
      "listen": "::",                       // 监听所有接口（bsd-box 模板同款）
      "listen_port": 7891,
      "sniff": true                         // 让 geosite 等域名规则生效
    },
    {
      "type": "dns",
      "tag": "dns-in",
      "listen": "192.168.122.135",          // 换成机器网卡地址
      "listen_port": 5353
    }
  ]
}
```

### pf 规则

```sh
kldload pf        # 15 上 pf 是模块，先加载（/dev/pf 依赖它；必须在 sing-box 处理连接前就位）

cat > /etc/pf.conf << 'EOF'
# TCP 53/80/443 → redirect 入站（TCP-only）
rdr on em0 proto tcp from 192.168.122.0/24 to any port {53, 80, 443} -> 192.168.122.135 port 7891
# UDP 53 → dns 入站（redirect 收不了 UDP）
rdr on em0 proto udp from 192.168.122.0/24 to any port 53 -> 192.168.122.135 port 5353
pass all
EOF
pfctl -e -f /etc/pf.conf
```

三个要点：
1. **`pass all` 不能省**——pf 默认丢弃所有未匹配流量，只有 rdr 没有 pass 会直接断网
2. rdr 目标用**网卡地址**而非 127.0.0.1（避免回环投递 quirks；入站 rdr 到 127.0.0.1 虽然合法但不如网卡地址稳）
3. `rdr`（DNAT）语义正确；`nat-to` 是出站 SNAT（家用路由器场景），与透明代理无关

### 验证

```sh
pfctl -s nat -v        # rdr 规则的 Packets/Bytes 计数器——客户机访问前后对比，涨了 = 命中
pfctl -s state         # 有 rdr 状态条目
# sing-box 日志出现 inbound/redirect[redirect-in]，且目标是"原始 IP:80"（不是 192.168.122.135:7891）
#   —— 日志显示原始目标 = DIOCNATLOOK 反查成功
```

客户端测试（在局域网其他机器上）：

```sh
curl http://8.8.8.8/                  # 经 rdr → redirect → 代理，得 Google 301
dig @223.5.5.5 www.google.com         # 经 rdr udp 53 → dns 入站 → fakeip (128.32.x)
dig +tcp @223.5.5.5 www.google.com    # TCP 路径（注意是 +tcp；-tcp 会被 dig 解析成 -t cp 报错）
```

---

## 常见故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 启动报 `dial tcp ...: invalid argument` | FIB 未建立就注册了 SO_SETFIB（旧版 bug，已修）；或 net.fibs 写不进去 | 确认已更新到最新构建；检查 `sysctl net.fibs` |
| `SIOCSIFNAME: file exists` | 残留的 tun0 设备（强杀/旧版） | `ifconfig tun0 destroy` 后重启 |
| kill -9 后设备残留 | FreeBSD 12–14 内核行为（关闭从不销毁设备） | 15+ 已自动销毁（TUNSTRANSIENT）；12–14 手动 `ifconfig tunN destroy` |
| 启用 pf 后全断网 | pf.conf 缺 `pass all`（pf 默认丢弃未匹配流量） | 加 `pass all` 并 `pfctl -f /etc/pf.conf` |
| 盒子上 curl 测 redirect 无反应 | rdr 只对入站生效，本机流量不经过 | 从局域网客户机测试；本机用 tun 模式 |
| 客户机 DNS 拿到污染 IP | UDP 53 没被 rdr（规则只有 proto tcp）或没配 dns 入站 | 按上面 pf 规则补 udp 行 + dns 入站 |
| `dig -tcp` 报 "invalid type cp" | dig 把 `-tcp` 解析成 `-t cp` | 用 `dig +tcp` |
| 规则集下载慢/超时 | raw.githubusercontent.com CDN 对直连限流 | `download_detour` 改走代理（FIB 隔离下不会回环，放心用） |

## FreeBSD 版本差异

| 项 | 12 / 13 / 14 | 15 |
|---|---|---|
| 强杀后 tun 设备 | 残留（需手动 destroy） | 自动销毁（TUNSTRANSIENT） |
| pf | 内核模块 kldload | 同左 |
| 进程匹配偏移 | 12.4+ 布局一致，均支持（仅 64 位） | 支持 |
