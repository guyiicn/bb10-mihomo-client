package main

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"local/bb10-qnx-tun-lab/helper"
)

// device is the privileged half the vpn session needs: hand the tun node to an
// unprivileged uid and take it back. Behind an interface so the session logic
// is testable off-device.
type device interface {
	give(uid int) error // chown node to uid so the app can open it
	restore() error     // chown node back to root:nto 0600
}

type tunNode struct{ path string }

func (t tunNode) give(uid int) error {
	if err := os.Chown(t.path, uid, -1); err != nil {
		return err
	}
	return os.Chmod(t.path, 0o600)
}
func (t tunNode) restore() error {
	err := os.Chown(t.path, 0, -1)
	if err2 := os.Chmod(t.path, 0o600); err == nil {
		err = err2
	}
	return err
}

// vpnConfig is the single, fixed policy a setuid invocation installs. Nothing
// here comes from argv or the environment: a setuid-root binary that any app
// can exec must not take caller-controlled addresses, routes or commands.
func vpnConfig() helper.Config {
	return helper.Config{
		Tun:       tunName,
		Local:     netip.MustParseAddr("198.18.255.1"),
		Peer:      netip.MustParseAddr("198.18.255.2"),
		Range:     netip.MustParsePrefix("198.19.0.0/16"),
		HijackDNS: true,
		// Re-read the system nameservers periodically: on a network switch
		// (Wi-Fi<->cellular) the resolver addresses change, and without this the
		// DNS host routes into the tunnel go stale. Routes.Reconcile is mutex-
		// guarded against teardown, so this is safe for the whole session.
		Reconcile: 5 * time.Second,
	}
}

// runVPN is the setuid entry point. The app execs this (setuid root) and keeps
// it as a child with a connected pipe. Protocol over stdin/stdout:
//
//	helper -> OWNED            (node handed to the app's uid; app may start core)
//	app    -> UP               (core has opened the device)
//	helper -> READY nameservers=N   or   ERR ...
//	...session runs until the pipe closes or the app sends DOWN...
//	helper -> (tears down routes, restores the node, exits)
//
// Teardown happens on EOF too, so a dead app or core rolls everything back.
func runVPN() int {
	euid, ruid := os.Geteuid(), os.Getuid()
	if euid != 0 {
		fmt.Fprintln(os.Stderr, "tun-helper vpn: not privileged (needs setuid root)")
		return 1
	}
	if ruid == 0 {
		fmt.Fprintln(os.Stderr, "tun-helper vpn: refusing to run as real root; exec me from the app")
		return 1
	}
	routes, err := helper.NewRoutes(vpnConfig(), execRunner{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tun-helper vpn:", err)
		return 1
	}
	return session(os.Stdin, os.Stdout, ruid, tunNode{"/dev/" + tunName}, routes)
}

// runVPNRoot is the entry used when the app reaches root through the AutoRoot
// wrapper /bin/__root (which runs us as full root, so getuid() is 0 and the
// app's own uid must be passed in). On this phone setuid is not honoured on the
// writable filesystem, so this is the path the app actually uses. The uid is
// the unprivileged app that will open the device; it must be a real non-root
// uid. Everything else is identical to runVPN, including EOF rollback.
func runVPNRoot(uidText string) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "tun-helper vpnroot: not privileged")
		return 1
	}
	uid, err := strconv.Atoi(uidText)
	if err != nil || uid <= 0 || uid > 0x7fffffff {
		fmt.Fprintln(os.Stderr, "tun-helper vpnroot: bad uid")
		return 1
	}
	routes, err := helper.NewRoutes(vpnConfig(), execRunner{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tun-helper vpnroot:", err)
		return 1
	}
	return session(os.Stdin, os.Stdout, uid, tunNode{"/dev/" + tunName}, routes)
}

func session(in io.Reader, out io.Writer, uid int, dev device, routes *helper.Routes) int {
	if err := dev.give(uid); err != nil {
		fmt.Fprintf(out, "ERR give device: %v\n", err)
		return 1
	}
	// From here on the device belongs to the app: every exit path restores it.
	teardown := func() {
		routes.Down()
		dev.restore()
	}
	fmt.Fprintln(out, "OWNED")
	// Stop the reconcile ticker on every exit path, before teardown.
	stop := make(chan struct{})
	defer close(stop)
	lines := bufio.NewScanner(in)
	up := false
	for lines.Scan() {
		switch strings.TrimSpace(lines.Text()) {
		case "UP":
			if up {
				continue
			}
			n, err := routes.Up()
			if err != nil {
				fmt.Fprintf(out, "ERR up: %v\n", err)
				teardown()
				return 1
			}
			up = true
			routes.StartReconcile(stop)
			fmt.Fprintf(out, "READY nameservers=%d\n", n)
		case "DOWN":
			teardown()
			fmt.Fprintln(out, "OK down")
			return 0
		default:
			teardown()
			fmt.Fprintln(out, "ERR unknown command")
			return 1
		}
	}
	// EOF: the app or core died. Roll everything back.
	teardown()
	return 0
}
