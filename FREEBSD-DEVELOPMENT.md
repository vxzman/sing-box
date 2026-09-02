# sing-box FreeBSD 支持：开发要点

> 基于 2026-08 的开发与真机测试记录整理。目标是让后续维护者（包括未来的自己）不需要重新踩坑。
>
> 仓库：`vxzman/sing-box`（分支 `freebsd`，基于官方 testing/1.14）+ `vxzman/sing-tun`（分支 `freebsd`，基于 v0.9.0-beta.4）
>
> 本地路径：`singbox-0/sing-box-freebsd`、`singbox-0/sing-tun-freebsd`；官方对照树 `singbox-0/sing-box`；参考实现 `singbox-0/bsd-box`（Vincent-Loeng，基于 1.12 的补丁集）；FreeBSD 内核源码 `singbox-0/freebsd-src`（完整克隆，含全部 releng 分支）

---

## 1. 双仓库结构与接线

sing-tun 保持原 module path，sing-box 用 replace 指向 fork：

```
sing-box/go.mod:
  replace github.com/sagernet/sing-tun => github.com/vxzman/sing-tun v0.9.0-beta.4.0.<UTC时间戳>-<sha12>
```

**重要**：goproxy.cn 对 fork 分支的缓存滞后，每次 sing-tun 有新提交后必须**手算伪版本**更新 replace，不能依赖 `@freebsd` 分支名解析：

```bash
cd sing-tun-freebsd && git push origin freebsd
SHA12=$(git rev-parse --short=12 freebsd)
TS=$(date -u -d @$(git log -1 --format=%ct freebsd) +%Y%m%d%H%M%S)
PSEUDO="v0.9.0-beta.4.0.${TS}-${SHA12}"
cd ../sing-box-freebsd && go mod edit -replace=github.com/sagernet/sing-tun=github.com/vxzman/sing-tun@$PSEUDO
GOTOOLCHAIN=go1.26.7 GOPROXY=https://goproxy.cn go mod download github.com/sagernet/sing-tun
```

本机推送走 gh-proxy（github.com DNS 不通），需带 credential helper：

```bash
git -c credential.helper='!f() { echo username=vxzman; echo password=$(gh auth token); }; f' push origin freebsd
```

---

## 2. 构建

```bash
GOTOOLCHAIN=go1.27.1 GOPROXY=https://goproxy.cn \
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 \
go build -trimpath \
  -ldflags "-X 'github.com/sagernet/sing-box/constant.Version=1.14.1f' -s -w -buildid=" \
  -tags 'with_gvisor,with_quic,with_utls,with_acme,with_clash_api' \
  -o sing-box ./cmd/sing-box
```

环境坑（都要记牢）：
- **本机 `~/.local/go`（1.27.0）是坏安装**（解压覆盖残留 1.26 文件，`internal/strconv` 新旧文件共存必炸）→ 必须 `GOTOOLCHAIN=go1.27.1`（工具链模块从 goproxy.cn 下载，绕开本机坏 GOROOT；或重装本机 Go）
- **linkname**：go1.26.x 构建 boxdd 需 `-ldflags=-checklinkname=0`；**go1.27.1 起不再需要**
- **sing-tun 新伪版本拉不到时**（goproxy.cn 对 GitHub 的按需抓取偶发 "temporarily unavailable"，通常几小时恢复）：本地临时 `go mod edit -replace=github.com/sagernet/sing-tun=/home/kaishuai/project/singbox-0/sing-tun-freebsd` 构建，**提交前改回伪版本**（CI 在 GitHub runner 上直连无此问题，工作流已设 `GOFLAGS=-mod=mod` 容缺 go.sum）
- 交叉编译即可，**不需要 FreeBSD 机器参与构建**；产物是 FreeBSD 原生静态 ELF（`file` 验证：`ELF 64-bit ... (FreeBSD), statically linked`）
- 版本号命名约定：上游版本 + `f`（如 `1.14.1f`），仅构建时注入，代码零改动

