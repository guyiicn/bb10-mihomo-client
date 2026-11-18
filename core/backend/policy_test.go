package backend

import (
	"errors"
	"testing"
)

func on(string) string  { return "1" }
func off(string) string { return "" }

func TestPolicyDisabledWithoutEnv(t *testing.T) {
	ok := Request{Name: "tun3", MTU: 1500}
	for _, env := range []func(string) string{nil, off, func(string) string { return "true" }} {
		if _, err := ok.Config(env); !errors.Is(err, ErrRealDeviceDisabled) {
			t.Fatalf("got %v", err)
		}
	}
	cfg, err := ok.Config(on)
	if err != nil || cfg.Path != "/dev/tun3" || cfg.MTU != 1500 || cfg.IfHead || !cfg.AcknowledgeRealInterface {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestPolicyRejectsEverythingItCannotHonour(t *testing.T) {
	for name, r := range map[string]Request{
		"auto-route":   {Name: "tun3", MTU: 1500, AutoRoute: true},
		"strict-route": {Name: "tun3", MTU: 1500, StrictRoute: true},
		"redirect":     {Name: "tun3", MTU: 1500, AutoRedirectMarkMode: true},
		"gso":          {Name: "tun3", MTU: 1500, GSO: true},
		"fd":           {Name: "tun3", MTU: 1500, FileDescriptor: 9},
		"inet6":        {Name: "tun3", MTU: 1500, Inet6Addresses: 1},
		"routes":       {Name: "tun3", MTU: 1500, RouteEntries: 1},
		"mtu-default":  {Name: "tun3"},
		"mtu-9000":     {Name: "tun3", MTU: 9000},
	} {
		if _, err := r.Config(on); !errors.Is(err, ErrUnsupportedOption) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	for _, n := range []string{"", "utun0", "tun", "tun12", "tap0", "tun0/../x"} {
		if _, err := (Request{Name: n, MTU: 1500}).Config(on); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
}
