# bb10-mihomo

A native **[mihomo](https://github.com/MetaCubeX/mihomo) (Clash.Meta) proxy / VPN client for BlackBerry 10** (BlackBerry Passport, BB10 10.3.3). This is a mono-repo: the Qt/Cascades app, a custom QNX TUN backend for the core, the root helper, the config tool, and the pinned-upstream-plus-patches that reproduce the core.

> ⚠️ **Experimental research project.** Not an official mihomo distribution. Built to study "an on-device core carrying whole-phone traffic over TUN" on a *rooted* BB10 10.3.3 device. Use only on hardware you own and are authorized to test.

---

## What it does

- **Multi-protocol.** Imports a standard mihomo YAML subscription and accepts **SS, SSR, VMess, VLESS, Trojan, Snell, TUIC, and Hysteria2 (QUIC)** nodes. Not supported: WireGuard, nodes needing a `plugin`/`dialer-proxy`. REALITY does not work under TUN (an environment limitation, by design).
- **Per-node latency test.** "Test all" probes every node through the core's control API; each result appears the instant it lands (no waiting for the whole batch). Sort nodes by latency.
- **Hot node switch.** Switch the active node live over the control API without restarting the core.
- **HTTPS connectivity probe.** One real, certificate-validated HTTPS request through the local mixed-port proxy to confirm the path actually works.
- **VPN mode (global).** Routes **whole-phone** traffic through the selected node over TUN (requires root). Closing the app or the core rolls the routes back and restores networking automatically.
- **Split routing.** China-mainland domains/IPs go direct, everything else through the node, using a bundled offline GeoIP + GeoSite. One tap to switch to global.
- **Egress IP display.** Shows the outbound IP seen when reaching several sites, so direct vs. proxied routing is visible at a glance.
- **Light / dark theme.** Toggle in the Control tab; the choice is persisted and applied at next launch.
- **Four tabs:** Control (控制), Nodes (节点), Connections (连接), Rules (规则).

---

## Repository layout

| Path | Contents |
|---|---|
| `app/` | Qt 4.8 / BlackBerry Cascades app — C++ UI controller (`src/`) + QML (`assets/main.qml`). |
| `core/` | Custom core workspace: QNX TUN backend (`backend/`), routing logic (`helper/`), root helper (`tun-helper/`), packet codec (`device.go` / `packet.go`). Go. |
| `tools/configtool/` | Go: generates core configs from a subscription (proxy/VPN, global/split) + diagnostic subcommands. Has unit tests. |
| `mihomo/` | **Pinned upstream + QNX patches** that reproduce the core (`UPSTREAM` manifest + `patches/*.patch`). The full GPL source tree is *not* vendored. |
| `experiments/` | **Non-production** debug scaffolding (probe / appsim / e2e / integration / gen-node-config / device-scripts). |
| `scripts/` | Build & fetch scripts (`build.sh`, `build-core.sh`, `fetch-geodata.sh`, `fetch-mihomo.sh`, `apply-patches.sh`, `verify-overrides.sh`, `make-bootstrap.py`). |
| `docs/` | `DEPENDENCIES.md` (dependency consolidation / reproducibility). |

---

## Architecture

```
┌──────────────────────────────────────────────┐
│  Qt 4.8 / Cascades UI  (app/)                  │
│   proxycontroller.*  — start/stop core,        │
│                        control API, latency    │
│                        tests, node switch,     │
│                        VPN privilege escalation │
│   clashrest.*  — REST client (/rules, /delay,  │
│                  /connections, node select)    │
│   clashstream.* — hand-rolled WebSocket        │
│                   (/traffic); Qt 4.8 has no    │
│                   QWebSocket                    │
└───────────────┬──────────────────────────────┘
                │ launch / pipe
    ┌───────────┼───────────────┬───────────────┐
    ▼           ▼               ▼               ▼
 mihomo     configtool (Go)   tun-helper     /bin/__root
 core       config + probe    (root helper)  (AutoRoot
 (bundled)  prepare/ctldelay  tun + routes    setuid-root
                                              wrapper)
```

- **Core control API** binds to `127.0.0.1` only, with a **per-launch random secret**, after geodata finishes loading (≈1.5 s after start). Nothing about it is logged.
- **Root** comes from the device's AutoRoot (`/bin/__root`, ready at boot). App use and VPN escalation need **no external SSH**.

---

## Build

The cross toolchain is **not** shipped in this repo. You need:

1. A **BB10 C++ toolchain** — provided as a Docker image, pinned by digest in `scripts/build.sh` (`ghcr.io/xiazy/blackberry10-toolchain@sha256:…`).
2. A **QNX Go toolchain** (`GOOS=qnx GOARCH=arm GOARM=7`) for the core and helper.

```sh
# 1) Build the core + root helper (fetches pinned mihomo and applies patches if missing).
scripts/build-core.sh

# 2) Fetch the GeoIP / GeoSite split-routing data (public, ~12 MB).
scripts/fetch-geodata.sh

# 3) Build configtool + the C++ app and package the .bar.
scripts/build.sh
# Output: app/build/bb10-proxy-research.bar

# configtool unit tests run standalone:
cd tools/configtool && go test ./...
```

The core is fully reproducible from the small patch set in `mihomo/` — see **[`docs/DEPENDENCIES.md`](docs/DEPENDENCIES.md)** for the pinned-upstream + patches + rebuild story (rebuilds verified byte-for-byte against the original fork).

---

## Install

Install the `.bar` with the normal BB10 sideload flow. For in-place upgrades on a rooted research device, `scripts/make-bootstrap.py` wraps the `.bar` into a `{MANIFEST, pkg.bar}` ZIP that the AutoRoot installer (`/var/rootdata/installer`) applies. **Day-to-day use — opening the app, enabling VPN, split routing, latency tests — needs no SSH.**

---

## Security

- **Never commit node subscriptions or credentials.** Subscription YAML, node addresses, passwords, and keys are runtime data, not source. `.gitignore` excludes `source.yaml`, `active.yaml`, `import.yaml`, `data/proxy/`, and `*.subscription.yaml`.
- The core control-API secret is random per launch, bound to loopback, and never logged.
- `app/assets/cacert.pem` is the public Mozilla CA bundle, used by the core for HTTPS certificate validation.

---

## For agents / contributors

A fast orientation for anyone (human or AI) changing this code. **Read this before editing.**

### Platform constraints (these cause real, non-obvious build/crash failures)

- **Qt 4.8 / C++98.** No C++11. A **local type cannot be a template argument** (e.g. to `std::stable_sort`) — use file-scope structs in an anonymous namespace.
- **No `QWebSocket`** → the WebSocket client in `app/src/clashstream.*` is hand-rolled (RFC 6455 framing over `QTcpSocket`, loopback, no TLS). **No `QUrlQuery`** → use `QUrl::addQueryItem`.
- **Link rule (`__dso_handle`).** A global object **with a constructor** breaks the BB10 link. Use function-local computation or POD globals only.
- **QML parse gotcha (causes a silent crash).** An inline-object property value — `prop: SomeObject { }` — must **not** be followed by `; nextProp` on the same line. Keep the next property on its own line.
- **QML runtime errors** (binding failures, unknown properties) are routed to `data/proxy/qml.log` on-device (see `app/src/main.cpp`), since they otherwise vanish without a desktop.

### UI theming

- Stock controls are recolored wholesale by the Cascades **visual style** (`ThemeSupport::setVisualStyle`, driven from `ProxyController`). Custom-colored spots (node cards, latency colors, accent/gray text) bind to **`proxy.darkTheme`** in QML and flip alongside it. The choice persists to `data/proxy/theme.txt`.

### Control API

- mihomo endpoints used: `/traffic` (WS, 1 Hz up/down), `/connections` (snapshot), `/rules` (`[{type,payload,proxy}]`), `/delay?...` (per-node latency), plus node select. Auth is `?token=<secret>` + `Authorization: Bearer`. The controller binds **after** geodata load, so the first connect attempts are expected to be refused and retried.
- `configtool` forces `udp: false` on every proxy. That disables only the proxy's **UDP relay** for client traffic; it does **not** disable a TUIC/Hysteria2 node's own QUIC transport to its server (QUIC works on QNX via quic-go's portable UDP path).

### Build / verify loop

1. Bump `<buildId>` in `app/bar-descriptor.xml` (`<versionNumber>0.1.0</versionNumber><buildId>NN</buildId>`).
2. `scripts/build.sh` (Docker BB10 cross-compile; host-clang diagnostics against the Qt headers are noise — the Docker build is authoritative).
3. Verify: `unzip -p app/build/bb10-proxy-research.bar META-INF/MANIFEST.MF | grep Package-Version`.
4. `python3 scripts/make-bootstrap.py <bar> 0.1.0.NN` for an installable bootstrap ZIP.

### Where to make changes

- UI layout / tabs / colors → `app/assets/main.qml`.
- App logic (core lifecycle, control API, latency, VPN) → `app/src/proxycontroller.{h,cpp}`.
- Config generation / protocol allowlist → `tools/configtool/main.go` (`supported()` gates node types; tests in `main_test.go`).
- Core / TUN / routing → `core/`.

---

## Credits & license

- Core: **[mihomo / Clash.Meta](https://github.com/MetaCubeX/mihomo)** (GPL-3.0).
- Routing data: **[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat)**.
- Root: AutoRoot (BB10).

This repository contains the glue code around the above (C++ UI, Go config tool, packaging scripts). It is licensed under **GPL-3.0** (see [`LICENSE`](LICENSE)), matching the bundled mihomo core. Release binaries (`.bar`) include the GPL-3.0 mihomo core; the corresponding source is this repository plus the pinned-upstream/patches in `mihomo/` (see `docs/DEPENDENCIES.md`).
