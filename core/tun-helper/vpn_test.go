package main

import (
	"bytes"
	"errors"
	"io"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"local/bb10-qnx-tun-lab/helper"
)

type fakeDev struct {
	given, restored int
	uid             int
	giveErr         error
}

func (d *fakeDev) give(uid int) error { d.given++; d.uid = uid; return d.giveErr }
func (d *fakeDev) restore() error     { d.restored++; return nil }

type recRunner struct {
	mu      sync.Mutex
	calls   []string
	resolve string
	failAdd bool
}

func (r *recRunner) Run(name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd := name + " " + strings.Join(args, " ")
	if name == "getconf" {
		return r.resolve, nil
	}
	if name == "netstat" {
		return "default 10.0.0.1 UGS msm1\n", nil
	}
	r.calls = append(r.calls, cmd)
	if r.failAdd && strings.Contains(cmd, "add -net") {
		return "boom", errors.New("exit 1")
	}
	return "", nil
}
func (r *recRunner) log() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
}

func newRoutes(t *testing.T, run helper.Runner) *helper.Routes {
	t.Helper()
	r, err := helper.NewRoutes(helper.Config{
		Tun: "tun3", Local: netip.MustParseAddr("198.18.255.1"), Peer: netip.MustParseAddr("198.18.255.2"),
		Range: netip.MustParsePrefix("198.19.0.0/16"), HijackDNS: true,
	}, run)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSessionUpThenDown(t *testing.T) {
	dev := &fakeDev{}
	run := &recRunner{resolve: "nameserver_9.9.9.9"}
	out := &bytes.Buffer{}
	code := session(strings.NewReader("UP\nDOWN\n"), out, 100, dev, newRoutes(t, run))
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if dev.given != 1 || dev.uid != 100 || dev.restored != 1 {
		t.Fatalf("dev=%+v", dev)
	}
	if !strings.Contains(out.String(), "OWNED") || !strings.Contains(out.String(), "READY nameservers=1") || !strings.Contains(out.String(), "OK down") {
		t.Fatalf("out=%q", out.String())
	}
	if !strings.Contains(run.log(), "ifconfig tun3 inet 198.18.255.1 198.18.255.2 netmask 255.255.255.255 up") || !strings.HasSuffix(run.log(), "ifconfig tun3 down") {
		t.Fatalf("routes not installed+removed:\n%s", run.log())
	}
}

func TestSessionTeardownOnEOF(t *testing.T) {
	// The app/core dying == stdin hitting EOF after UP. Everything must roll back.
	dev := &fakeDev{}
	run := &recRunner{resolve: "nameserver_9.9.9.9"}
	code := session(strings.NewReader("UP\n"), &bytes.Buffer{}, 100, dev, newRoutes(t, run))
	if code != 0 || dev.restored != 1 {
		t.Fatalf("code=%d dev=%+v", code, dev)
	}
	if strings.Count(run.log(), "delete -net") != 1 || !strings.HasSuffix(run.log(), "ifconfig tun3 down") {
		t.Fatalf("no rollback on EOF:\n%s", run.log())
	}
}

func TestSessionUpFailureRestoresDevice(t *testing.T) {
	dev := &fakeDev{}
	run := &recRunner{resolve: "nameserver_9.9.9.9", failAdd: true}
	out := &bytes.Buffer{}
	code := session(strings.NewReader("UP\n"), out, 100, dev, newRoutes(t, run))
	if code != 1 || dev.restored != 1 {
		t.Fatalf("code=%d dev=%+v", code, dev)
	}
	if !strings.Contains(out.String(), "ERR up") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestSessionRejectsUnknownAndBadGive(t *testing.T) {
	dev := &fakeDev{}
	out := &bytes.Buffer{}
	if code := session(strings.NewReader("HELLO\n"), out, 100, dev, newRoutes(t, &recRunner{})); code != 1 || dev.restored != 1 {
		t.Fatalf("unknown cmd: code=%d dev=%+v", code, dev)
	}
	bad := &fakeDev{giveErr: errors.New("nope")}
	out2 := &bytes.Buffer{}
	if code := session(strings.NewReader("UP\n"), out2, 100, bad, newRoutes(t, &recRunner{})); code != 1 {
		t.Fatalf("give failure code=%d", code)
	}
	if bad.restored != 0 || !strings.Contains(out2.String(), "ERR give device") {
		t.Fatalf("must not restore a device never given: %+v out=%q", bad, out2.String())
	}
}

func TestSessionDoubleUpIsIdempotent(t *testing.T) {
	dev := &fakeDev{}
	run := &recRunner{resolve: "nameserver_9.9.9.9"}
	session(strings.NewReader("UP\nUP\nDOWN\n"), io.Discard, 100, dev, newRoutes(t, run))
	if strings.Count(run.log(), "add -net") != 1 {
		t.Fatalf("range route added twice:\n%s", run.log())
	}
}
