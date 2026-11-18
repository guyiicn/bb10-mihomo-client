// Package helper is the root half of the experimental root/app split.
//
// The proxy core runs unprivileged (as the app's uid) and opens /dev/tunN
// itself after the helper has handed it ownership of that one node. Everything
// else that needs root is done here, and only this fixed list:
//
//   - give /dev/tunN to one uid, and take it back on exit
//   - assign the point-to-point address and bring the interface up
//   - route the fake-ip range and the current IPv4 system nameservers to it
//   - undo all of that when the client says DOWN or its connection closes
//
// The client cannot choose addresses, routes or commands: they are fixed when
// root starts the helper. A dead core closes its socket, which is what
// triggers the rollback; the driver itself does not remove these routes when
// the device is closed (measured on the phone).
package helper

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Runner executes one system command and returns its combined output.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

type Config struct {
	Tun   string       // interface name, e.g. "tun3"
	Local netip.Addr   // interface address
	Peer  netip.Addr   // point-to-point destination, used as route gateway
	Range netip.Prefix // fake-ip range routed into the tunnel
	// HijackDNS routes each system nameserver's /32 into the tunnel so that
	// the core's dns-hijack answers the phone's ordinary resolver.
	HijackDNS bool
	// Reconcile is how often the nameserver list is re-read while up.
	Reconcile time.Duration
}

func (c Config) validate() error {
	if len(c.Tun) != 4 || !strings.HasPrefix(c.Tun, "tun") || c.Tun[3] < '0' || c.Tun[3] > '9' {
		return fmt.Errorf("helper: bad tun name %q", c.Tun)
	}
	if !c.Local.Is4() || !c.Peer.Is4() || !c.Range.Addr().Is4() {
		return errors.New("helper: IPv4 only (IPv6 routing on this phone carries IMS)")
	}
	if c.Range.Bits() < 12 || c.Range != c.Range.Masked() {
		return fmt.Errorf("helper: range %v must be a canonical prefix no wider than /12", c.Range)
	}
	if !c.Range.Addr().IsPrivate() && !netip.MustParsePrefix("198.18.0.0/15").Contains(c.Range.Addr()) {
		return fmt.Errorf("helper: range %v is not a private or benchmark range", c.Range)
	}
	if c.Range.Contains(c.Local) || c.Range.Contains(c.Peer) || c.Local == c.Peer {
		return errors.New("helper: interface addresses must be distinct and outside the routed range")
	}
	return nil
}

// Routes owns the interface configuration and the routes. It is safe for
// concurrent use.
type Routes struct {
	cfg Config
	run Runner

	mu sync.Mutex
	up bool
	ns map[netip.Addr]bool // nameserver host routes currently installed
}

func NewRoutes(cfg Config, run Runner) (*Routes, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Routes{cfg: cfg, run: run, ns: map[netip.Addr]bool{}}, nil
}

func mask(bits int) string {
	m := net.CIDRMask(bits, 32)
	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}

// Up configures the interface and installs the routes. On any failure it
// rolls back whatever it did.
func (r *Routes) Up() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.up {
		return len(r.ns), nil
	}
	c := r.cfg
	r.up = true // so that down() undoes partial work
	if out, err := r.run.Run("ifconfig", c.Tun, "inet", c.Local.String(), c.Peer.String(), "netmask", "255.255.255.255", "up"); err != nil {
		r.down()
		return 0, fmt.Errorf("ifconfig: %v: %s", err, out)
	}
	if out, err := r.run.Run("route", "-n", "add", "-net", c.Range.Addr().String(), "-netmask", mask(c.Range.Bits()), c.Peer.String()); err != nil {
		r.down()
		return 0, fmt.Errorf("route add range: %v: %s", err, out)
	}
	r.reconcile()
	return len(r.ns), nil
}

// Reconcile re-reads the system nameservers and adjusts the host routes.
func (r *Routes) Reconcile() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.up {
		r.reconcile()
	}
}

