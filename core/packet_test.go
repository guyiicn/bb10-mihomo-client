package qnxtunlab

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func ipv4() []byte {
	p := make([]byte, 20)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	return p
}
func ipv6() []byte { p := make([]byte, 40); p[0] = 0x60; return p }
func TestRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		p      []byte
		family uint32
	}{{"ipv4", ipv4(), AFInet}, {"ipv6", ipv6(), AFInet6}} {
		for _, mode := range []Framing{RawIP, FamilyBE32} {
			t.Run(tc.name+string(rune('0'+mode)), func(t *testing.T) {
				wire, e := Encode(mode, tc.p, 1500)
				if e != nil {
					t.Fatal(e)
				}
				if mode == FamilyBE32 && binary.BigEndian.Uint32(wire) != tc.family {
					t.Fatal("wrong QNX family")
				}
				decoded, e := Decode(mode, wire, 1500)
				if e != nil || !bytes.Equal(decoded, tc.p) {
					t.Fatalf("roundtrip %v", e)
				}
				wire[len(wire)-1] ^= 1
				if bytes.Equal(decoded, wire[len(wire)-len(decoded):]) {
					t.Fatal("decoder aliased device buffer")
				}
			})
		}
	}
}
func TestRejectMalformed(t *testing.T) {
	v4 := ipv4()
	v6 := ipv6()
	badIHL := ipv4()
	badIHL[0] = 0x44
	badLen := ipv4()
	badLen[3] = 21
	badV6 := ipv6()
	badV6[5] = 1
	for _, p := range [][]byte{nil, {0x45}, badIHL, badLen, badV6, {0x70}} {
		if _, e := Encode(RawIP, p, 1500); e == nil {
			t.Fatalf("accepted malformed %x", p)
		}
	}
	if _, e := Encode(RawIP, v4, 19); !errors.Is(e, ErrPacket) {
		t.Fatal(e)
	}
	if _, e := Decode(FamilyBE32, []byte{0, 0, 0}, 1500); !errors.Is(e, ErrPacket) {
		t.Fatal(e)
	}
	frame := append([]byte{0, 0, 0, AFInet6}, v4...)
	if _, e := Decode(FamilyBE32, frame, 1500); !errors.Is(e, ErrFamily) {
		t.Fatal(e)
	}
	frame = append([]byte{0, 0, 0, AFInet}, v6...)
	if _, e := Decode(FamilyBE32, frame, 1500); !errors.Is(e, ErrFamily) {
		t.Fatal(e)
	}
	if _, e := Encode(42, v4, 1500); !errors.Is(e, ErrFraming) {
		t.Fatal(e)
	}
	if _, e := Decode(42, v4, 1500); !errors.Is(e, ErrFraming) {
		t.Fatal(e)
	}
}
func FuzzDecode(f *testing.F) {
	f.Add(uint8(0), ipv4())
	f.Add(uint8(1), append([]byte{0, 0, 0, AFInet6}, ipv6()...))
	f.Add(uint8(1), []byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, m uint8, data []byte) {
		mode := Framing(m % 2)
		p, e := Decode(mode, data, 1500)
		if e != nil {
			return
		}
		wire, e := Encode(mode, p, 1500)
		if e != nil || !bytes.Equal(wire, data) {
			t.Fatalf("accepted non-round-trippable packet: %v", e)
		}
	})
}
