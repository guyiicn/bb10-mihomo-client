package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadKeepsSupportedDropsPluginAndUnsupported(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "in.yaml")
	s := "proxies:\n" +
		"  - {name: a, type: ss, server: x.example, port: 443, cipher: aes-128-gcm, password: secret}\n" +
		"  - {name: r, type: ssr, server: r.example, port: 443, cipher: aes-128-cfb, password: pw, protocol: origin, obfs: plain}\n" +
		"  - {name: b, type: vmess, server: y.example, port: 443, uuid: 11111111-1111-1111-1111-111111111111, alterId: 0, cipher: auto}\n" +
		"  - {name: c, type: vless, server: z.example, port: 443, uuid: 22222222-2222-2222-2222-222222222222, tls: true, servername: z.example}\n" +
		"  - {name: d, type: trojan, server: w.example, port: 443, password: pw, sni: w.example}\n" +
		"  - {name: s, type: snell, server: s.example, port: 443, psk: abcdef, version: 2}\n" +
		"  - {name: t, type: tuic, server: t.example, port: 443, uuid: 33333333-3333-3333-3333-333333333333, password: pw}\n" +
		"  - {name: h, type: hysteria2, server: q.example, port: 443, password: pw}\n" +
		"  - {name: plug, type: ss, server: x.example, port: 443, cipher: aes-128-gcm, password: secret, plugin: obfs}\n" +
		"  - {name: wg, type: wireguard, server: g.example, port: 443, private-key: k, public-key: p}\n"
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
	n, e := load(p)
	if e != nil {
		t.Fatal(e)
	}
	// Kept: the 8 supported types. Dropped: the plugin ss (external plugin) and
	// the wireguard node (unsupported type, no QUIC/datapath for it here).
	if len(n) != 8 {
		t.Fatalf("want 8 kept (ss/ssr/vmess/vless/trojan/snell/tuic/hysteria2), got %d", len(n))
	}
	got := map[string]bool{}
	for _, m := range n {
		got[str(m["type"])] = true
		if str(m["plugin"]) != "" {
			t.Fatalf("plugin node must be dropped, but one survived: %v", m)
		}
	}
	for _, want := range []string{"ss", "ssr", "vmess", "vless", "trojan", "snell", "tuic", "hysteria2"} {
		if !got[want] {
			t.Fatalf("missing type %s; got %v", want, got)
		}
	}
	if got["wireguard"] {
		t.Fatal("unsupported type wireguard must be dropped")
	}
}

