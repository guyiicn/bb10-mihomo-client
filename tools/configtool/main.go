// Configtool prepares a mihomo config from a user subscription for this
// prototype. It passes each supported proxy through verbatim (preserving every
// protocol-specific field) and only overrides the display name and disables UDP.
// Supported types are the TCP-family protocols that work without an external
// plugin binary or QNX UDP/QUIC datapath: ss, vmess, vless, trojan. QUIC/UDP
// protocols (hysteria2/tuic/wireguard) and plugin/relay chains are dropped.
package main

import (
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "os"
 "strconv"
 "strings"
 "time"
 "unicode"

 "gopkg.in/yaml.v3"
)

const maxBytes = 4 << 20

// rawProxy keeps the proxy mapping exactly as written in the source so every
// protocol-specific field (uuid, tls, ws-opts, reality-opts, flow, sni, ...)
// survives into the emitted config untouched.
type rawProxy = map[string]any

type source struct {
 Proxies []rawProxy `yaml:"proxies"`
}
type item struct {
 Index int    `json:"index"`
 Name  string `json:"name"`
}

// supported reports whether a proxy type is one we pass through. The bundled mihomo
// has every adapter compiled in (verified by symbol: ssr/snell/tuic/hysteria2/… all
// present), and quic-go is built with its portable sys_conn_no_oob.go path, so QUIC
// transports have a real datapath on QNX. We therefore pass:
//   - TCP-family: ss, ssr, vmess, vless, trojan, snell (no external plugin needed;
//     each proxy is forced udp:false so only its TCP relay is used).
//   - QUIC-family: tuic, hysteria2 — these dial the server over QUIC/UDP directly
//     (not via TUN), so udp:false does not disable them. Their real reachability on
//     QNX is validated per node by the control-API /delay test (5s-bounded), which
//     actually completes the QUIC handshake, so a QUIC path that does not work on
//     this phone simply shows as 超时 rather than silently pretending to connect.
// Nodes needing an external plugin binary or a dialer chain are still dropped in
// accept(); mihomo validates each passed proxy's own fields at config-load time.
func supported(t string) bool {
 switch t {
 case "ss", "ssr", "vmess", "vless", "trojan", "snell":
  return true
 case "tuic", "hysteria2":
  return true
 }
 return false
}

func toInt(v any) (int, bool) {
 switch x := v.(type) {
 case int:
  return x, true
 case int64:
  return int(x), true
 case float64:
  return int(x), true
 case string:
  n, e := strconv.Atoi(strings.TrimSpace(x))
  if e != nil {
   return 0, false
  }
  return n, true
 }
 return 0, false
}

func str(v any) string {
 s, _ := v.(string)
 return s
}

func clean(s string, n int) string {
 var b strings.Builder
 for _, r := range s {
  if b.Len() >= n {
   break
  }
  if unicode.IsControl(r) {
   b.WriteRune(' ')
  } else {
   b.WriteRune(r)
  }
 }
 return b.String()
}

// pname is the stable, unique proxy name for node i, shared by `list` (what the
// UI shows and selects by) and the emitted config (what mihomo and the control
// API key on). The index prefix guarantees uniqueness even with duplicate or
// empty display names.
func pname(name string, i int) string {
 s := clean(name, 80)
 if s == "" {
  s = fmt.Sprintf("节点%d", i+1)
 }
 return fmt.Sprintf("%d·%s", i+1, s)
}

// accept validates the minimum we require of any pass-through proxy. Everything
// beyond this (cipher names, uuid format, tls options) is left for mihomo to
// validate at load time.
func accept(p rawProxy) bool {
 if !supported(str(p["type"])) {
  return false
 }
 server := str(p["server"])
 if server == "" || len(server) > 253 || strings.ContainsAny(server, "\r\n\x00") {
  return false
 }
 if port, ok := toInt(p["port"]); !ok || port < 1 || port > 65535 {
  return false
 }
 // Drop proxies needing an external plugin binary (ss obfs/v2ray-plugin) or a
 // dialer chain; neither is available in this prototype.
 if str(p["plugin"]) != "" || str(p["dialer-proxy"]) != "" {
  return false
 }
 return true
}

