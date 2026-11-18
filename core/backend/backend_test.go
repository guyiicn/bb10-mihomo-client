package backend

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeKernel models the tun driver behaviour read from the device binary:
// exclusive open, TUNSIFHEAD/TUNGIFHEAD, EHOSTDOWN until the interface is
// configured, AF_INET-only writes without the header, families 2 and 24 only,
// silent truncation of frames larger than the read buffer, ENXIO/EBADF once
// gone. The MTU-exceeded errno is not known; EIO stands in for it.
type fakeKernel struct {
	mu       sync.Mutex
	open     bool
	ifhead   bool
	ready    bool
	gone     bool
	mtu      int
	queue    [][]byte // raw IP packets waiting for the reader
	rawQueue [][]byte // frames delivered verbatim (to inject garbage)
	written  [][]byte // raw IP packets accepted by the driver
	readErr  error
	reads    int
	closes   int
	shortBy  int
}

func (k *fakeKernel) Open(string) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.open {
		return -1, EBUSY
	}
	k.open = true
	return 7, nil
}
func (k *fakeKernel) IoctlSetInt(_ int, req uint, v int) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if req != TUNSIFHEAD {
		return syscall.ENOTTY
	}
	k.ifhead = v != 0
	return nil
}
func (k *fakeKernel) IoctlGetInt(_ int, req uint) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if req != TUNGIFHEAD {
		return 0, syscall.ENOTTY
	}
	if k.ifhead {
		return 0x400, nil
	}
	return 0, nil
}
func (k *fakeKernel) WaitReadable(_ int, d time.Duration) (bool, error) {
	deadline := time.Now().Add(d)
	for {
		k.mu.Lock()
		ok := !k.open || k.gone || k.readErr != nil || len(k.queue)+len(k.rawQueue) > 0
		k.mu.Unlock()
		if ok {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(time.Millisecond)
	}
}
func (k *fakeKernel) Read(_ int, p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reads++
	switch {
	case !k.open:
		return 0, EBADF
	case k.gone:
		return 0, ENXIO
	case k.readErr != nil:
		return 0, k.readErr
	case !k.ready:
		return 0, EHOSTDOWN
	}
	if len(k.rawQueue) > 0 {
		f := k.rawQueue[0]
		k.rawQueue = k.rawQueue[1:]
		return copy(p, f), nil
	}
	if len(k.queue) == 0 {
		return 0, EAGAIN
	}
	pkt := k.queue[0]
	k.queue = k.queue[1:]
	if k.ifhead {
		af := uint32(2)
		if pkt[0]>>4 == 6 {
			af = 24
		}
		f := make([]byte, 4+len(pkt))
		binary.BigEndian.PutUint32(f, af)
		copy(f[4:], pkt)
		pkt = f
	}
	return copy(p, pkt), nil // excess is dropped, as in the driver
}
func (k *fakeKernel) Write(_ int, p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.open {
		return 0, EBADF
	}
	if k.gone {
		return 0, ENXIO
	}
	af, body := uint32(2), p
	if k.ifhead {
		if len(p) < 4 {
			return 0, syscall.EIO
		}
		af, body = binary.BigEndian.Uint32(p), p[4:]
	}
	if len(body) > k.mtu {
		return 0, syscall.EIO
	}
	if af != 2 && af != 24 {
		return 0, syscall.EAFNOSUPPORT
	}
	k.written = append(k.written, append([]byte(nil), body...))
	return len(p) - k.shortBy, nil
}
func (k *fakeKernel) Close(int) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.closes++
	k.open = false
	return nil
}

func v4(n int) []byte {
	p := make([]byte, n)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(n))
	return p
}
func v6(payload int) []byte {
	p := make([]byte, 40+payload)
	p[0] = 0x60
	binary.BigEndian.PutUint16(p[4:], uint16(payload))
	return p
}

func open(t *testing.T, k *fakeKernel, ifhead bool) *Tun {
	t.Helper()
	k.mtu = 1400
	tun, err := Open(k, Config{Path: "/dev/tun3", MTU: 1400, IfHead: ifhead, PollInterval: 20 * time.Millisecond, MaxConsecutiveFailures: 3})
	if err != nil {
		t.Fatal(err)
	}
	return tun
}

func TestRealDeviceDisabledByDefault(t *testing.T) {
	if _, err := OpenReal(Config{Path: "/dev/tun3", MTU: 1400, AcknowledgeRealInterface: true}); !errors.Is(err, ErrRealDeviceDisabled) {
		t.Fatalf("default build must not open devices: %v", err)
	}
}

func TestConfigRejected(t *testing.T) {
	for _, c := range []Config{
		{Path: "/dev/tap0", MTU: 1400}, {Path: "/dev/tun10", MTU: 1400}, {Path: "/dev/tun0/../x", MTU: 1400},
		{Path: "/dev/tun0", MTU: 100}, {Path: "/dev/tun0", MTU: 1000, IfHead: true}, {Path: "/dev/tun0", MTU: 1400, PollInterval: time.Minute},
	} {
		k := &fakeKernel{}
		if _, err := Open(k, c); err == nil || k.open {
			t.Fatalf("config %+v accepted", c)
		}
	}
}

func TestOpenBusyAndIfHeadVerification(t *testing.T) {
	k := &fakeKernel{}
	tun := open(t, k, true)
	if !k.ifhead {
		t.Fatal("TUNSIFHEAD not issued")
	}
	if _, err := Open(k, Config{Path: "/dev/tun3", MTU: 1400}); !errors.Is(err, EBUSY) {
		t.Fatalf("second open: %v", err)
	}
	tun.Close()
	tun.Close()
	// The unit keeps its header flag across close; raw mode must clear it.
	raw := open(t, k, false)
	if k.ifhead {
		t.Fatal("stale header mode not cleared for raw mode")
	}
	raw.Close()
	k.closes--
	if k.closes != 1 {
		t.Fatalf("fd closed %d times", k.closes)
	}
	if tun.Err() != nil {
		t.Fatalf("orderly close reported %v", tun.Err())
	}
}

