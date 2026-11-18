// Package backend is a default-disabled prototype of a BB10 (io-pkt-v6-hc)
// TUN packet backend with the Read/Write/Close shape of sing-tun.Tun.
//
// It only moves packets over an already-chosen /dev/tunN descriptor. It never
// assigns addresses, brings interfaces up, or touches routes, DNS, PF, APN or
// the VPN Manager. All kernel access goes through the Kernel interface; the
// only real implementation is compiled with `-tags bb10_real_tun` on QNX and
// additionally needs Config.AcknowledgeRealInterface. Nothing here has been
// run against the real driver: the constants and semantics below were read
// from the device's io-pkt-v6-hc binary by static analysis (see
// ../TUN-DRIVER-STATIC-20261115.md) and remain to be confirmed on hardware.
package backend

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	lab "local/bb10-qnx-tun-lab"
)

// ioctl requests decoded from the tun ioctl handler of the device binary
// (NetBSD numbering: 't',66 / 't',65, int argument).
const (
	TUNSIFHEAD uint = 0x80047442
	TUNGIFHEAD uint = 0x40047441
)

// QNX errno values, cross-checked between the binary and the x/sys QNX fork.
const (
	EINTR     = syscall.Errno(0x4)
	ENXIO     = syscall.Errno(0x6)
	EBADF     = syscall.Errno(0x9)
	EAGAIN    = syscall.Errno(0xb)
	EBUSY     = syscall.Errno(0x10)
	EHOSTDOWN = syscall.Errno(0x108) // tunread when the interface is not OPEN|INITED|IASET
)

// Kernel is the complete set of system operations the backend may perform.
type Kernel interface {
	Open(path string) (fd int, err error)
	IoctlSetInt(fd int, req uint, value int) error
	IoctlGetInt(fd int, req uint) (int, error)
	// WaitReadable returns false,nil on timeout.
	WaitReadable(fd int, timeout time.Duration) (bool, error)
	Read(fd int, p []byte) (int, error)
	Write(fd int, p []byte) (int, error)
	Close(fd int) error
}

type Config struct {
	Path string // exactly /dev/tun0../dev/tun9
	MTU  int    // must equal the interface MTU configured elsewhere
	// IfHead selects the 4-byte network-order address-family header
	// (TUNSIFHEAD). Without it the driver treats every written packet as
	// IPv4, so IPv6 writes are refused here instead of being corrupted.
	IfHead bool
	// PollInterval bounds how long Close waits for a blocked reader.
	PollInterval time.Duration
	// MaxConsecutiveFailures bounds malformed frames / transient read errors
	// before the device is failed. sing-tun's mipstack read loop retries
	// every non-closed error forever, so the bound has to live here.
	MaxConsecutiveFailures int
	// AcknowledgeRealInterface must be set for OpenReal. Opening /dev/tunN
	// creates a tunN network interface on the phone.
	AcknowledgeRealInterface bool
}

var pathRE = regexp.MustCompile(`^/dev/tun[0-9]$`)

var (
	ErrRealDeviceDisabled = errors.New("qnx tun: real device support not compiled in or not acknowledged")
	ErrIPv6NeedsIfHead    = errors.New("qnx tun: IPv6 write requires IfHead mode")
)

// FailedError is returned by Read/Write once the device has failed or been
// closed. It matches os.ErrClosed so packet loops stop instead of spinning.
type FailedError struct{ Cause error }

func (e *FailedError) Error() string {
	if e.Cause == nil {
		return "qnx tun: closed"
	}
	return "qnx tun: failed: " + e.Cause.Error()
}
func (e *FailedError) Unwrap() error        { return e.Cause }
func (e *FailedError) Is(target error) bool { return target == os.ErrClosed }

type Stats struct {
	RxPackets, TxPackets, RxDropped, TxRejected uint64
}

type Tun struct {
	k    Kernel
	fd   int
	cfg  Config
	mode lab.Framing

	io     sync.RWMutex // held shared around every syscall, exclusively by Close
	closed atomic.Bool
	once   sync.Once
	done   chan struct{}
	cause  error

	rx, tx, rxDrop, txRej atomic.Uint64
}

func (c *Config) normalize() error {
	if !pathRE.MatchString(c.Path) {
		return fmt.Errorf("qnx tun: refusing path %q", c.Path)
	}
	if c.MTU < 576 || c.MTU > 65535 || (c.IfHead && c.MTU < 1280) {
		return fmt.Errorf("qnx tun: invalid MTU %d", c.MTU)
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 250 * time.Millisecond
	}
	if c.PollInterval > 2*time.Second {
		return errors.New("qnx tun: PollInterval too long")
	}
	if c.MaxConsecutiveFailures <= 0 {
		c.MaxConsecutiveFailures = 32
	}
	return nil
}

