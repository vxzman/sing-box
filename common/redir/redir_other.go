//go:build !linux && !darwin && !freebsd

package redir

import (
	"net"
	"net/netip"
	"os"
)

func GetOriginalDestination(conn net.Conn) (destination netip.AddrPort, err error) {
	return netip.AddrPort{}, os.ErrInvalid
}
