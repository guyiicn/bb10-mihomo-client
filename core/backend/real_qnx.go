//go:build qnx && bb10_real_tun

package backend

import (
	"time"

	"golang.org/x/sys/unix"
)

// UNTESTED ON HARDWARE. Compiled only with -tags bb10_real_tun. Opening the
// node creates a tunN interface in io-pkt; run it only under an approved,
// time-boxed test plan (see ../TUN-DRIVER-STATIC-20261115.md, "真机 TUN 探测计划").
type realKernel struct{}

func (realKernel) Open(path string) (int, error) {
	return unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
}
func (realKernel) IoctlSetInt(fd int, req uint, v int) error {
	return unix.IoctlSetPointerInt(fd, req, v)
}
func (realKernel) IoctlGetInt(fd int, req uint) (int, error) { return unix.IoctlGetInt(fd, req) }
func (realKernel) WaitReadable(fd int, timeout time.Duration) (bool, error) {
	var r unix.FdSet
	r.Set(fd)
	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	n, err := unix.Select(fd+1, &r, nil, nil, &tv)
	return n > 0 && r.IsSet(fd), err
}
func (realKernel) Read(fd int, p []byte) (int, error)  { return unix.Read(fd, p) }
func (realKernel) Write(fd int, p []byte) (int, error) { return unix.Write(fd, p) }
func (realKernel) Close(fd int) error                  { return unix.Close(fd) }

func OpenReal(cfg Config) (*Tun, error) {
	if !cfg.AcknowledgeRealInterface {
		return nil, ErrRealDeviceDisabled
	}
	return Open(realKernel{}, cfg)
}
