# BB10 QNX TUN 数据帧离线研究原型

这是独立的包编解码及模拟设备实验，**不是可用的 VPN 或 QNX TUN 实现**，不打开任何设备，不调用 ioctl，不设置接口、路由、DNS，也没有集成进 Mihomo 或手机应用。两个格式都只是待真机确定的候选：原始 IP；或 4 字节网络序 AF 前缀 + 原始 IP。不能根据 BSD 的行为推定 BB10 驱动一定相同。

- BB10 10.3.1 SDK 头文件声明 AF_INET=2、AF_INET6=24；不能使用 Linux 的 AF_INET6=10 或其他 BSD 的值。此数值为编译时线索，10.3.3 驱动行为未测。
- `Device` 接受调用者已经提供的、具备包边界的 ReadWriteCloser；已链接实际 sing-tun 接口及其 mipstack 协议栈，并使用内存模拟设备测试，但不能称为完整 TUN 后端。
- 校验 IPv4 IHL/总长、IPv6 负载长度与 AF 一致性；暂不支持 IPv6 jumbogram，也不验证校验和。短写立即失败，不重试。读取截断语义、零包与关闭/取消行为取决于真实驱动，仍待验证。
- Linux 主机单测、模拟设备测试、10 秒 fuzz 通过。实际 sing-tun mipstack 与内存模拟设备连接，IPv4 TCP（三次握手及双向数据）和 UDP（模拟回调及响应）在两种假设帧格式下均通过；Linux 重复 20 次，手机 apps 用户 QNX/ARM 模拟测试重复 5 次通过。没有真实 TUN、真实 App 流量或 Mihomo 节点出站；此 UDP 测试不证明 QNX 网络 UDP 或节点 UDP 可用。手机没有新增 tun 接口。

运行：
```
go test -v -cover ./...
go test -count=20 ./integration
go test -run '^$' -fuzz '^FuzzDecode$' -fuzztime=10s -parallel=2
```

## backend/（2026-11-15）

默认禁用的 TUN 后端雏形，依据对手机 `io-pkt-v6-hc` 的静态反汇编结论编写（见 `../TUN-DRIVER-STATIC-20261115.md`），**未在真机运行**。默认构建中 `OpenReal` 恒返回 `ErrRealDeviceDisabled`；真实系统调用只在 `GOOS=qnx -tags bb10_real_tun` 下编译，并要求 `Config.AcknowledgeRealInterface`。不配地址、路由、DNS。

```
go test -timeout 120s -count=5 -cover ./...
GOOS=qnx GOARCH=arm GOARM=7 CGO_ENABLED=0 go vet -tags bb10_real_tun ./...
```

## Mihomo 接入与真机实验（2026-11-15）

`backend/policy.go`（选项审核、`BB10_TUN_EXPERIMENT=1` 门控）、`../overrides/sing-tun/tun_qnx.go`（带 tag 的 `New()`）、`probe/`、`probe2/`（真机探测程序）、`e2e-client/`（引流实验客户端）。真机结果见 `../TUN-DRIVER-STATIC-20261115.md` 第 6–8 节。`build/` 下的实验核心不是代理 App 的核心。

## gen-node-config

`gen-node-config SOURCE.yaml INDEX OUT.yaml` copies one plugin-less SS node
into a TUN steering test config. OUT contains credentials: keep it private and
delete it (and any Mihomo log, which may name the node host) after the test.

## helper / tun-helper (root/app split, experimental)

`helper` holds the route logic (host-testable with a fake command runner);
`tun-helper serve UID SOCKET` is the root side and `tun-helper run SOCKET WAIT
-- CMD...` runs the core under it as the app uid. The helper is started by
hand over root SSH; nothing installs it. `device-scripts/` has the scripts
used on the phone. See `../TUN-DRIVER-STATIC-20261115.md` section 10.