---

## 3. Tun 模式实现（核心，全部在 sing-tun）

### 3.1 文件清单

| 文件 | 内容 |
|---|---|
| `tun_freebsd.go` | `/dev/tun` 克隆设备 + ioctl 配置 + 地址 + FIB 隔离 + AF_ROUTE 路由 |
| `tun_freebsd_gvisor.go` | 自写 gVisor `LinkEndpoint`（`with_gvisor` tag） |
| `monitor_route_bsd.go` | 由 `monitor_darwin.go` `git mv` 而来，tag `darwin \|\| freebsd`，FetchRIB 轮询网络变化 |
| `tun_rules.go` | `autoRouteUseSubRanges` 加 freebsd（darwin 式子网段拆分） |
| `tun.go` | `Inet4/6GatewayAddr()` 加 freebsd 分支（网关 = tun 自身地址） |
| `tun_other.go` / `tun_nondarwin.go` | build tag 排除 freebsd |

### 3.2 设备层要点（全部来自 freebsd-src 验证）

- **打开 `/dev/tun`（克隆设备）**，`TUNGIFNAME` 拿系统分配的名字，`SIOCSIFNAME` 改成配置名
- ioctl 序列：`TUNSIFHEAD`（4 字节 PI 头）→ `TUNSIFMODE`（IFF_BROADCAST\|IFF_MULTICAST）→ `TUNSIFPID`（进程归属）→ `TUNSTRANSIENT`（15+）→ `SIOCSIFMTU` → 改名 → `SIOCAIFADDR`/`SIOCAIFADDR_IN6`（IPv6 带 `IN6_IFF_NODAD`）+ ND6 关自动 link-local
- **写包时把 PI 头规范化为 `00 00 00 AF_INET/AF_INET6`**，否则报 "address family not supported by protocol family"
- **`TUNSIFPID` 的 ioctl 数据被内核忽略**（直接记当前进程 pid）——传什么指针都行
- **同名跳过 rename**：克隆设备抢回旧名（如 `tun0`）后 `tun0→tun0` 的 `SIOCSIFNAME` 必 EEXIST
- gVisor 端点读循环：剥 PI 头 → **目标是 tun 自身地址的包原样写回**（自反射）→ 其余交给协议栈

### 3.3 回环避免：FIB 隔离（本项目的灵魂）

```
FIB 0（主表）:  子网段捕获路由 → tun0     ← 普通流量全部进 tun
FIB 2022:      真实默认网关副本 → em0     ← sing-box 自己的 socket 走这里
辅助:           net.add_addr_allfibs=1    ← tun 地址对所有 FIB 可见
```

- 捕获路由经 AF_ROUTE 路由 socket 写入，`SO_SETFIB=RT_DEFAULT_FIB` 选 FIB 0
- 真默认网关用 `route.FetchRIB` 找到后抄进 FIB 2022（启动时先 `RTM_DELETE` 再 `RTM_ADD`，幂等）
- **出站 socket 用 socket 级 `SO_SETFIB=2022`**（`route/network_setfib_freebsd.go`），挂在 dialer control 链上；**listener 不绑**（入站连接要按到达包的 FIB 0 查找，绑了收不到连接）
- FIB 索引复用 `iproute2_table_index`（默认 2022），**不新增配置字段**

### 3.4 为什么 FreeBSD 不能学 macOS 绑网卡

- FreeBSD **没有** SO_BINDTODEVICE（8.0 引入的半成品，14 移除）；IP_BOUND_IF 是 Apple 私有
- 即使有，FreeBSD 语义是"强制从该口出"而非 macOS 的"环了就丢"——绑 tun 会真环路
- FreeBSD 的原生 per-socket 原语是 **SO_SETFIB**：`ip_output.c` 用 socket 的 `inp_inc.inc_fibnum` 决定路由查找的表
- 为什么不用 netlink：FreeBSD netlink 是 Linux 工具兼容层，**RTM_NEWADDR（配地址）和 RTM_NEWRULE（ip rule）都不实现**，fwmark 不存在——恰恰缺 tun 需要的全部能力

