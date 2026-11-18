package integration

import (
	"context"
	"encoding/binary"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	tun "github.com/metacubex/sing-tun"
	"github.com/metacubex/sing/common/logger"
	"local/bb10-qnx-tun-lab/backend"
)

var _ tun.Tun = (*backend.Tun)(nil)

// chanKernel is an in-memory stand-in for the driver in address-family
// header mode. No device is opened.
type chanKernel struct {
	in, out chan []byte
	gone    atomic.Bool
	reads   atomic.Int64
	pending []byte
}

func (k *chanKernel) Open(string) (int, error)           { return 5, nil }
func (k *chanKernel) IoctlSetInt(int, uint, int) error   { return nil }
func (k *chanKernel) IoctlGetInt(int, uint) (int, error) { return 0x400, nil }
func (k *chanKernel) Close(int) error                    { return nil }
func (k *chanKernel) WaitReadable(_ int, d time.Duration) (bool, error) {
	if k.gone.Load() || k.pending != nil {
		return true, nil
	}
	select {
	case k.pending = <-k.in:
		return true, nil
	case <-time.After(d):
		return k.gone.Load(), nil
	}
}
func (k *chanKernel) Read(_ int, p []byte) (int, error) {
	k.reads.Add(1)
	if k.gone.Load() {
		return 0, backend.ENXIO
	}
	f := make([]byte, 4+len(k.pending))
	binary.BigEndian.PutUint32(f, 2)
	copy(f[4:], k.pending)
	k.pending = nil
	return copy(p, f), nil
}
func (k *chanKernel) Write(_ int, p []byte) (int, error) {
	if k.gone.Load() {
		return 0, backend.ENXIO
	}
	k.out <- append([]byte(nil), p...)
	return len(p), nil
}

func TestRealMipstackWithBackendOverFakeKernel(t *testing.T) {
	k := &chanKernel{in: make(chan []byte, 4), out: make(chan []byte, 4)}
	dev, err := backend.Open(k, backend.Config{Path: "/dev/tun3", MTU: 1500, IfHead: true, PollInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{seen: make(chan []byte, 1), failures: make(chan error, 1)}
	opt := tun.Options{MTU: 1500, Inet4Address: []netip.Prefix{netip.MustParsePrefix("10.8.0.1/24")}}
	stack, err := tun.NewMipstack(tun.StackOptions{Context: context.Background(), Tun: dev, TunOptions: opt, Handler: h, Logger: logger.NOP(), UDPTimeout: time.Second, ICMPTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = stack.Start(); err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	k.in <- udpPacket()
	select {
	case data := <-h.seen:
		if string(data) != "hello" {
			t.Fatalf("payload %q", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no UDP forward callback")
	}
	select {
	case err = <-h.failures:
		t.Fatal(err)
	case f := <-k.out:
		if len(f) < 37 || binary.BigEndian.Uint32(f) != 2 || f[4+9] != 17 || string(f[4+28:]) != "reply" {
			t.Fatal("bad framed UDP response")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no reply from stack")
	}

	// Driver disappears: the device must fail once and the stack's read loop
	// must stop instead of spinning on the error.
	k.gone.Store(true)
	select {
	case <-dev.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("device did not fail")
	}
	if dev.Err() == nil {
		t.Fatal("failure cause lost")
	}
	time.Sleep(50 * time.Millisecond)
	before := k.reads.Load()
	time.Sleep(300 * time.Millisecond)
	if after := k.reads.Load(); after != before {
		t.Fatalf("read loop still spinning after failure: %d -> %d", before, after)
	}
}
