//go:build freebsd

package route

import (
	"syscall"

	"github.com/sagernet/sing/common/control"

	"golang.org/x/sys/unix"
)

// setSocketFIB binds the socket to the dedicated routing table (FIB) used
// for loop prevention on FreeBSD: the tun capture routes live in the
// default FIB while the dedicated FIB holds a copy of the real default
// gateway, so traffic of the proxy itself never matches the capture
// routes.
func setSocketFIB(fib int) control.Func {
	return func(network, address string, conn syscall.RawConn) error {
		return control.Raw(conn, func(fd uintptr) error {
			return unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SETFIB, fib)
		})
	}
}