### 3.5 时序坑（真机踩过的最大坑）

`RegisterOutputFIB`（box.New 阶段）先于 tun 设备创建（inbound Start 阶段）生效——中间规则集初始化等环节的 dial 会拿到 `SO_SETFIB=2022`，而 FIB 还没建 → **EINVAL 启动失败**。修复：**FIB 的建立（sysctl 预检 + 网关抄录）在注册时刻就执行**（`RegisterOutputFIB` 里调 `setupOutputFIB`），sing-tun 侧 prepareFIB 保留幂等重复执行。

### 3.6 设备生命周期（内核行为，免费知识）

- **12–14：关闭 fd 从不销毁设备**（`tundtor` 只 down + 清地址）；15 新增 `TUNSTRANSIENT`（`_IOW('t',98,int)`）才有"随 fd 销毁"
- 优雅退出：我们显式 `SIOCIFDESTROY`（先关 fd 再尽力 destroy，忽略错误）
- **kill -9 在 12–14 上设备必然残留**（内核限制，无解），15 上靠 TUNSTRANSIENT 兜底
- tun 创建要求 `PRIV_NET_IFCREATE`（uid 级 root 检查）——FreeBSD **没有 setcap 等价物**，降权只有 VNET jail 一条路

---

## 4. 进程匹配（sing-box `common/process/searcher_freebsd.go`）

数据来源：`net.inet.tcp.pcblist` / `net.inet.udp.pcblist`（xinpgen 头 + xinpcb/xtcpcb 条目）→ 匹配源端口+源 IP → 取 `xso_so`（socket 指针）→ `kern.file`（xfile 条目）里 `xf_data == xso_so` 找 pid → `KERN_PROC_PATHNAME` sysctl 拿可执行路径。

**偏移表（已对照 freebsd-src 的 releng/12.4、13.4、14.2、main 四个分支 + C 编译器逐字段验证，12.4–15.0 布局完全相同）：**

```
xinpgen=64  xinpcb=400  xtcpcb=744  xfile=128
xso_so=16   ie_lport=254   v4 laddr=284   v6 laddr=272(=284-12)   inp_vflag=392
xf_pid=8    xf_data=56
```

验证过程中学到的：
- **手算极易漏 padding**：`in_conninfo`（44 字节）嵌在 xinpcb 里因对齐 uint64 占 48 字节——vflag 实际在 392 而非手算的 388
- **UDP item 基准是 0、TCP 是 8**：UDP 条目是裸 xinpcb（从 `xi_len` 开始），TCP 条目 xtcpcb 的前 8 字节是 `xt_len`（内嵌 xinpcb 在 +8）。bsd-box 的 UDP 统一 +8 是错的
- v4 地址在 union +12（`ia46_pad12`/`__pad[3]` 结构，新旧版本一致），v6 在 union +0
- **只支持 64 位**（amd64/arm64/riscv64）：i386/arm32 指针和 ksize_t 是 4 字节，所有偏移失效
- 加了 30s TTL 结果缓存 + pid→path 永久缓存（bsd-box 是裸扫描）

---

## 5. Redirect（sing-box `common/redir/redir_freebsd.go`）

- **pf 路径**：打开 `/dev/pf`，ioctl `DIOCNATLOOK`（`0xc04c4417`，`pfioc_natlook` 76 字节：4×pf_addr + 4×port + af/proto/direction，已对照 15 内核头文件验证）——用 conn 的本地/远端四元组反查 rdr 之前的原始目标
- direction 用 **PF_OUT**（原始包的走向）；`/dev/pf` 不存在时回退 ipfw 语义（取 localAddr）
- **redirect 入站是 TCP-only**（sing-box 全平台一致的透明代理语义）——UDP DNS 走不了，见配置指南
- **pf 只对入站数据包做 rdr**（`rdr-to` 出站方向到本地地址明确不支持）——本机流量接不了 redirect，这是 pf 与 iptables/nftables 的根本设计差异