func TestRoundTripBothModes(t *testing.T) {
	for _, ifhead := range []bool{false, true} {
		k := &fakeKernel{ready: true}
		tun := open(t, k, ifhead)
		k.queue = [][]byte{v4(60)}
		if ifhead {
			k.queue = append(k.queue, v6(20))
		}
		out := make([]byte, 1400)
		for _, want := range append([][]byte(nil), k.queue...) {
			n, err := tun.Read(out)
			if err != nil || n != len(want) || out[0] != want[0] {
				t.Fatalf("ifhead=%v read n=%d err=%v", ifhead, n, err)
			}
		}
		if n, err := tun.Write(v4(80)); err != nil || n != 80 {
			t.Fatal(err)
		}
		_, err := tun.Write(v6(8))
		if ifhead && err != nil {
			t.Fatal(err)
		}
		if !ifhead && !errors.Is(err, ErrIPv6NeedsIfHead) {
			t.Fatalf("raw mode accepted IPv6: %v", err)
		}
		wantWritten := 1
		if ifhead {
			wantWritten = 2
		}
		if len(k.written) != wantWritten || len(k.written[0]) != 80 {
			t.Fatalf("driver saw %d packets", len(k.written))
		}
		tun.Close()
	}
}

func TestWriteRejectsBadPacketsWithoutTouchingDevice(t *testing.T) {
	k := &fakeKernel{ready: true}
	tun := open(t, k, true)
	defer tun.Close()
	bad := v4(60)
	bad[3]++ // length mismatch
	for _, p := range [][]byte{nil, {0x45}, bad, v4(1401)} {
		if _, err := tun.Write(p); err == nil {
			t.Fatal("bad packet accepted")
		}
	}
	if len(k.written) != 0 || tun.Stats().TxRejected != 4 {
		t.Fatalf("written=%d stats=%+v", len(k.written), tun.Stats())
	}
}

func TestGarbageIsDroppedThenBounded(t *testing.T) {
	k := &fakeKernel{ready: true}
	tun := open(t, k, true)
	k.rawQueue = [][]byte{{0, 0, 0, 2, 0x45}} // truncated
	k.queue = [][]byte{v4(40)}
	out := make([]byte, 1400)
	if n, err := tun.Read(out); err != nil || n != 40 {
		t.Fatalf("good packet after garbage: n=%d err=%v", n, err)
	}
	for i := 0; i < 10; i++ {
		k.rawQueue = append(k.rawQueue, []byte{0, 0, 0, 24, 0x45, 0, 0, 20})
	}
	_, err := tun.Read(out)
	if !errors.Is(err, os.ErrClosed) || tun.Err() == nil {
		t.Fatalf("garbage flood must fail the device: %v", err)
	}
	if k.reads > 1+1+4 || k.closes != 1 {
		t.Fatalf("unbounded: reads=%d closes=%d", k.reads, k.closes)
	}
	select {
	case <-tun.Done():
	default:
		t.Fatal("Done not signalled")
	}
}

func TestNotReadyIsBoundedAndTerminalErrnoIsImmediate(t *testing.T) {
	// Interface went down with a packet still queued: every read is EHOSTDOWN.
	// (An idle unconfigured interface never becomes readable; Read then just
	// polls until Close, which TestCloseUnblocksReader covers.)
	k := &fakeKernel{queue: [][]byte{v4(40)}}
	tun := open(t, k, false)
	if _, err := tun.Read(make([]byte, 1400)); !errors.Is(err, os.ErrClosed) || !errors.Is(err, EHOSTDOWN) {
		t.Fatalf("got %v", err)
	}
	if k.reads != 4 {
		t.Fatalf("reads=%d", k.reads)
	}
	if _, err := tun.Write(v4(40)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after failure: %v", err)
	}

	k2 := &fakeKernel{ready: true, gone: true}
	tun2 := open(t, k2, false)
	if _, err := tun2.Read(make([]byte, 1400)); !errors.Is(err, ENXIO) || k2.reads != 1 {
		t.Fatalf("ENXIO: err=%v reads=%d", err, k2.reads)
	}
}

func TestTransientErrorsDoNotFail(t *testing.T) {
	k := &fakeKernel{ready: true, readErr: EINTR}
	tun := open(t, k, false)
	defer tun.Close()
	go func() {
		time.Sleep(30 * time.Millisecond)
		k.mu.Lock()
		k.readErr = nil
		k.queue = [][]byte{v4(40)}
		k.mu.Unlock()
	}()
	if n, err := tun.Read(make([]byte, 1400)); err != nil || n != 40 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCloseUnblocksReader(t *testing.T) {
	k := &fakeKernel{ready: true}
	tun := open(t, k, true)
	res := make(chan error, 1)
	go func() { _, err := tun.Read(make([]byte, 1400)); res <- err }()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	tun.Close()
	select {
	case err := <-res:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader still blocked")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("close too slow")
	}
}

func TestShortWriteAndSmallBuffer(t *testing.T) {
	k := &fakeKernel{ready: true, queue: [][]byte{v4(200)}}
	tun := open(t, k, false)
	if _, err := tun.Read(make([]byte, 100)); err == nil || errors.Is(err, os.ErrClosed) {
		t.Fatalf("small buffer: %v", err)
	}
	k.shortBy = 1
	if _, err := tun.Write(v4(40)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("short write must fail the device: %v", err)
	}
}
