package qnxtunlab

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type fakePacketFD struct {
	read    []byte
	written []byte
	calls   int
	short   bool
	closed  bool
}

func (f *fakePacketFD) Read(p []byte) (int, error) { return copy(p, f.read), nil }
func (f *fakePacketFD) Write(p []byte) (int, error) {
	f.calls++
	f.written = append([]byte(nil), p...)
	if f.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func (f *fakePacketFD) Close() error { f.closed = true; return nil }

func TestDeviceMockReadWrite(t *testing.T) {
	for _, mode := range []Framing{RawIP, FamilyBE32} {
		p := ipv4()
		wire, _ := Encode(mode, p, 1500)
		fake := &fakePacketFD{read: wire}
		dev, e := NewDevice(fake, mode, 1500)
		if e != nil {
			t.Fatal(e)
		}
		out := make([]byte, 1500)
		n, e := dev.Read(out)
		if e != nil || n != len(p) || !bytes.Equal(out[:n], p) {
			t.Fatalf("read: %d %v", n, e)
		}
		n, e = dev.Write(p)
		if e != nil || n != len(p) || fake.calls != 1 || !bytes.Equal(fake.written, wire) {
			t.Fatalf("write: %d %v", n, e)
		}
		fake.short = true
		n, e = dev.Write(p)
		if n != 0 || !errors.Is(e, io.ErrShortWrite) || fake.calls != 2 {
			t.Fatal("short write should fail without retry")
		}
		if e = dev.Close(); e != nil || !fake.closed {
			t.Fatal("close not forwarded")
		}
	}
}
func TestDeviceReject(t *testing.T) {
	f := &fakePacketFD{read: ipv4()}
	d, e := NewDevice(f, RawIP, 1500)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Read(make([]byte, 19)); !errors.Is(e, io.ErrShortBuffer) {
		t.Fatalf("short buffer %v", e)
	}
	if _, e = d.Write([]byte{0}); e == nil || f.calls != 0 {
		t.Fatal("invalid packet was sent")
	}
	if _, e = NewDevice(f, Framing(3), 1500); !errors.Is(e, ErrFraming) {
		t.Fatal(e)
	}
	if _, e = NewDevice(f, RawIP, 39); e == nil {
		t.Fatal("invalid mtu accepted")
	}
}