---

## 6. sing-box 侧的其他改动

| 改动 | 说明 |
|---|---|
| `route/network_setfib_freebsd.go` | SO_SETFIB 出站挂钩 + FIB 建立（sysctl + 网关抄录） |
| `route/network.go` | `RegisterOutputFIB`/`OutputFIBFunc`（镜像 Linux mark 机制） |
| `adapter/network.go` | 接口方法 |
| `common/dialer/default.go` | dialer 链追加 FIB func（**不加 listener**） |
| `protocol/tun/inbound.go` | FreeBSD+auto_route 时注册 FIB；auto_redirect 非 Linux 明确报错 |
| `route/network_environment_route_bsd.go` | darwin 版 git mv 共享（tag `darwin \|\| freebsd`） |
| `common/redir` / `common/process` | 见上两节 |
| `experimental/libbox` | 可移植的崩溃报告文件加 freebsd tag；OOM/电源报告 stub（官方树原本编译不过 freebsd） |
| `route/network.go` 校验 | `auto_detect_interface`/`default_interface` 放行 FreeBSD（绑定是 no-op，路由表原生决定出口） |

---

## 7. 已知限制 / 待办

- [ ] 默认网关变化后 FIB 2022 副本不刷新（bsd-box 同样如此；monitor 重抄是增强项）
- [ ] kill -9 在 12–14 上残留 tun 设备（内核限制）
- [ ] tun 读写每包一次 syscall（FreeBSD 无 sendmmsg）
- [ ] 无 auto_redirect（pf `route-to` 方案未实现，bsd-box 也没做）
- [ ] 32 位目标不支持（tun 无碍，进程匹配明确拒绝）
- [ ] CI 工作流 `.github/workflows/freebsd.yml` 已写但**未推送**（gh token 缺 workflow scope，需 `gh auth refresh -h github.com -s workflow`）
- [ ] 上游同步：定期 `git fetch upstream && git rebase`，目标是把 sing-tun 的 tun_freebsd 部分提 PR 回 SagerNet

---

## 8. 上游版本跟进维护流程

上游每出新版本（如 1.14.x 小版本或 1.15 大版本）时按此流程走。核心策略：**两个分支都 rebase 上游**，保持线性历史（将来往 SagerNet 提 PR 也干净）。

### 8.1 sing-tun

```bash
cd sing-tun-freebsd
git fetch upstream
# 先看 sing-box 新版 go.mod 里 pin 的 sing-tun 版本，rebase 到那个 tag（而不是 dev 任意提交）：
git rebase v0.9.0-beta.5          # 示例：新 tag
# —— 冲突热点（按概率排序）——
# tun_freebsd.go        Options 结构变化、Tun 接口变化
# tun_freebsd_gvisor.go gvisor 版本升级 → LinkEndpoint/NetworkLinkEndpoint 接口变化
#                       （对齐方法：先看同版本 tun_darwin_gvisor.go 怎么改的）
# tun.go                Inet4/6GatewayAddr 的 switch 分支
# monitor_route_bsd.go  monitor 接口变化（对齐 monitor_darwin.go 的演化）
# tun_rules.go          常量处
# 解完冲突后编译三平台：
GOTOOLCHAIN=go1.26.7 GOPROXY=https://goproxy.cn GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOTOOLCHAIN=go1.26.7 GOPROXY=https://goproxy.cn GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOTOOLCHAIN=go1.26.7 GOPROXY=https://goproxy.cn GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build ./...
git push origin freebsd
# 手算新伪版本（见第 1 节）供 sing-box 更新
```

### 8.2 sing-box

