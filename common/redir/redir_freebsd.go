//go:build freebsd

package redir

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"syscall"
	"unsafe"

	M "github.com/sagernet/sing/common/metadata"
)

const (
	PF_IN       = 0x1
	PF_OUT      = 0x2
	DIOCNATLOOK = 0xc04c4417 // net/pfvar.h
)

// GetOriginalDestination returns the destination address before a pf rdr
// (redirect) rule was applied. Falls back to ipfw semantics (the local
// address) when pf is not available.
func GetOriginalDestination(conn net.Conn) (destination netip.AddrPort, err error) {
	_, err = os.Stat("/dev/pf")
	if errors.Is(err, os.ErrNotExist) {
		// ipfw
		la := conn.LocalAddr().(*net.TCPAddr)
		var ip net.IP
		if la.IP.To4() != nil {
			ip = make(net.IP, net.IPv4len)
			copy(ip, la.IP.To4())
		} else {
			ip = make(net.IP, net.IPv6len)
			copy(ip, la.IP)
		}
		destination = netip.AddrPortFrom(M.AddrFromIP(ip), uint16(la.Port))
		return destination, nil
	}

	// pf
	fd, err := syscall.Open("/dev/pf", 0, syscall.O_RDWR)
	if err != nil {
		return netip.AddrPort{}, err
	}
	defer syscall.Close(fd)
	nl := struct {
		saddr, daddr, rsaddr, rdaddr [16]byte
		sport, dport, rsport, rdport [2]byte
		af, proto, direction         uint8
	}{
		af:        syscall.AF_INET,
		proto:     syscall.IPPROTO_TCP,
		direction: PF_OUT,
	}
	la := conn.LocalAddr().(*net.TCPAddr)
	ra := conn.RemoteAddr().(*net.TCPAddr)
	raIP, laIP := ra.IP, la.IP
	raPort, laPort := ra.Port, la.Port
	switch {
	case raIP.To4() != nil:
		copy(nl.saddr[:net.IPv4len], raIP.To4())
		copy(nl.daddr[:net.IPv4len], laIP.To4())
		nl.af = syscall.AF_INET
	default:
		copy(nl.saddr[:], raIP.To16())
		copy(nl.daddr[:], laIP.To16())
		nl.af = syscall.AF_INET6
	}
	binary.BigEndian.PutUint16(nl.sport[:], uint16(raPort))
	binary.BigEndian.PutUint16(nl.dport[:], uint16(laPort))
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), DIOCNATLOOK, uintptr(unsafe.Pointer(&nl))); errno != 0 {
		return netip.AddrPort{}, errno
	}

	var ip net.IP
	switch nl.af {
	case syscall.AF_INET:
		ip = make(net.IP, net.IPv4len)
		copy(ip, nl.rdaddr[:net.IPv4len])
	case syscall.AF_INET6:
		ip = make(net.IP, net.IPv6len)
		copy(ip, nl.rdaddr[:])
	}
	port := binary.BigEndian.Uint16(nl.rdport[:])
	destination = netip.AddrPortFrom(M.AddrFromIP(ip), port)
	return
}
