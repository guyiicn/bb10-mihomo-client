// Package qnxtunlab explores packet framing only. It does not open a TUN,
// perform ioctls, create interfaces, or configure routes.
package qnxtunlab

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Family values come from the BB10 10.3.1 SDK, not the host platform.
const (
	AFInet  = 2
	AFInet6 = 24
)

type Framing uint8

const (
	RawIP Framing = iota
	FamilyBE32
)

var ErrPacket = errors.New("invalid IP packet")
var ErrFamily = errors.New("unsupported or mismatched address family")
var ErrFraming = errors.New("unsupported framing")

// Family validates packet boundaries and returns the QNX address family.
// IPv6 jumbograms (payload-length 0 with data) are intentionally unsupported.
func Family(p []byte) (uint32, error) {
	if len(p) == 0 {
		return 0, ErrPacket
	}
	switch p[0] >> 4 {
	case 4:
		if len(p) < 20 {
			return 0, ErrPacket
		}
		ihl := int(p[0]&15) * 4
		if ihl < 20 || ihl > len(p) || int(binary.BigEndian.Uint16(p[2:4])) != len(p) {
			return 0, ErrPacket
		}
		return AFInet, nil
	case 6:
		if len(p) < 40 || 40+int(binary.BigEndian.Uint16(p[4:6])) != len(p) {
			return 0, ErrPacket
		}
		return AFInet6, nil
	default:
		return 0, ErrFamily
	}
}

// Encode returns one complete device frame. The caller must send it in one
// packet write; retrying a short write would create a second, corrupt packet.
func Encode(mode Framing, packet []byte, mtu int) ([]byte, error) {
	if mtu < 1 || len(packet) > mtu {
		return nil, ErrPacket
	}
	family, err := Family(packet)
	if err != nil {
		return nil, err
	}
	switch mode {
	case RawIP:
		return append([]byte(nil), packet...), nil
	case FamilyBE32:
		frame := make([]byte, 4+len(packet))
		binary.BigEndian.PutUint32(frame, family)
		copy(frame[4:], packet)
		return frame, nil
	default:
		return nil, ErrFraming
	}
}

// Decode validates one entire frame, then returns a copy of the IP packet.
func Decode(mode Framing, frame []byte, mtu int) ([]byte, error) {
	var declared uint32
	var packet []byte
	switch mode {
	case RawIP:
		packet = frame
	case FamilyBE32:
		if len(frame) < 4 {
			return nil, ErrPacket
		}
		declared = binary.BigEndian.Uint32(frame[:4])
		packet = frame[4:]
	default:
		return nil, ErrFraming
	}
	if mtu < 1 || len(packet) > mtu {
		return nil, ErrPacket
	}
	family, err := Family(packet)
	if err != nil {
		return nil, err
	}
	if mode == FamilyBE32 && family != declared {
		return nil, fmt.Errorf("%w: prefix %d", ErrFamily, declared)
	}
	return append([]byte(nil), packet...), nil
}
