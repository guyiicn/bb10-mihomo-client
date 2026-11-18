//go:build qnx && bb10_real_tun

// Command probe2 is the approved point-to-point loop test: the interface must
// already carry LOCAL -> PEER (configured by the caller). It sends one UDP
// datagram from LOCAL to PEER through the kernel, expects it on the tun
// descriptor, injects one crafted UDP reply and expects it on the socket.
// It configures nothing itself and exits within 8 seconds.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	lab "local/bb10-qnx-tun-lab"
)

const (
	tunsifhead uint = 0x80047442
	tungifhead uint = 0x40047441
	fionbio    uint = 0x8004667e
	peerPort        = 40000
)

func errno(err error) string {
	var e syscall.Errno
	if errors.As(err, &e) {
		return fmt.Sprintf("errno=%d (%v)", int(e), err)
	}
	return fmt.Sprintf("err=%v", err)
}

func main() {
	if len(os.Args) != 5 || len(os.Args[1]) != 9 || os.Args[1][:8] != "/dev/tun" || (os.Args[2] != "ifhead" && os.Args[2] != "raw") {
		fmt.Println("usage: probe2 /dev/tunN ifhead|raw LOCAL PEER")
		os.Exit(2)
	}
	local, peer := net.ParseIP(os.Args[3]).To4(), net.ParseIP(os.Args[4]).To4()
	if local == nil || peer == nil {
		fmt.Println("bad address")
		os.Exit(2)
	}
	go func() {
		time.Sleep(8 * time.Second)
		fmt.Println("WATCHDOG: exiting")
		os.Exit(3)
	}()
	fd, err := unix.Open(os.Args[1], unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		fmt.Println("open: FAIL", errno(err))
		os.Exit(1)
	}
	fmt.Println("open: ok, mode", os.Args[2])
	code := run(fd, os.Args[2] == "ifhead", local, peer)
	if err = unix.Close(fd); err != nil {
		fmt.Println("close: FAIL", errno(err))
		code = 1
	} else {
		fmt.Println("close: ok")
	}
	os.Exit(code)
}

func waitRead(fd int, buf []byte, d time.Duration) (int, error) {
	var r unix.FdSet
	r.Set(fd)
	tv := unix.NsecToTimeval(d.Nanoseconds())
	n, err := unix.Select(fd+1, &r, nil, nil, &tv)
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}
	if n == 0 {
		return 0, errors.New("select timeout")
	}
	return unix.Read(fd, buf)
}

func run(fd int, ifhead bool, local, peer net.IP) int {
	// The header flag survives close/reopen of the same unit (seen on the
	// phone), so it is always set explicitly, never assumed.
	mode, want := lab.RawIP, 0
	if ifhead {
		mode, want = lab.FamilyBE32, 1
	}
	if err := unix.IoctlSetPointerInt(fd, tunsifhead, want); err != nil {
		fmt.Println("TUNSIFHEAD: FAIL", errno(err))
		return 1
	}
	v, err := unix.IoctlGetInt(fd, tungifhead)
	fmt.Printf("TUNGIFHEAD: %#x %s\n", v, errno(err))
	if err != nil || (v != 0) != ifhead {
		return 1
	}
	if err = unix.IoctlSetPointerInt(fd, fionbio, 1); err != nil {
		fmt.Println("FIONBIO: FAIL", errno(err))
		return 1
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: local})
	if err != nil {
		fmt.Println("udp bind: FAIL", err)
		return 1
	}
	defer conn.Close()
	lport := conn.LocalAddr().(*net.UDPAddr).Port
	out := []byte("probe-out")
	if _, err = conn.WriteToUDP(out, &net.UDPAddr{IP: peer, Port: peerPort}); err != nil {
		fmt.Println("udp send: FAIL", errno(err))
		return 1
	}
	fmt.Println("udp send: ok")

	// Kernel -> tun.
	buf := make([]byte, 2048)
	got := false
	for i := 0; i < 5 && !got; i++ {
		n, err := waitRead(fd, buf, time.Second)
		if err != nil {
			fmt.Printf("tun read: FAIL n=%d %s\n", n, errno(err))
			return 1
		}
		head := n
		if head > 8 {
			head = 8
		}
		fmt.Printf("tun read: n=%d first bytes=% x\n", n, buf[:head])
		p, err := lab.Decode(mode, buf[:n], 1500)
		if err != nil {
			fmt.Println("  frame does not decode in this mode:", err)
			return 1
		}
		if p[9] != 17 || !bytes.Equal(p[12:16], local) || !bytes.Equal(p[16:20], peer) {
			fmt.Printf("  other packet (proto %d), skipping\n", p[9])
			continue
		}
		ihl := int(p[0]&15) * 4
		sport, dport := binary.BigEndian.Uint16(p[ihl:]), binary.BigEndian.Uint16(p[ihl+2:])
		ok := int(sport) == lport && dport == peerPort && bytes.Equal(p[ihl+8:], out)
		fmt.Printf("  IPv4 UDP local->peer, ports and payload match: %v, ip len=%d\n", ok, len(p))
		if !ok {
			return 1
		}
		got = true
	}
	if !got {
		fmt.Println("tun read: probe datagram not seen")
		return 1
	}

	// tun -> kernel.
	in := []byte("probe-in")
	p := make([]byte, 28+len(in))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	p[8], p[9] = 64, 17
	copy(p[12:16], peer)
	copy(p[16:20], local)
	binary.BigEndian.PutUint16(p[20:], peerPort)
	binary.BigEndian.PutUint16(p[22:], uint16(lport))
	binary.BigEndian.PutUint16(p[24:], uint16(8+len(in))) // UDP checksum 0: legal for IPv4
	copy(p[28:], in)
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i:]))
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(p[10:], ^uint16(sum))
	frame, err := lab.Encode(mode, p, 1500)
	if err != nil {
		fmt.Println("encode:", err)
		return 1
	}
	n, err := unix.Write(fd, frame)
	fmt.Printf("tun write: n=%d of %d %s\n", n, len(frame), errno(err))
	if err != nil || n != len(frame) {
		return 1
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	rb := make([]byte, 64)
	rn, from, err := conn.ReadFromUDP(rb)
	if err != nil {
		fmt.Println("udp recv: FAIL", err)
		return 1
	}
	ok := bytes.Equal(rb[:rn], in) && from.IP.Equal(peer) && from.Port == peerPort
	fmt.Printf("udp recv: %d bytes, source and payload match: %v\n", rn, ok)
	if !ok {
		return 1
	}
	fmt.Println("RESULT: loop ok")
	return 0
}
