package integration

import (
	"context"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	mips "github.com/metacubex/mipstack"
	tun "github.com/metacubex/sing-tun"
	"github.com/metacubex/sing/common/logger"
	lab "local/bb10-qnx-tun-lab"
)

func tcpPacket(seq, ack uint32, flags byte, payload []byte) []byte {
	p := make([]byte, 40+len(payload))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	p[8] = 64
	p[9] = 6
	copy(p[12:16], []byte{10, 8, 0, 2})
	copy(p[16:20], []byte{198, 18, 0, 20})
	binary.BigEndian.PutUint16(p[10:12], mips.InternetChecksum(p[:20]))
	t := p[20:]
	binary.BigEndian.PutUint16(t[0:2], 30124)
	binary.BigEndian.PutUint16(t[2:4], 443)
	binary.BigEndian.PutUint32(t[4:8], seq)
	binary.BigEndian.PutUint32(t[8:12], ack)
	t[12] = 0x50
	t[13] = flags
	binary.BigEndian.PutUint16(t[14:16], 65535)
	copy(t[20:], payload)
	c, e := mips.IPTransportChecksum(netip.MustParseAddr("10.8.0.2"), netip.MustParseAddr("198.18.0.20"), 6, t)
	if e != nil {
		panic(e)
	}
	binary.BigEndian.PutUint16(t[16:18], c)
	return p
}
func TestRealMipstackTCPWithMockTun(t *testing.T) {
	for _, mode := range []lab.Framing{lab.RawIP, lab.FamilyBE32} {
		t.Run(map[lab.Framing]string{lab.RawIP: "raw", lab.FamilyBE32: "family-header"}[mode], func(t *testing.T) {
			pipe := newPipe()
			dev, e := lab.NewDevice(pipe, mode, 1500)
			if e != nil {
				t.Fatal(e)
			}
			h := &handler{seen: make(chan []byte, 1), failures: make(chan error, 1)}
			opt := tun.Options{MTU: 1500, Inet4Address: []netip.Prefix{netip.MustParsePrefix("10.8.0.1/24")}}
			stack, e := tun.NewMipstack(tun.StackOptions{Context: context.Background(), Tun: dev, TunOptions: opt, Handler: h, Logger: logger.NOP(), UDPTimeout: time.Second, ICMPTimeout: time.Second})
			if e != nil {
				t.Fatal(e)
			}
			if e = stack.Start(); e != nil {
				t.Fatal(e)
			}
			defer stack.Close()
			defer dev.Close()
			send := func(p []byte) {
				wire, e := lab.Encode(mode, p, 1500)
				if e != nil {
					t.Fatal(e)
				}
				pipe.in <- wire
			}
			receive := func() []byte {
				t.Helper()
				select {
				case f := <-pipe.out:
					p, e := lab.Decode(mode, f, 1500)
					if e != nil {
						t.Fatal(e)
					}
					return p
				case e := <-h.failures:
					t.Fatal(e)
				case <-time.After(3 * time.Second):
					t.Fatal("timeout receiving TCP packet")
				}
				return nil
			}
			send(tcpPacket(100, 0, 0x02, nil))
			synack := receive()
			if len(synack) < 40 || synack[9] != 6 || synack[33]&0x12 != 0x12 {
				t.Fatalf("expected SYN/ACK: %x", synack)
			}
			serverSeq := binary.BigEndian.Uint32(synack[24:28])
			if binary.BigEndian.Uint32(synack[28:32]) != 101 {
				t.Fatal("unexpected client acknowledgement")
			}
			send(tcpPacket(101, serverSeq+1, 0x10, nil))
			send(tcpPacket(101, serverSeq+1, 0x18, []byte("hello")))
			select {
			case data := <-h.seen:
				if string(data) != "hello" {
					t.Fatal("bad forwarded TCP payload")
				}
			case e := <-h.failures:
				t.Fatal(e)
			case <-time.After(3 * time.Second):
				t.Fatal("no TCP forwarding callback")
			}
			deadline := time.After(3 * time.Second)
			for {
				select {
				case f := <-pipe.out:
					p, e := lab.Decode(mode, f, 1500)
					if e != nil {
						t.Fatal(e)
					}
					if len(p) < 40 || p[9] != 6 {
						continue
					}
					iplen := int(p[0]&15) * 4
					if len(p) < iplen+20 {
						continue
					}
					tcplen := int(p[iplen+12]>>4) * 4
					if len(p) < iplen+tcplen {
						continue
					}
					if string(p[iplen+tcplen:]) == "reply" {
						return
					}
				case e := <-h.failures:
					t.Fatal(e)
				case <-deadline:
					t.Fatal("no TCP reply")
				}
			}
		})
	}
}