// Open opens the device through k, sets TUNSIFHEAD to match cfg.IfHead and
// reads it back; any mismatch closes the descriptor and fails. No other ioctl
// is issued.
func Open(k Kernel, cfg Config) (*Tun, error) {
	if k == nil {
		return nil, errors.New("qnx tun: nil kernel")
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	fd, err := k.Open(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("qnx tun: open %s: %w", cfg.Path, err)
	}
	t := &Tun{k: k, fd: fd, cfg: cfg, mode: lab.RawIP, done: make(chan struct{})}
	// The header flag belongs to the tunN unit and survives close/reopen
	// (confirmed on the phone), so it is set and read back in both modes.
	want := 0
	if cfg.IfHead {
		t.mode, want = lab.FamilyBE32, 1
	}
	if err = k.IoctlSetInt(fd, TUNSIFHEAD, want); err == nil {
		var v int
		if v, err = k.IoctlGetInt(fd, TUNGIFHEAD); err == nil && (v != 0) != cfg.IfHead {
			err = fmt.Errorf("TUNGIFHEAD reports %#x", v)
		}
	}
	if err != nil {
		k.Close(fd)
		return nil, fmt.Errorf("qnx tun: set address-family header mode: %w", err)
	}
	return t, nil
}

// Done is closed when the device has been closed or has failed.
func (t *Tun) Done() <-chan struct{} { return t.done }

// Err reports why the device stopped; nil after an orderly Close.
func (t *Tun) Err() error {
	select {
	case <-t.done:
		return t.cause
	default:
		return nil
	}
}

func (t *Tun) Stats() Stats {
	return Stats{t.rx.Load(), t.tx.Load(), t.rxDrop.Load(), t.txRej.Load()}
}

func (t *Tun) shutdown(cause error) {
	t.once.Do(func() {
		t.closed.Store(true)
		t.io.Lock()
		err := t.k.Close(t.fd)
		t.io.Unlock()
		if cause == nil {
			cause = err
		}
		t.cause = cause
		close(t.done)
	})
}

// fail must not be called with t.io held.
func (t *Tun) fail(cause error) error {
	t.shutdown(cause)
	return &FailedError{t.cause}
}

func (t *Tun) Close() error {
	t.shutdown(nil)
	return nil
}

func terminal(err error) bool {
	return errors.Is(err, ENXIO) || errors.Is(err, EBADF)
}

// Read returns exactly one validated IP packet. Frames the driver hands over
// that do not validate are dropped and counted; a run of failures longer than
// MaxConsecutiveFailures fails the device.
func (t *Tun) Read(out []byte) (int, error) {
	frame := make([]byte, t.cfg.MTU+5) // header + one byte to expose oversize frames
	failures := 0
	var last error
	for {
		if t.closed.Load() {
			return 0, &FailedError{t.Err()}
		}
		if failures > t.cfg.MaxConsecutiveFailures {
			return 0, t.fail(fmt.Errorf("%d consecutive read failures, last: %w", failures, last))
		}
		if failures > 0 {
			time.Sleep(time.Duration(failures) * 10 * time.Millisecond)
		}
		t.io.RLock()
		if t.closed.Load() {
			t.io.RUnlock()
			continue
		}
		ready, err := t.k.WaitReadable(t.fd, t.cfg.PollInterval)
		n := 0
		if err == nil && ready {
			n, err = t.k.Read(t.fd, frame)
		}
		t.io.RUnlock()
		switch {
		case err == nil && !ready:
			continue
		case err == nil:
		case errors.Is(err, EINTR), errors.Is(err, EAGAIN):
			continue
		case terminal(err):
			return 0, t.fail(err)
		default: // includes EHOSTDOWN: interface lost its address or went down
			failures++
			last = err
			continue
		}
		packet, err := lab.Decode(t.mode, frame[:n], t.cfg.MTU)
		if err != nil {
			t.rxDrop.Add(1)
			failures++
			last = err
			continue
		}
		if len(out) < len(packet) {
			t.rxDrop.Add(1)
			return 0, fmt.Errorf("qnx tun: %d-byte packet exceeds %d-byte buffer", len(packet), len(out))
		}
		t.rx.Add(1)
		return copy(out, packet), nil
	}
}

// Write sends one IP packet in a single device write. Nothing is retried: a
// repeated or partial write would inject a second, corrupt packet.
func (t *Tun) Write(packet []byte) (int, error) {
	if t.closed.Load() {
		return 0, &FailedError{t.Err()}
	}
	if !t.cfg.IfHead && len(packet) > 0 && packet[0]>>4 == 6 {
		t.txRej.Add(1)
		return 0, ErrIPv6NeedsIfHead
	}
	frame, err := lab.Encode(t.mode, packet, t.cfg.MTU)
	if err != nil {
		t.txRej.Add(1)
		return 0, err
	}
	t.io.RLock()
	if t.closed.Load() {
		t.io.RUnlock()
		return 0, &FailedError{t.Err()}
	}
	n, err := t.k.Write(t.fd, frame)
	t.io.RUnlock()
	if err != nil {
		if terminal(err) {
			return 0, t.fail(err)
		}
		t.txRej.Add(1)
		return 0, err
	}
	if n != len(frame) {
		return 0, t.fail(fmt.Errorf("short write %d of %d", n, len(frame)))
	}
	t.tx.Add(1)
	return len(packet), nil
}
