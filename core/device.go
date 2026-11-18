package qnxtunlab

import (
	"errors"
	"io"
)

// Device adapts a packet-oriented ReadWriteCloser to raw IP. It accepts an
// already-open handle only. No platform-specific device is opened here.
// One concurrent reader and one concurrent writer are supported if the
// underlying handle supports them; multiple readers need external locking.
type Device struct {
	fd   io.ReadWriteCloser
	mode Framing
	mtu  int
}

func NewDevice(fd io.ReadWriteCloser, mode Framing, mtu int) (*Device, error) {
	if fd == nil || mtu < 40 || mtu > 65535 {
		return nil, errors.New("invalid device or MTU")
	}
	if mode != RawIP && mode != FamilyBE32 {
		return nil, ErrFraming
	}
	return &Device{fd: fd, mode: mode, mtu: mtu}, nil
}

func (d *Device) Read(out []byte) (int, error) {
	// One extra byte detects frames beyond the selected MTU where the underlying
	// packet source reports the bytes copied. Actual QNX truncation semantics are
	// unknown and must be tested before any production use.
	b := make([]byte, d.mtu+5)
	n, err := d.fd.Read(b)
	if n < 0 || n > len(b) {
		return 0, ErrPacket
	}
	if err != nil {
		return 0, err
	}
	packet, err := Decode(d.mode, b[:n], d.mtu)
	if err != nil {
		return 0, err
	}
	if len(out) < len(packet) {
		return 0, io.ErrShortBuffer
	}
	return copy(out, packet), nil
}
func (d *Device) Write(packet []byte) (int, error) {
	frame, err := Encode(d.mode, packet, d.mtu)
	if err != nil {
		return 0, err
	}
	n, err := d.fd.Write(frame)
	if err != nil {
		return 0, err
	}
	if n != len(frame) {
		return 0, io.ErrShortWrite
	}
	return len(packet), nil
}
func (d *Device) Close() error { return d.fd.Close() }
