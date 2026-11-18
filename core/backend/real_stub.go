//go:build !(qnx && bb10_real_tun)

package backend

// OpenReal is unavailable in default builds: no code path in this binary can
// open a TUN device.
func OpenReal(Config) (*Tun, error) { return nil, ErrRealDeviceDisabled }
