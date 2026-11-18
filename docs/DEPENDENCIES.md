# 依赖收敛与复现（Dependency Consolidation）

本项目的 mihomo 内核为 QNX/BB10 做过改造。历史上这些改造散落在「兄弟目录 fork + 绝对路径 `replace`」里，不可复现、不可移植。现已全部收敛为**「定版上游 + 提交补丁 + 重建脚本」**，任何一台机器都能从仓库内的少量补丁重建出完全相同的内核源码与二进制。

## 一张图看懂

```
mihomo/UPSTREAM            ← 定版清单（唯一真相源）
mihomo/patches/            ← 提交进 git 的补丁（全部改动，无绝对路径）
  ├─ mihomo.patch          ← 内核主模块：QNX DNS packetconn 分流 + go.mod/go.sum 增量（5 文件 / 89 行）
  ├─ wireguard-go.patch    ┐
  ├─ bbolt.patch           │
  ├─ sing.patch            │ 7 个依赖的 QNX 移植
  ├─ sing-tun.patch        │ （各加 *_qnx.go + 一行 build-tag）
  ├─ logrus.patch          │
  ├─ uio.patch             │
  └─ dhcp.patch            ┘
scripts/fetch-mihomo.sh    ← 克隆定版 mihomo 核心 + 应用 mihomo.patch → mihomo/upstream/（gitignore）
scripts/apply-patches.sh   ← 拉 7 个定版上游 + 应用补丁 → mihomo/overrides-upstream/（gitignore）
scripts/verify-overrides.sh← 逐字节核对重建结果 vs 参考树
scripts/build-core.sh      ← 缺失时自动调上面两个脚本；go.work 把 7 个 override 指向重建树后编译
```

## 四类依赖，逐一收口

| 类别 | 项 | 复现方式 | 状态 |
|---|---|---|---|
| **内核主模块** | `github.com/metacubex/mihomo` | 定版 commit `0455d0d2`（Alpha 分支，2026-11-11）+ `mihomo.patch` | ✅ 自包含 |
| **QNX 依赖 override（7）** | wireguard-go / bbolt / sing / sing-tun / logrus / uio / dhcp | 各自定版上游 + `patches/<name>.patch`，重建后与原 fork **逐字节一致** | ✅ |
| **已发布的 replace（2）** | `google.golang.org/protobuf`→metacubex/protobuf-go；`golang.org/x/sys`→XIAZY/go-sys-qnx | 定版发布版本，`mihomo.patch` 写进 go.mod，无需本地补丁 | ✅ |
| **传递依赖（~130）** | go.mod require 全集 | `go.mod` + `go.sum`（334 行哈希）定版；`GOPROXY` + modcache 解析 | ✅ 定版 |

## 如何从零重建

```sh
# 仅凭 mihomo/UPSTREAM + mihomo/patches/ + scripts/：
scripts/build-core.sh      # 自动 fetch-mihomo + apply-patches + 编译
scripts/verify-overrides.sh  # 可选：核对 override 与参考树逐字节一致
```

`build-core.sh` 的 `go.work`（gitignore，含绝对路径）把 7 个 override 的 `replace` 指向 `mihomo/overrides-upstream/`，**优先级高于 go.mod**，因此 `mihomo.patch` 里**不含**任何绝对路径，可移植。

## 仍属外部输入（非源码依赖，合理外置）

- **QNX Go 交叉工具链** `toolchain/extracted/go`（go1.27.2 QNX 版，预编译大件）。
- **modcache** `toolchain/modcache`（传递依赖的本地缓存）。它由 `go.sum` 定版，可用 `GOPROXY` + `go mod download` 从公网重建；离线构建时 `apply-patches.sh` 也默认从它取上游原件（`BB10_UPSTREAM_MODCACHE`）。

## 验证记录（2026-11-16）

- 7 个 override 重建树 vs 原 fork：**逐字节一致**（`verify-overrides.sh` 全 OK）。
- `mihomo.patch` 重放到纯净上游 checkout：**逐字节重现**内核源码树。
- 从零（仅补丁+定版）全链重建内核二进制：`sha256 f3b88ff0…`，与旧「兄弟树」构建**机器码一致**（哈希差异仅来自 `-trimpath` 对本地目录 replace 无法规范化、把 override 绝对路径嵌进 pclntab；源码与逻辑不变）。
- 设备上已装 0.1.0.22（旧构建 `e7f277f3…`）与此功能等价，无需重装。

## 边角料

- 内核 `constant/version.go` 里 `Version = "1.10.0"` 为上游既有值（非本项目版本），BB10 App 自己的版本是 bar 的 `0.1.0.N`，勿混淆。
- 研究工作区 `~/bb10-passport/vpn-research/mihomo` 残留过 4 个非源码文件（`*.tmp`、`*.research-original`），已确认不属移植代码、**未纳入** `mihomo.patch`。
