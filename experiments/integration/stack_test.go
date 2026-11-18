package integration

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	tun "github.com/metacubex/sing-tun"
	"github.com/metacubex/sing/common/buf"
	"github.com/metacubex/sing/common/logger"
	M "github.com/metacubex/sing/common/metadata"
	N "github.com/metacubex/sing/common/network"
	lab "local/bb10-qnx-tun-lab"
)

// This assertion checks the actual imported sing-tun interface at compile time.
var _ tun.Tun = (*lab.Device)(nil)

type packetPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func newPipe() *packetPipe {
	return &packetPipe{make(chan []byte, 4), make(chan []byte, 4), make(chan struct{}), sync.Once{}}
}
func (p *packetPipe) Read(b []byte) (int, error) {
	select {
	case x := <-p.in:
		return copy(b, x), nil
	case <-p.done:
		return 0, net.ErrClosed
	}
}
func (p *packetPipe) Write(b []byte) (int, error) {
	select {
	case p.out <- append([]byte(nil), b...):
		return len(b), nil
	case <-p.done:
		return 0, net.ErrClosed
	}
}
func (p *packetPipe) Close() error { p.once.Do(func() { close(p.done) }); return nil }

type handler struct {
	tun.Handler
	seen     chan []byte
	failures chan error
}

func (h *handler) NewConnection(_ context.Context, conn net.Conn, _ M.Metadata) error {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	data := make([]byte, 5)
	if _, err := io.ReadFull(conn, data); err != nil {
		h.failures <- err
		return err
	}
	h.seen <- data
	_, err := conn.Write([]byte("reply"))
	if err != nil {
		h.failures <- err
	}
	return err
}

func (h *handler) NewPacket(_ context.Context, _ netip.AddrPort, b *buf.Buffer, m M.Metadata, init func(N.PacketConn) N.PacketWriter) {
	defer b.Release()
	h.seen <- append([]byte(nil), b.Bytes()...)
	response := buf.NewSize(5)
	response.Write([]byte("reply"))
	if e := init(nil).WritePacket(response, m.Destination); e != nil {
		h.failures <- e
	}
}

// An IPv4 UDP datagram; an all-zero UDP checksum is legal for IPv4.
func udpPacket() []byte {
	body := []byte("hello")
	p := make([]byte, 20+8+len(body))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	p[8] = 64
	p[9] = 17
	copy(p[12:16], []byte{10, 8, 0, 2})
	copy(p[16:20], []byte{198, 18, 0, 20})
	binary.BigEndian.PutUint16(p[20:22], 30123)
	binary.BigEndian.PutUint16(p[22:24], 8080)
	binary.BigEndian.PutUint16(p[24:26], uint16(8+len(body)))
	copy(p[28:], body)
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(p[10:12], ^uint16(sum))
	return p
}

func TestRealMipstackWithMockTun(t *testing.T) {
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
			wire, e := lab.Encode(mode, udpPacket(), 1500)
			if e != nil {
				t.Fatal(e)
			}
			pipe.in <- wire
			select {
			case data := <-h.seen:
				if string(data) != "hello" {
					t.Fatalf("unexpected payload %q", data)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no UDP forward callback")
			}
			select {
			case e = <-h.failures:
				t.Fatal(e)
			case frame := <-pipe.out:
				p, e := lab.Decode(mode, frame, 1500)
				if e != nil {
					t.Fatal(e)
				}
				if len(p) < 33 || p[9] != 17 || string(p[28:]) != "reply" || binary.BigEndian.Uint16(p[20:22]) != 8080 || binary.BigEndian.Uint16(p[22:24]) != 30123 {
					t.Fatal("bad UDP response packet")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no reply from stack")
			}
		})
	}
}
