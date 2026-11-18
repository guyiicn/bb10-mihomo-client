# bb10-mihomo

在 **BlackBerry 10（BlackBerry Passport）** 上以原生应用形式运行 [mihomo](https://github.com/MetaCubeX/mihomo)（Clash.Meta）内核的代理 / VPN 客户端。

> ⚠️ **实验性研究项目。** 这不是 mihomo 的官方发行版，是为在已 Root 的 BB10 10.3.3 设备上研究「本机内核经 TUN 带全机流量」而做的定制版。功能可用，稳定性仍在完善中。请仅在你拥有、且已授权测试的设备上使用。

---

## 功能

- **多协议**：SS / VMess / VLESS / Trojan（TCP 系）。导入标准 mihomo YAML 订阅即可。
- **逐节点测速**：经内核控制 API（external-controller）对每个节点做真实 HTTPS 延迟测试。
- **不重启切节点**：运行中经控制 API 即时切换当前节点，无需重启内核。
- **HTTPS 连接测试**：经本机 mixed-port 代理做一次真实、带证书校验的 HTTPS 请求，验证链路是否真的通。
- **VPN 模式（全局）**：经 TUN 让**全机应用**流量走所选节点（需 Root）。关闭或退出自动恢复网络。
- **路由分流**：中国大陆域名 / IP 直连，其余走节点（内置 GeoIP + GeoSite，全离线）。可一键切「全局」。
- **出口 IP 显示**：显示访问 baidu / google / claude 时**我方对外的出口 IP**——直连站显示本机运营商 IP，走节点的站显示节点出口 IP，直观验证分流。

---

## 架构

```
┌─────────────────────────────────────────────┐
│  Qt / Cascades C++ 界面 (src/)               │
│   proxycontroller.*  — 启停内核、控制 API、    │
│                        测速、切节点、VPN 提权   │
└───────────────┬─────────────────────────────┘
                │ 启动 / 管道
    ┌───────────┼───────────────┬──────────────┐
    ▼           ▼               ▼              ▼
 mihomo     configtool (Go)   tun-helper    /bin/__root
 内核       生成配置+诊断       (Root 助手)    (AutoRoot
 (捆绑)     prepare/probe/…    管理 tun/路由   setuid-root)
```

- **界面**：Qt 4.8 / BlackBerry Cascades（C++）。
- **内核**：捆绑的 mihomo，带 QNX TUN 后端（`bb10_real_tun` 构建标签 + `BB10_TUN_EXPERIMENT=1` 才启用；代理模式两者皆无，行为与上游一致）。
- **configtool**（Go，交叉编译进包）：从订阅 YAML 生成内核配置（代理 / VPN、全局 / 分流），并提供 `probe` / `httpsget` / `ctldelay` 等诊断子命令。
- **tun-helper**（Go，Root 助手）：以 Root 管理 TUN 设备与路由。App 经 AutoRoot 的 **`/bin/__root`**（setuid-root 包装器）提权 exec 它；管道 EOF 即回滚路由、归还设备。
- **Root 来源**：设备上的 AutoRoot（`/bin/__root`，开机自动就绪）。**App 运行与 VPN 提权均不依赖任何外部 SSH 连接。**

---

## 运行要求

- BlackBerry Passport，BB10 **10.3.3**，已通过 **AutoRoot** 获得 Root（VPN 模式必需；纯代理模式不需要 Root）。
- 标准 mihomo YAML 订阅（SS / VMess / VLESS / Trojan 节点）。

---

## 构建

构建管线依赖一套 BB10 交叉工具链，**不随本仓库分发**：

1. **QNX 版 Go 工具链**（`GOOS=qnx GOARCH=arm GOARM=7`）。
2. **C++ 工具链 Docker 镜像**：`ghcr.io/xiazy/blackberry10-toolchain`（见 `build.sh` 中 pin 的 sha256）。
3. **内核与 Root 助手**：`mihomo` 与 `tun-helper` 由 `build-core.sh` 从一个定制 mihomo 工作区（qnx-tun-lab，含 QNX TUN 后端与 DNS 修复）构建，产物放在 `build/bin/`。`build.sh` 若缺 `build/bin/mihomo` 会直接报错。

工具链根目录由环境变量 `BB10_PROXY_RESEARCH` 指定（默认 `$HOME/bb10-passport/vpn-research`）。

```sh
# 1) 构建内核 + Root 助手（需要定制 mihomo 工作区）
./build-core.sh

# 2) 下载分流用的 GeoIP / GeoSite（公开数据，约 12 MB）
./scripts/fetch-geodata.sh

# 3) 构建 configtool + C++ 应用并打包 .bar
./build.sh
# 产物：build/bb10-proxy-research.bar
```

`configtool` 的单元测试可独立跑：

```sh
cd configtool && go test ./...
```

---

## 安装

本应用为**侧载**研究包，经 AutoRoot 的安装器原地升级（`{MANIFEST, pkg.bar}` ZIP → `/var/rootdata/installer`）。普通用户按常规 BB10 侧载流程安装 `.bar` 即可。安装后日常使用（开 App、开 VPN、分流、测速）**完全无需 SSH**。

---

## 目录

```
src/                C++ 界面与控制器（main.cpp, proxycontroller.*）
assets/             main.qml, icon.png, cacert.pem（geodata 由脚本拉取）
configtool/         Go：配置生成 + 诊断子命令（含单元测试）
bar-descriptor.xml  BAR 打包描述
build.sh            configtool + C++ 打包
build-core.sh       mihomo + tun-helper（需定制工作区）
scripts/            fetch-geodata.sh
```

---

## 安全说明

- **切勿提交任何节点订阅 / 凭据。** 订阅 YAML、节点地址、密码、密钥均为敏感数据，不属于本仓库。
- 内核控制 API 的 secret 每次启动随机生成，仅绑定 `127.0.0.1`，不落日志。
- `assets/cacert.pem` 为公开的 Mozilla CA 根证书集，供内核做 HTTPS 证书校验。
- 回显出口 IP 用 http（App 的 Qt SSL 使用 BB10 过旧的系统 CA，无法校验现代证书；出口 IP 本身非敏感数据）。

---

## 致谢与许可

- 内核：[mihomo / Clash.Meta](https://github.com/MetaCubeX/mihomo)（GPL-3.0）。
- 分流数据：[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat)。
- Root：AutoRoot（BB10）。

本仓库仅含上述应用的胶水代码（C++ 界面、Go 配置工具、打包脚本）。捆绑的 mihomo 为 GPL-3.0，分发时请遵循其许可。
