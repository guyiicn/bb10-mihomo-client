// Command tun-helper is the experimental root/app bridge.
//
//	tun-helper serve UID SOCKET          (root) own the tun node and routes
//	tun-helper run SOCKET WAIT -- CMD... (app uid) run the core under it
//	tun-helper vpn                       (setuid root) one-shot VPN session over stdio
//
// "serve" gives /dev/tun3 to UID, listens on SOCKET (a unix socket only UID
// can reach) and serves one client at a time; see package helper for exactly
// what it will do. It restores the node's owner and removes everything on
// SIGTERM. It is started by hand over root SSH: nothing here installs it or
// touches the boot chain.
//
// "run" starts CMD, waits WAIT for it to open the device, asks the helper for
// UP, and keeps the connection open for as long as CMD lives.
package main

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"local/bb10-qnx-tun-lab/helper"
)

const tunName = "tun3"

type execRunner struct{}

// safePath is the only PATH the privileged helper uses to find ifconfig,
// route, getconf and netstat. A setuid-root program must never inherit PATH
// from the app that exec'd it, or a planted binary would run as root.
const safePath = "/sbin:/usr/sbin:/bin:/usr/bin"

func (execRunner) Run(name string, args ...string) (string, error) {
	// Resolve the absolute path ourselves against safePath; do not let
	// exec.Command consult the inherited PATH.
	abs := ""
	for _, dir := range strings.Split(safePath, ":") {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			abs = p
			break
		}
	}
	if abs == "" {
		return "", fmt.Errorf("%s not found in %s", name, safePath)
	}
	cmd := exec.Command(abs, args...)
	cmd.Env = []string{"PATH=" + safePath}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func main() {
	switch {
	case len(os.Args) == 4 && os.Args[1] == "serve":
		serve(os.Args[2], os.Args[3])
	case len(os.Args) >= 6 && os.Args[1] == "run" && os.Args[4] == "--":
		run(os.Args[2], os.Args[3], os.Args[5:])
	case len(os.Args) == 2 && os.Args[1] == "vpn":
		os.Exit(runVPN())
	case len(os.Args) == 3 && os.Args[1] == "vpnroot":
		os.Exit(runVPNRoot(os.Args[2]))
	case len(os.Args) == 2 && os.Args[1] == "idcheck":
		// Side-effect-free setuid probe: prints the real and effective uid so a
		// caller can confirm the setuid bit was honoured. Touches nothing.
		fmt.Printf("uid=%d euid=%d gid=%d egid=%d\n", os.Getuid(), os.Geteuid(), os.Getgid(), os.Getegid())
		os.Exit(0)
	default:
		fmt.Println("usage: tun-helper serve UID SOCKET | run SOCKET WAIT -- CMD... | vpn | vpnroot UID")
		os.Exit(2)
	}
}

func fatal(format string, a ...any) {
	fmt.Printf("tun-helper: "+format+"\n", a...)
	os.Exit(1)
}

func serve(uidText, socket string) {
	uid, err := strconv.Atoi(uidText)
	if err != nil || uid <= 0 {
		fatal("bad uid")
	}
	if os.Geteuid() != 0 {
		fatal("serve must run as root")
	}
	routes, err := helper.NewRoutes(helper.Config{
		Tun:       tunName,
		Local:     netip.MustParseAddr("198.18.255.1"),
		Peer:      netip.MustParseAddr("198.18.255.2"),
		Range:     netip.MustParsePrefix("198.19.0.0/16"),
		HijackDNS: true,
		Reconcile: 3 * time.Second,
	}, execRunner{})
	if err != nil {
		fatal("%v", err)
	}
	dir := filepath.Dir(socket)
	os.Remove(socket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fatal("%v", err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		fatal("listen: %v", err)
	}
	// Only UID may reach the socket: the directory is its private 0700.
	if err := os.Chown(dir, uid, -1); err != nil {
		fatal("%v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		fatal("%v", err)
	}
	if err := os.Chown(socket, uid, -1); err != nil {
		fatal("%v", err)
	}
	dev := "/dev/" + tunName
	if err := os.Chown(dev, uid, -1); err != nil {
		fatal("chown %s: %v", dev, err)
	}
	fmt.Println("serving: device handed to uid", uid)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP)
	go func() {
		<-sig
		l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			break
		}
		fmt.Println("client connected")
		routes.Serve(conn) // one client at a time
		fmt.Println("client gone, routes removed")
	}
	routes.Down()
	fmt.Println("restore owner:", os.Chown(dev, 0, -1), os.Chmod(dev, 0o600))
	os.Remove(socket)
}

func run(socket, waitText string, command []string) {
	wait, err := time.ParseDuration(waitText)
	if err != nil {
		fatal("bad wait")
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		fatal("helper not reachable: %v", err)
	}
	defer conn.Close()
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fatal("start: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		cmd.Process.Signal(syscall.SIGTERM)
	}()
	select {
	case err := <-exited:
		fatal("core exited before UP: %v", err)
	case <-time.After(wait):
	}
	fmt.Fprintln(conn, "UP")
	reply, _ := bufio.NewReader(conn).ReadString('\n')
	fmt.Print("helper: ", reply)
	if len(reply) < 2 || reply[:2] != "OK" {
		cmd.Process.Kill()
		<-exited
		os.Exit(1)
	}
	err = <-exited
	fmt.Fprintln(conn, "DOWN")
	fmt.Println("core exited:", err)
}