```bash
cd sing-box-freebsd
git fetch upstream
git rebase upstream/testing
# —— 冲突热点 ——
# go.mod                      replace 行（保留下面的 fork 伪版本，更新之）
# route/network.go            RegisterOutputFIB/OutputFIBFunc 区域（NetworkManager 结构经常动）
# adapter/network.go          NetworkManager 接口方法
# common/dialer/default.go    dialer control 链（上游常调整 Append 顺序）
# protocol/tun/inbound.go     注册 FIB 的代码块（注意保留 auto_redirect 的非 Linux gate）
# common/process/searcher.go  Searcher 接口若有变化
# experimental/libbox/*       上游新增平台文件时补 freebsd tag / stub
# 更新 replace 到 sing-tun 新伪版本 → go mod download → 编译三平台 → 真机冒烟 → push
```

### 8.3 每次大版本必须复查的点

1. **新平台抽象**：上游 diff 里出现新的 `*_linux.go`/`*_darwin.go`/`*_windows.go` 文件或新 stub 时，判断 FreeBSD 该共享（改 tag）还是新增实现（如 1.14 的 network_environment）
2. **go.mod 的 gvisor/sing 版本**：跟着 sing-tun 的依赖走，接口变化以 darwin 版为参照物
3. **FreeBSD 大版本（16）**：进程匹配的偏移表必须重新验证——用 `/tmp/offsetcheck/offsets.c`（C 编译器布局计算器，建议提交进仓库 docs 目录）对照新 header；tun ioctl 数字对照 `sys/net/if_tun.h`
4. **版本号**：`1.15f`（上游版本 + f），构建时 ldflags 注入
5. **真机冒烟清单**（每次发布前过一遍）：启动 → `sysctl net.fibs` → `netstat -rn -F 0/-F 2022` → curl 公网 + CPU idle（回环检查）→ kill -9 后设备/路由清理 → （redirect 模式）pf 计数 + DIOCNATLOOK 日志

### 8.4 长期减负路线

- **sing-tun 的 tun_freebsd 部分提 PR 回 SagerNet**（他们接受贡献）：成功后 sing-tun fork 可退役，sing-box 只维护平台小文件
- **CI 推送后**（workflow scope 补上）：每次 release 打 tag 自动构建，手动只剩 rebase 和真机冒烟
- **不要学 bsd-box 的补丁式维护**（patch 套 release）：它的停更就是因为补丁基线漂移成本失控

---

## 9. 真机测试记录（FreeBSD 15.1-RELEASE VM，libvirt 网络）

| 验证点 | 结果 |
|---|---|
| FIB 建立 | `net.fibs=2023`、`net.add_addr_allfibs=1` 运行时 sysctl 写入成功（无需 loader.conf 重启） |
| 路由表 | FIB 0：子网段捕获路由→tun0；FIB 2022：`default 192.168.122.1 UGS em0` + 直连路由镜像 |
| tun 设备 | UP、双栈地址、`nd6 NO_DAD`（setND6 生效）、`Opened by PID`（TUNSIFPID 生效） |
| 回环 | `curl www.google.com` 得真实 302；CPU 98% idle——无环实锤 |
| DNS/fakeip | `drill @172.19.0.2` 与 `@223.5.5.5`（公网 DNS 被捕获劫持）均返回 `128.32.0.2` fakeip |
| redirect | 局域网客户机流量经 rdr → 日志显示**原始目标 IP**（DIOCNATLOOK 反查成功）、真实客户端源地址 |
| UDP DNS | redirect 入站收不了（TCP-only）→ 用 dns 入站 + `rdr udp 53` 解决 |
| 教训 | 盒子自身 curl 永远测不到 rdr（出站不支持）；`dig -tcp` 是错的（被解析成 `-t cp`），正确写法 `dig +tcp`；pf 规则漏 `pass all` 会全网断（pf 默认丢弃未匹配流量） |