// Protocol-specific fields must survive pass-through into the emitted config.
func TestPreparePassesProtocolFields(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "in.yaml")
	if e := os.WriteFile(src, []byte("proxies:\n  - {name: v1, type: vmess, server: v.example, port: 8443, uuid: 11111111-1111-1111-1111-111111111111, alterId: 0, cipher: auto, network: ws, tls: true, servername: v.example}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(d, "active.yaml")
	if code := run([]string{"configtool", "prepare-vpn", src, "1", out, "17891", "S"}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	b, _ := os.ReadFile(out)
	s := string(b)
	for _, want := range []string{"type: vmess", "uuid: 11111111", "network: ws", "servername: v.example", "udp: false", "name: 1·v1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}
func TestRejectNoSupported(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "in.yaml")
	os.WriteFile(p, []byte("proxies: [{type: vmess}]"), 0600)
	if _, e := load(p); e == nil {
		t.Fatal("expected rejection")
	}
}
func TestRejectOversize(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "in.yaml")
	os.WriteFile(p, make([]byte, maxBytes+1), 0600)
	if _, e := load(p); e == nil {
		t.Fatal("expected rejection")
	}
}

func sampleSource(t *testing.T) string {
	d := t.TempDir()
	src := filepath.Join(d, "in.yaml")
	if e := os.WriteFile(src, []byte("proxies:\n  - {name: n1, type: ss, server: x.example, port: 443, cipher: aes-128-gcm, password: secret}\n  - {name: n2, type: ss, server: y.example, port: 8443, cipher: aes-256-gcm, password: secret2}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	return src
}

func TestPrepareVPNEmitsTunConfig(t *testing.T) {
	src := sampleSource(t)
	out := filepath.Join(filepath.Dir(src), "active.yaml")
	if code := run([]string{"configtool", "prepare-vpn", src, "2", out, "17891", "TESTSECRET"}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	b, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	for _, want := range []string{"enable: true", "device: tun3", "fake-ip-range: 198.19.0.0/16", "dns-hijack", "198.18.255.1/30", "auto-route: false", "enhanced-mode: fake-ip", "MATCH,BB10",
		"external-controller: 127.0.0.1:17891", "secret: TESTSECRET", "proxy-groups:", "name: BB10", "type: select",
		"proxy-server-nameserver:", "default-nameserver:", "fake-ip-filter:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("vpn config missing %q in:\n%s", want, s)
		}
	}
	// Both nodes must be present, and the chosen node (index 2) must be the
	// group's first (default) entry.
	if !strings.Contains(s, "2·n2") || !strings.Contains(s, "1·n1") {
		t.Fatalf("missing node names:\n%s", s)
	}
	i2, i1 := strings.Index(s, "- 2·n2"), strings.Index(s, "- 1·n1")
	if i2 < 0 || i1 < 0 || i2 > i1 {
		t.Fatalf("chosen node not first in group:\n%s", s)
	}
	if !strings.Contains(s, "mixed-port: 17890") {
		t.Fatalf("vpn config must expose mixed-port for the HTTPS probe:\n%s", s)
	}
	if fi, _ := os.Stat(out); fi == nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("perm not 0600")
	}
	// Default (no routing arg) must stay global: a single MATCH rule, no geo keys.
	if strings.Contains(s, "GEOSITE") || strings.Contains(s, "geodata-mode") {
		t.Fatalf("default routing must be global (no geo rules):\n%s", s)
	}
}

func TestPrepareSplitRouting(t *testing.T) {
	src := sampleSource(t)
	out := filepath.Join(filepath.Dir(src), "active.yaml")
	if code := run([]string{"configtool", "prepare-vpn", src, "1", out, "17891", "TESTSECRET", "split"}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	b, _ := os.ReadFile(out)
	s := string(b)
	for _, want := range []string{
		"GEOSITE,cn,DIRECT", "GEOIP,CN,DIRECT", "MATCH,BB10",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve", "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"geodata-mode: false", "geo-auto-update: false",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("split config missing %q in:\n%s", want, s)
		}
	}
	// A CN direct rule must come before the catch-all MATCH.
	if gi, mi := strings.Index(s, "GEOSITE,cn,DIRECT"), strings.Index(s, "MATCH,BB10"); gi < 0 || mi < 0 || gi > mi {
		t.Fatalf("GEOSITE,cn must precede MATCH:\n%s", s)
	}
	// Invalid routing mode is rejected.
	if code := run([]string{"configtool", "prepare-vpn", src, "1", out, "17891", "TESTSECRET", "bogus"}, &bytes.Buffer{}); code == 0 {
		t.Fatalf("invalid routing mode must fail")
	}
}

func TestPrepareProxyUnchanged(t *testing.T) {
	src := sampleSource(t)
	out := filepath.Join(filepath.Dir(src), "active.yaml")
	if code := run([]string{"configtool", "prepare", src, "1", out, "17891", "TESTSECRET"}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	b, _ := os.ReadFile(out)
	s := string(b)
	if !strings.Contains(s, "mixed-port: 17890") {
		t.Fatalf("proxy config lost mixed-port:\n%s", s)
	}
	if strings.Contains(s, "tun3") || strings.Contains(s, "fake-ip") {
		t.Fatalf("proxy config must not enable tun/dns:\n%s", s)
	}
	if !strings.Contains(s, "external-controller: 127.0.0.1:17891") {
		t.Fatalf("proxy config lost controller:\n%s", s)
	}
}

func TestBadArgs(t *testing.T) {
	src0 := sampleSource(t)
	if code := run([]string{"configtool", "prepare-vpn", src0, "1"}, &bytes.Buffer{}); code != 2 {
		t.Fatalf("missing args exit=%d", code)
	}
	src := sampleSource(t)
	out := filepath.Join(filepath.Dir(src), "x.yaml")
	if code := run([]string{"configtool", "prepare-vpn", src, "9", out, "17891", "TESTSECRET"}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("bad index exit=%d", code)
	}
	if code := run([]string{"configtool", "prepare-vpn", src, "1", out, "99999", "TESTSECRET"}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("bad port exit=%d", code)
	}
	if code := run([]string{"configtool", "prepare-vpn", src, "1", out, "17891", ""}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("empty secret exit=%d", code)
	}
}

func TestListStillWorks(t *testing.T) {
	src := sampleSource(t)
	var b bytes.Buffer
	if code := run([]string{"configtool", "list", src}, &b); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(b.String(), `"name":"1·n1"`) {
		t.Fatalf("list output=%s", b.String())
	}
}