func load(path string) ([]rawProxy, error) {
 f, e := os.Open(path)
 if e != nil {
  return nil, errors.New("无法读取配置文件")
 }
 defer f.Close()
 b, e := io.ReadAll(io.LimitReader(f, maxBytes+1))
 if e != nil || len(b) > maxBytes {
  return nil, errors.New("配置文件过大或读取失败")
 }
 var s source
 if yaml.Unmarshal(b, &s) != nil {
  return nil, errors.New("配置格式不正确")
 }
 var out []rawProxy
 for _, v := range s.Proxies {
  if !accept(v) {
   continue
  }
  out = append(out, v)
  if len(out) > 1000 {
   return nil, errors.New("节点数量过多")
  }
 }
 if len(out) == 0 {
  return nil, errors.New("没有支持的节点（支持 SS/SSR/VMess/VLESS/Trojan/Snell 及 TUIC/Hysteria2）")
 }
 return out, nil
}

func main() { os.Exit(run(os.Args, os.Stdout)) }

// run dispatches one subcommand and returns the process exit code. It is split
// out of main so tests can drive it in-process without os.Exit terminating
// them. Errors go to stderr; only "list" writes to out.
func run(argv []string, out io.Writer) int {
 if len(argv) >= 2 && argv[1] == "ctlget" {
  // ctlget PORT SECRET PATH — raw GET against the running controller (diagnostic).
  if len(argv) != 5 {
   fmt.Fprintln(os.Stderr, "用法: ctlget PORT SECRET PATH")
   return 2
  }
  req, e := http.NewRequest("GET", "http://127.0.0.1:"+argv[2]+argv[4], nil)
  if e != nil {
   fmt.Fprintln(os.Stderr, "bad request")
   return 1
  }
  req.Header.Set("Authorization", "Bearer "+argv[3])
  gc := http.Client{Timeout: 15 * time.Second}
  r, e := gc.Do(req)
  if e != nil {
   fmt.Fprintf(out, "ERR %v\n", e)
   return 1
  }
  b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
  r.Body.Close()
  fmt.Fprintf(out, "HTTP %d: %s\n", r.StatusCode, string(b))
  return 0
 }
 if len(argv) >= 2 && argv[1] == "ctldelay" {
  // ctldelay PORT SECRET NAME URL — query the running core's controller for a
  // node's delay over loopback (diagnostic; SSH port-forwarding to the device
  // loopback is blocked, so this runs on-device). Prints "HTTP <code>: <body>".
  if len(argv) != 6 {
   fmt.Fprintln(os.Stderr, "用法: ctldelay PORT SECRET NAME URL")
   return 2
  }
  api := fmt.Sprintf("http://127.0.0.1:%s/proxies/%s/delay?timeout=5000&url=%s",
   argv[2], url.PathEscape(argv[4]), url.QueryEscape(argv[5]))
  req, e := http.NewRequest("GET", api, nil)
  if e != nil {
   fmt.Fprintln(os.Stderr, "bad request")
   return 1
  }
  req.Header.Set("Authorization", "Bearer "+argv[3])
  c := http.Client{Timeout: 15 * time.Second}
  r, e := c.Do(req)
  if e != nil {
   fmt.Fprintf(out, "ERR %v\n", e)
   return 1
  }
  b, _ := io.ReadAll(io.LimitReader(r.Body, 2048))
  r.Body.Close()
  fmt.Fprintf(out, "HTTP %d: %s\n", r.StatusCode, string(b))
  return 0
 }
 if len(argv) == 2 && argv[1] == "probe" {
  p, _ := url.Parse("http://127.0.0.1:17890")
  c := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(p), DisableKeepAlives: true}, Timeout: 20 * time.Second}
  // gstatic's generate_204 is the standard reachability target: a tiny TLS GET
  // that returns 204. example.com proved flaky through some nodes; gstatic is the
  // same host the delay test uses, so the two tests agree. Accept any 2xx (204).
  r, e := c.Get("https://www.gstatic.com/generate_204")
  if e != nil {
   fmt.Fprintln(os.Stderr, "连接测试失败")
   return 1
  }
  io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
  r.Body.Close()
  if r.StatusCode < 200 || r.StatusCode >= 300 {
   fmt.Fprintln(os.Stderr, "连接测试失败")
   return 1
  }
  fmt.Fprintln(out, "OK")
  return 0
 }
 if len(argv) == 3 && argv[1] == "httpsget" {
  // httpsget URL — a real CA-verified HTTP(S) GET through the running core's
  // mixed-port proxy (127.0.0.1:17890), used to tell genuine node reachability
  // apart from the control API's /delay (whose HTTPS probe fails cert
  // verification in the core and so reports 503 for every https target). Prints
  // the real status, byte count and content-type, or the transport error.
  p, _ := url.Parse("http://127.0.0.1:17890")
  c := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(p), DisableKeepAlives: true}, Timeout: 20 * time.Second}
  r, e := c.Get(argv[2])
  if e != nil {
   fmt.Fprintf(out, "ERR %v\n", e)
   return 1
  }
  b, _ := io.ReadAll(io.LimitReader(r.Body, 512))
  r.Body.Close()
  snip := strings.TrimSpace(string(b))
  if len(snip) > 100 {
   snip = snip[:100]
  }
  fmt.Fprintf(out, "HTTP %d, %d+ bytes, ct=%q, body=%q\n", r.StatusCode, len(b), r.Header.Get("Content-Type"), snip)
  return 0
 }
 if len(argv) < 3 {
  fmt.Fprintln(os.Stderr, "参数错误")
  return 2
 }
 nodes, e := load(argv[2])
 if e != nil {
  fmt.Fprintln(os.Stderr, e)
  return 1
 }
 switch argv[1] {
 case "list":
  items := make([]item, 0, len(nodes))
  for i, n := range nodes {
   items = append(items, item{Index: i + 1, Name: pname(str(n["name"]), i)})
  }
  json.NewEncoder(out).Encode(items)
 case "prepare", "prepare-vpn":
  // prepare[-vpn] SOURCE INDEX OUT CTRLPORT SECRET [ROUTING]
  // All supported nodes go into the config as named proxies inside a single
  // `select` group "BB10"; the chosen node is the group's default. The app then
  // switches nodes and runs per-node latency tests live over the loopback
  // control API (external-controller + secret) without restarting the core.
  // ROUTING is "global" (default: everything via the node) or "split" (CN
  // domains/IPs and private ranges go DIRECT, the rest via the node — needs
  // geosite.dat + geoip.metadb in the core's working dir).
  if len(argv) != 7 && len(argv) != 8 {
   fmt.Fprintln(os.Stderr, "参数错误")
   return 2
  }
  routing := "global"
  if len(argv) == 8 {
   routing = argv[7]
   if routing != "global" && routing != "split" {
    fmt.Fprintln(os.Stderr, "路由模式无效")
    return 1
   }
  }
  idx, e := strconv.Atoi(argv[3])
  if e != nil || idx < 1 || idx > len(nodes) {
   fmt.Fprintln(os.Stderr, "节点编号无效")
   return 1
  }
  cport, e := strconv.Atoi(argv[5])
  if e != nil || cport < 1 || cport > 65535 {
   fmt.Fprintln(os.Stderr, "控制端口无效")
   return 1
  }
  secret := argv[6]
  if secret == "" || len(secret) > 128 || strings.ContainsAny(secret, "\r\n\x00") {
   fmt.Fprintln(os.Stderr, "控制密钥无效")
   return 1
  }
  names := make([]string, len(nodes))
  proxies := make([]any, len(nodes))
  for i, n := range nodes {
   names[i] = pname(str(n["name"]), i)
   // Pass the proxy through verbatim; only normalise the fields we own: a
   // unique name, an integer port, and UDP off. udp:false disables only the
   // proxy's UDP *relay* for client traffic (the QNX TUN datapath has no
   // reliable UDP relay); it does NOT disable a tuic/hysteria2 node's own
   // QUIC transport to its server, so those still carry TCP over QUIC.
   n["name"] = names[i]
   if port, ok := toInt(n["port"]); ok {
    n["port"] = port
   }
   n["udp"] = false
   proxies[i] = n
  }
  // Group list: chosen node first (becomes the select default), then the rest.
  order := make([]string, 0, len(names)+1)
  order = append(order, names[idx-1])
  for i, nm := range names {
   if i != idx-1 {
    order = append(order, nm)
   }
  }
  group := map[string]any{"name": "BB10", "type": "select", "proxies": order}
  ctrl := "127.0.0.1:" + strconv.Itoa(cport)
  // Routing rules. "global" sends everything to the node. "split" keeps private
  // ranges and mainland-China domains/IPs on DIRECT and proxies the rest, so
  // domestic traffic stays off the node. Split needs geosite.dat + geoip.metadb
  // in the core's working dir (the app copies them from assets); geodata-mode
  // false selects the mmdb geoip. Order matters: cheap no-resolve IP rules and
  // domain rules first, the IP geo rule (which may resolve) next, MATCH last.
  var rules []string
  if routing == "split" {
   rules = []string{
    "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
    "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
    "IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
    "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
    "GEOSITE,cn,DIRECT",
    "GEOIP,CN,DIRECT",
    "MATCH,BB10",
   }
  } else {
   rules = []string{"MATCH,BB10"}
  }
  var cfg map[string]any
  if argv[1] == "prepare-vpn" {
   // VPN mode: whole-device via TUN. The TUN, fake-ip range, interface address
   // and dns-hijack match the on-device verified config; the helper owns the
   // interface address and routes, the core only opens the device. Upstream DNS
   // (223.5.5.5 / 119.29.29.29) must differ from the phone's system resolvers
   // or fake-ip resolution loops.
   // DNS: fake-ip for whole-device capture. The critical keys mirror a shipped
   // high-star client (mihomo-party) and the mihomo DNS docs:
   //   - proxy-server-nameserver resolves the NODE's own hostname with real DNS
   //     so it never gets a fake-ip. Without it, fake-ip mode hands the core a
   //     198.19.x address for every domain node, the core dials that into tun3,
   //     and every node's connection (and every delay test) loops and fails.
   //   - default-nameserver (plain IPs) bootstraps resolver hostnames.
   //   - fake-ip-filter keeps local/captive domains on real IPs.
   // mixed-port gives a loopback HTTP/SOCKS listener alongside TUN so the in-app
   // "测试 HTTPS 连接" probe (which dials 127.0.0.1:17890) works in VPN mode too.
   // It is bound to 127.0.0.1, reachable only on-device, and does not alter the
   // phone's routing; TUN still carries whole-device traffic as before.
   cfg = map[string]any{"mixed-port": 17890, "allow-lan": false, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "silent", "ipv6": false,
    "unified-delay":       true,
    "external-controller": ctrl, "secret": secret,
    "profile": map[string]any{"store-selected": false, "store-fake-ip": false},
    "dns": map[string]any{"enable": true, "listen": "127.0.0.1:17853", "ipv6": false,
     "enhanced-mode": "fake-ip", "fake-ip-range": "198.19.0.0/16",
     "fake-ip-filter":          []string{"+.lan", "+.local", "localhost.ptlogin2.qq.com", "time.*.com", "ntp.*.com"},
     "default-nameserver":      []string{"223.5.5.5", "119.29.29.29"},
     "nameserver":              []string{"223.5.5.5", "119.29.29.29"},
     "proxy-server-nameserver": []string{"223.5.5.5", "119.29.29.29"}},
    "tun":     map[string]any{"enable": true, "stack": "mips", "device": "tun3", "mtu": 1500, "auto-route": false, "auto-detect-interface": false, "inet4-address": []string{"198.18.255.1/30"}, "dns-hijack": []string{"any:53"}},
    "proxies": proxies, "proxy-groups": []any{group}, "rules": rules}
  } else {
   cfg = map[string]any{"mixed-port": 17890, "allow-lan": false, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "silent", "ipv6": false,
    "unified-delay":       true,
    "external-controller": ctrl, "secret": secret,
    "dns":     map[string]any{"enable": false}, "tun": map[string]any{"enable": false},
    "proxies": proxies, "proxy-groups": []any{group}, "rules": rules}
  }
  // Split routing consults bundled geo databases; point the core at them and keep
  // it fully offline (no auto-download). geodata-mode:false uses geoip.metadb.
  if routing == "split" {
   cfg["geodata-mode"] = false
   cfg["geo-auto-update"] = false
   cfg["geodata-loader"] = "memconservative"
  }
  data, e := yaml.Marshal(cfg)
  if e != nil {
   fmt.Fprintln(os.Stderr, "配置生成失败")
   return 1
  }
  // Write beside the destination then rename. Never expose credentials via stdout.
  target := argv[4]
  tmp := target + ".new"
  f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
  if e != nil {
   fmt.Fprintln(os.Stderr, "无法创建私有配置")
   return 1
  }
  if _, e = f.Write(data); e == nil {
   e = f.Close()
  } else {
   f.Close()
  }
  if e == nil {
   e = os.Rename(tmp, target)
  }
  if e != nil {
   os.Remove(tmp)
   fmt.Fprintln(os.Stderr, "无法保存私有配置")
   return 1
  }
 default:
  fmt.Fprintln(os.Stderr, "参数错误")
  return 2
 }
 return 0
}
