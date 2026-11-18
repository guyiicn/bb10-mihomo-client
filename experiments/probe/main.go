//go:build qnx && bb10_real_tun

// Command probe is the one-shot hardware check described in
// ../../TUN-DRIVER-STATIC-20261115.md section 5. It opens one tun node, issues
// TUNGIFHEAD / TUNSIFHEAD / FIONBIO, waits 100 ms for readability, attempts a
// single non-blocking read, and closes. It never writes a packet, assigns an
// address, brings the interface up, or touches routes. Any unexpected result
// stops the sequence and closes the descriptor.
package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	tunsifhead uint = 0x80047442
	tungifhead uint = 0x40047441
	fionbio    uint = 0x8004667e
)

func errno(err error) string {
	var e syscall.Errno
	if errors.As(err, &e) {
		return fmt.Sprintf("errno=%d (%v)", int(e), err)
	}
	return fmt.Sprintf("err=%v", err)
}

func main() {
	if len(os.Args) != 2 || len(os.Args[1]) != 9 || os.Args[1][:8] != "/dev/tun" {
		fmt.Println("usage: probe /dev/tunN")
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
	fmt.Println("open: ok")
	code := run(fd)
	if err = unix.Close(fd); err != nil {
		fmt.Println("close: FAIL", errno(err))
		code = 1
	} else {
		fmt.Println("close: ok")
	}
	os.Exit(code)
}

func run(fd int) int {
	v, err := unix.IoctlGetInt(fd, tungifhead)
	if err != nil {
		fmt.Println("TUNGIFHEAD(before): FAIL", errno(err))
		return 1
	}
	fmt.Printf("TUNGIFHEAD(before): %#x\n", v)
	if v != 0 {
		fmt.Println("unexpected: header mode already on")
		return 1
	}
	if err = unix.IoctlSetPointerInt(fd, tunsifhead, 1); err != nil {
		fmt.Println("TUNSIFHEAD=1: FAIL", errno(err))
		return 1
	}
	fmt.Println("TUNSIFHEAD=1: ok")
	if v, err = unix.IoctlGetInt(fd, tungifhead); err != nil {
		fmt.Println("TUNGIFHEAD(after): FAIL", errno(err))
		return 1
	}
	fmt.Printf("TUNGIFHEAD(after): %#x\n", v)
	if v == 0 {
		fmt.Println("unexpected: header mode did not stick")
		return 1
	}
	if err = unix.IoctlSetPointerInt(fd, fionbio, 1); err != nil {
		fmt.Println("FIONBIO=1: FAIL", errno(err))
		return 1 // never attempt a read that could block
	}
	fmt.Println("FIONBIO=1: ok")

	var r unix.FdSet
	r.Set(fd)
	tv := unix.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
	start := time.Now()
	n, err := unix.Select(fd+1, &r, nil, nil, &tv)
	fmt.Printf("select(100ms): n=%d %s elapsed=%v\n", n, errno(err), time.Since(start).Round(time.Millisecond))
	if err != nil {
		return 1
	}

	buf := make([]byte, 2048)
	start = time.Now()
	n, err = unix.Read(fd, buf)
	fmt.Printf("read(nonblocking): n=%d %s elapsed=%v\n", n, errno(err), time.Since(start).Round(time.Millisecond))
	return 0
}