func (r *Routes) reconcile() {
	if !r.cfg.HijackDNS {
		return
	}
	want := map[netip.Addr]bool{}
	for _, a := range r.nameservers() {
		want[a] = true
	}
	for a := range r.ns {
		if !want[a] {
			r.run.Run("route", "-n", "delete", "-host", a.String(), r.cfg.Peer.String())
			delete(r.ns, a)
		}
	}
	for a := range want {
		if !r.ns[a] {
			if _, err := r.run.Run("route", "-n", "add", "-host", a.String(), r.cfg.Peer.String()); err == nil {
				r.ns[a] = true
			}
		}
	}
}

// nameservers returns the IPv4 system nameservers that are safe to route into
// the tunnel. A nameserver that is also a gateway (typical on Wi-Fi, where
// the router is both) is skipped: a host route for it would shadow the
// link-layer entry the default route depends on.
func (r *Routes) nameservers() []netip.Addr {
	out, err := r.run.Run("getconf", "_CS_RESOLVE")
	if err != nil {
		return nil
	}
	table, err := r.run.Run("netstat", "-rn", "-f", "inet")
	if err != nil {
		return nil // cannot rule out gateways: hijack nothing
	}
	gateways := map[netip.Addr]bool{}
	for _, line := range strings.Split(table, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			if g, err := netip.ParseAddr(f[1]); err == nil {
				gateways[g] = true
			}
		}
	}
	var list []netip.Addr
	for _, f := range strings.Fields(out) {
		a, err := netip.ParseAddr(strings.TrimPrefix(f, "nameserver_"))
		if err != nil || !a.Is4() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
			continue
		}
		if gateways[a] || a == r.cfg.Local || a == r.cfg.Peer || r.cfg.Range.Contains(a) {
			continue
		}
		list = append(list, a)
	}
	return list
}

// Down removes every route and the interface address. It always attempts
// every step and is safe to call repeatedly.
func (r *Routes) Down() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.down()
}

func (r *Routes) down() {
	if !r.up {
		return
	}
	c := r.cfg
	for a := range r.ns {
		r.run.Run("route", "-n", "delete", "-host", a.String(), c.Peer.String())
		delete(r.ns, a)
	}
	r.run.Run("route", "-n", "delete", "-net", c.Range.Addr().String(), "-netmask", mask(c.Range.Bits()), c.Peer.String())
	r.run.Run("ifconfig", c.Tun, "inet", c.Local.String(), "delete")
	r.run.Run("ifconfig", c.Tun, "down")
	r.up = false
}

// StartReconcile runs Reconcile() every cfg.Reconcile until stop is closed. It
// is a no-op (starting nothing) when reconciliation is not configured. Safe to
// run alongside Down(): Reconcile and Down share r.mu and Reconcile does
// nothing once r.up is false, so a tick racing teardown cannot resurrect routes.
func (r *Routes) StartReconcile(stop <-chan struct{}) {
	if r.cfg.Reconcile <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(r.cfg.Reconcile)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r.Reconcile()
			}
		}
	}()
}

// Serve handles one client connection: "UP\n" installs, "DOWN\n" or the
// connection closing for any reason removes. It returns when the connection
// is finished and the routes are gone.
func (r *Routes) Serve(conn net.Conn) {
	defer conn.Close()
	defer r.Down()
	stop := make(chan struct{})
	defer close(stop)
	lines := bufio.NewScanner(conn)
	for lines.Scan() {
		switch strings.TrimSpace(lines.Text()) {
		case "UP":
			n, err := r.Up()
			if err != nil {
				fmt.Fprintf(conn, "ERR %v\n", err)
				return
			}
			fmt.Fprintf(conn, "OK nameservers=%d\n", n)
			r.StartReconcile(stop)
		case "DOWN":
			r.Down()
			fmt.Fprintln(conn, "OK down")
			return
		default:
			fmt.Fprintln(conn, "ERR unknown command")
			return
		}
	}
}
