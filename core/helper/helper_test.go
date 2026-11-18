package helper

import (
	"bufio"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu      sync.Mutex
	calls   []string
	resolve string
	table   string
	fail    string // substring of a command that must fail
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := name + " " + strings.Join(args, " ")
	switch name {
	case "getconf":
		return f.resolve, nil
	case "netstat":
		return f.table, nil
	}
	f.calls = append(f.calls, cmd)
	if f.fail != "" && strings.Contains(cmd, f.fail) {
		return "boom", errors.New("exit 1")
	}
	return "", nil
}

func (f *fakeRunner) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func cfg() Config {
	return Config{Tun: "tun3", Local: netip.MustParseAddr("198.18.255.1"), Peer: netip.MustParseAddr("198.18.255.2"),
		Range: netip.MustParsePrefix("198.19.0.0/16"), HijackDNS: true}
}

const table = "Destination Gateway Flags\ndefault 10.0.0.1 UGS msm1\n10.0.0.0/24 link#3 UC msm1\n"

func TestValidate(t *testing.T) {
	bad := []func(*Config){
		func(c *Config) { c.Tun = "tun12" },
		func(c *Config) { c.Tun = "tap0" },
		func(c *Config) { c.Local = netip.MustParseAddr("fd00::1") },
		func(c *Config) { c.Range = netip.MustParsePrefix("0.0.0.0/0") },
		func(c *Config) { c.Range = netip.MustParsePrefix("8.8.0.0/16") },
		func(c *Config) { c.Range = netip.MustParsePrefix("198.19.1.0/16") },
		func(c *Config) { c.Range = netip.MustParsePrefix("198.18.0.0/15") },
		func(c *Config) { c.Peer = c.Local },
	}
	for i, mut := range bad {
		c := cfg()
		mut(&c)
		if _, err := NewRoutes(c, &fakeRunner{}); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestUpDownAndNameserverFilter(t *testing.T) {
	// 10.0.0.1 is a gateway, ::1 is IPv6, 127.0.0.1 loopback: only 9.9.9.9 qualifies.
	f := &fakeRunner{resolve: "nameserver_10.0.0.1\nnameserver_9.9.9.9\nnameserver_fd00::1\nnameserver_127.0.0.1\n", table: table}
	r, err := NewRoutes(cfg(), f)
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Up()
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := "ifconfig tun3 inet 198.18.255.1 198.18.255.2 netmask 255.255.255.255 up\n" +
		"route -n add -net 198.19.0.0 -netmask 255.255.0.0 198.18.255.2\n" +
		"route -n add -host 9.9.9.9 198.18.255.2"
	if f.log() != want {
		t.Fatalf("got:\n%s", f.log())
	}
	// Network change: nameserver replaced.
	f.mu.Lock()
	f.resolve, f.calls = "nameserver_1.2.3.4", nil
	f.mu.Unlock()
	r.Reconcile()
	if f.log() != "route -n delete -host 9.9.9.9 198.18.255.2\nroute -n add -host 1.2.3.4 198.18.255.2" {
		t.Fatalf("reconcile:\n%s", f.log())
	}
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	r.Down()
	r.Down()
	want = "route -n delete -host 1.2.3.4 198.18.255.2\n" +
		"route -n delete -net 198.19.0.0 -netmask 255.255.0.0 198.18.255.2\n" +
		"ifconfig tun3 inet 198.18.255.1 delete\nifconfig tun3 down"
	if f.log() != want {
		t.Fatalf("down:\n%s", f.log())
	}
}

func TestStartReconcileTicksUntilStopped(t *testing.T) {
	c := cfg()
	c.Reconcile = 2 * time.Millisecond
	f := &fakeRunner{resolve: "nameserver_9.9.9.9", table: table}
	r, err := NewRoutes(c, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(); err != nil {
		t.Fatal(err)
	}
	// Simulate a network switch: resolver changes while up.
	f.mu.Lock()
	f.resolve, f.calls = "nameserver_1.2.3.4", nil
	f.mu.Unlock()
	stop := make(chan struct{})
	r.StartReconcile(stop)
	// The ticker must pick up the change on its own.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(f.log(), "route -n add -host 1.2.3.4") {
		if time.Now().After(deadline) {
			t.Fatalf("reconcile ticker never added new nameserver:\n%s", f.log())
		}
		time.Sleep(2 * time.Millisecond)
	}
	// After stop, ticking ceases: no further route calls once settled.
	close(stop)
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	if f.log() != "" {
		t.Fatalf("ticker kept running after stop:\n%s", f.log())
	}
}

func TestStartReconcileNoopWhenUnconfigured(t *testing.T) {
	f := &fakeRunner{resolve: "nameserver_9.9.9.9", table: table}
	r, _ := NewRoutes(cfg(), f) // cfg() has no Reconcile
	r.Up()
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	stop := make(chan struct{})
	r.StartReconcile(stop)
	close(stop)
	time.Sleep(20 * time.Millisecond)
	if f.log() != "" {
		t.Fatalf("unconfigured reconcile should do nothing:\n%s", f.log())
	}
}

func TestUpFailureRollsBack(t *testing.T) {
	f := &fakeRunner{table: table, fail: "add -net"}
	r, _ := NewRoutes(cfg(), f)
	if _, err := r.Up(); err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasSuffix(f.log(), "ifconfig tun3 inet 198.18.255.1 delete\nifconfig tun3 down") {
		t.Fatalf("no rollback:\n%s", f.log())
	}
}

func TestServeRollsBackWhenClientDies(t *testing.T) {
	for _, polite := range []bool{true, false} {
		f := &fakeRunner{resolve: "nameserver_9.9.9.9", table: table}
		r, _ := NewRoutes(cfg(), f)
		a, b := net.Pipe()
		done := make(chan struct{})
		go func() { r.Serve(b); close(done) }()
		a.Write([]byte("UP\n"))
		reply, _ := bufio.NewReader(a).ReadString('\n')
		if reply != "OK nameservers=1\n" {
			t.Fatalf("reply %q", reply)
		}
		if polite {
			go a.Write([]byte("DOWN\n"))
			bufio.NewReader(a).ReadString('\n')
		}
		a.Close() // the core crashing looks exactly like this
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return")
		}
		if !strings.HasSuffix(f.log(), "ifconfig tun3 down") || strings.Count(f.log(), "delete -net") != 1 {
			t.Fatalf("polite=%v:\n%s", polite, f.log())
		}
	}
}
