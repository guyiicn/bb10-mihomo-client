// Command gen-node-config builds the "TUN in, proxy node out" test config.
//
//	gen-node-config SOURCE.yaml INDEX OUT.yaml
//
// It copies exactly one plugin-less Shadowsocks node (the INDEX-th, from 0)
// out of SOURCE into OUT under the neutral name "node". OUT then contains
// credentials: keep it private (mode 0600) and delete it after the test.
// Nothing sensitive is printed: only counts and the node's type and cipher.
package main

import (
	"fmt"
	"net"
	"os"
	"strconv"

	"go.yaml.in/yaml/v3"
)

const base = `# TUN steering test with one proxy node. CONTAINS CREDENTIALS.
allow-lan: false
bind-address: 127.0.0.1
mode: rule
log-level: info
ipv6: false
find-process-mode: off
profile:
  store-selected: false
  store-fake-ip: false
dns:
  enable: true
  listen: 127.0.0.1:17853
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.19.0.0/16
  nameserver:
    - 223.5.5.5
    - 119.29.29.29
tun:
  enable: true
  stack: mips
  device: tun3
  mtu: 1500
  auto-route: false
  auto-detect-interface: false
  dns-hijack:
    - any:53
  inet4-address:
    - 198.18.255.1/30
rules:
  - MATCH,node
`

func main() {
	if len(os.Args) != 4 {
		fmt.Println("usage: gen-node-config SOURCE.yaml INDEX OUT.yaml")
		os.Exit(2)
	}
	index, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fail("bad index")
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail("cannot read source")
	}
	var src struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(raw, &src); err != nil {
		fail("source is not valid YAML")
	}
	var plain []map[string]any
	for _, p := range src.Proxies {
		if p["type"] == "ss" && p["plugin"] == nil {
			plain = append(plain, p)
		}
	}
	fmt.Printf("proxies=%d plain-ss=%d\n", len(src.Proxies), len(plain))
	if index < 0 || index >= len(plain) {
		fail("index out of range")
	}
	node := plain[index]
	server, _ := node["server"].(string)
	fmt.Printf("picked #%d: type=ss cipher=%v udp=%v server-is-ip=%v\n", index, node["cipher"], node["udp"], net.ParseIP(server) != nil)
	node["name"] = "node"
	out, err := yaml.Marshal(map[string]any{"proxies": []any{node}})
	if err != nil {
		fail("marshal")
	}
	if err := os.WriteFile(os.Args[3], append([]byte(base), out...), 0o600); err != nil {
		fail("cannot write output")
	}
}

func fail(msg string) {
	fmt.Println("gen-node-config:", msg)
	os.Exit(1)
}
