package backend

import (
	"errors"
	"fmt"
)

// ExperimentEnv must be set to "1" in the process environment before the
// sing-tun entry point will even consider opening a device.
const ExperimentEnv = "BB10_TUN_EXPERIMENT"

// MaxMTU is the interface MTU observed on the phone. The backend cannot query
// the interface, and the driver rejects larger writes.
const MaxMTU = 1500

// Request is the subset of sing-tun options that matter on BB10, flattened so
// the policy can be tested on any host.
type Request struct {
	Name                 string
	MTU                  uint32
	AutoRoute            bool
	StrictRoute          bool
	GSO                  bool
	AutoRedirectMarkMode bool
	FileDescriptor       int
	Inet6Addresses       int
	// RouteEntries counts every route / include / exclude selector. This
	// backend installs no routes, so accepting them would silently ignore
	// what the user asked for.
	RouteEntries int
}

var ErrUnsupportedOption = errors.New("qnx tun: option not supported on BB10")

// Config turns a request into a backend Config or refuses it. Nothing is
// downgraded silently: an option that cannot be honoured is an error.
func (r Request) Config(getenv func(string) string) (Config, error) {
	if getenv == nil || getenv(ExperimentEnv) != "1" {
		return Config{}, fmt.Errorf("%w (%s is not 1)", ErrRealDeviceDisabled, ExperimentEnv)
	}
	reject := func(what string) (Config, error) {
		return Config{}, fmt.Errorf("%w: %s", ErrUnsupportedOption, what)
	}
	switch {
	case r.AutoRoute:
		return reject("auto-route (routes are never installed by this backend)")
	case r.StrictRoute:
		return reject("strict-route (no fail-closed mechanism exists)")
	case r.AutoRedirectMarkMode:
		return reject("auto-redirect")
	case r.GSO:
		return reject("gso")
	case r.FileDescriptor != 0:
		return reject("file-descriptor")
	case r.Inet6Addresses != 0:
		return reject("inet6-address (IPv6 routing on this phone carries IMS; the TUN must stay IPv4-only)")
	case r.RouteEntries != 0:
		return reject("route / include / exclude lists")
	case r.MTU == 0 || r.MTU > MaxMTU:
		return reject(fmt.Sprintf("mtu %d (set it explicitly, at most %d)", r.MTU, MaxMTU))
	}
	cfg := Config{Path: "/dev/" + r.Name, MTU: int(r.MTU), AcknowledgeRealInterface: true}
	if err := cfg.normalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
