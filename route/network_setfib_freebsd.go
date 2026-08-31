//go:build freebsd

package route

import (
	"net"
	"net/netip"
	"os"
	"syscall"
	"unsafe"

	"github.com/sagernet/sing/common/control"
	E "github.com/sagernet/sing/common/exceptions"

	"golang.org/x/net/route"
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

// setupOutputFIB prepares the dedicated FIB eagerly at registration time:
// socket binding becomes active as soon as the tun inbound is configured,
// while the tun device (and the capture routes) only come up when the
// inbound starts, so the FIB must exist before any other dialer activity
// (rule-set initialization, DNS, etc.) takes place. The same preparation
// runs again (idempotently) when the tun device is created.
func setupOutputFIB(fib int) error {
	addAddrAllFibs, err := unix.SysctlUint32("net.add_addr_allfibs")
	if err != nil {
		return err
	}
	if addAddrAllFibs == 0 {
		err = sysctlWriteUint32("net.add_addr_allfibs", 1)
		if err != nil {
			return err
		}
	}

	fibs, err := unix.SysctlUint32("net.fibs")
	if err != nil {
		return err
	}
	if fibs < uint32(fib+1) {
		err = sysctlWriteUint32("net.fibs", uint32(fib+1))
		if err != nil {
			return E.New(
				"failed to set net.fibs=", fib+1, ": ", err,
				". Please add `net.fibs=", fib+1,
				"` and `net.add_addr_allfibs=1` to /boot/loader.conf and reboot",
			)
		}
	}

	gateway4, gateway6, gateway6Index, err := findDefaultGateways()
	if err != nil {
		return err
	}
	if gateway4.IsValid() {
		// Idempotent: remove a stale default route left by a previous run.
		_ = execRoute(fib, unix.RTM_DELETE, netip.PrefixFrom(netip.IPv4Unspecified(), 0), gateway4, 0)
		err = execRoute(fib, unix.RTM_ADD, netip.PrefixFrom(netip.IPv4Unspecified(), 0), gateway4, 0)
		if err != nil {
			return E.Cause(err, "copy IPv4 default route to FIB ", fib)
		}
	}
	if gateway6.IsValid() {
		_ = execRoute(fib, unix.RTM_DELETE, netip.PrefixFrom(netip.IPv6Unspecified(), 0), gateway6, gateway6Index)
		err = execRoute(fib, unix.RTM_ADD, netip.PrefixFrom(netip.IPv6Unspecified(), 0), gateway6, gateway6Index)
		if err != nil {
			return E.Cause(err, "copy IPv6 default route to FIB ", fib)
		}
	}
	return nil
}

func sysctlWriteUint32(name string, value uint32) error {
	_, _, errno := unix.Syscall6(
		unix.SYS___SYSCTLBYNAME,
		uintptr(unsafe.Pointer(unsafe.StringData(name))),
		uintptr(len(name)),
		0,
		0,
		uintptr(unsafe.Pointer(&value)),
		uintptr(unsafe.Sizeof(value)),
	)
	if errno != 0 {
		return os.NewSyscallError("sysctlbyname "+name, errno)
	}
	return nil
}

func findDefaultGateways() (gateway4 netip.Addr, gateway6 netip.Addr, gateway6Index int, err error) {
	ribMessage, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return
	}
	routeMessages, err := route.ParseRIB(route.RIBTypeRoute, ribMessage)
	if err != nil {
		return
	}
	for _, rawRouteMessage := range routeMessages {
		routeMessage, isRouteMessage := rawRouteMessage.(*route.RouteMessage)
		if !isRouteMessage {
			continue
		}
		if routeMessage.Flags&unix.RTF_UP == 0 || routeMessage.Flags&unix.RTF_GATEWAY == 0 {
			continue
		}
		if len(routeMessage.Addrs) <= unix.RTAX_NETMASK {
			continue
		}
		if netmask, isIPv4Mask := routeMessage.Addrs[unix.RTAX_NETMASK].(*route.Inet4Addr); isIPv4Mask {
			if ones, _ := net.IPMask(netmask.IP[:]).Size(); ones != 0 {
				continue
			}
			gateway, isIPv4Gateway := routeMessage.Addrs[unix.RTAX_GATEWAY].(*route.Inet4Addr)
			if isIPv4Gateway {
				gateway4 = netip.AddrFrom4(gateway.IP)
			}
			continue
		}
		if netmask, isIPv6Mask := routeMessage.Addrs[unix.RTAX_NETMASK].(*route.Inet6Addr); isIPv6Mask {
			if ones, _ := net.IPMask(netmask.IP[:]).Size(); ones != 0 {
				continue
			}
			gateway, isIPv6Gateway := routeMessage.Addrs[unix.RTAX_GATEWAY].(*route.Inet6Addr)
			if isIPv6Gateway {
				gateway6 = netip.AddrFrom16(gateway.IP)
				gateway6Index = routeMessage.Index
			}
		}
	}
	return
}

// execRoute writes a route message into the routing socket.
// fib selects the target routing table (SO_SETFIB).
func execRoute(fib int, rtmType int, destination netip.Prefix, gateway netip.Addr, gatewayIndex int) error {
	routeMessage := route.RouteMessage{
		Type:    rtmType,
		Version: unix.RTM_VERSION,
		Flags:   unix.RTF_STATIC | unix.RTF_GATEWAY,
		Seq:     1,
	}
	if rtmType == unix.RTM_ADD {
		routeMessage.Flags |= unix.RTF_UP
	}
	if gateway.Is4() {
		routeMessage.Addrs = []route.Addr{
			syscall.RTAX_DST:     &route.Inet4Addr{IP: destination.Addr().As4()},
			syscall.RTAX_NETMASK: &route.Inet4Addr{IP: netip.MustParseAddr(net.IP(net.CIDRMask(destination.Bits(), 32)).String()).As4()},
			syscall.RTAX_GATEWAY: &route.Inet4Addr{IP: gateway.As4()},
		}
	} else {
		routeMessage.Addrs = []route.Addr{
			syscall.RTAX_DST:     &route.Inet6Addr{IP: destination.Addr().As16()},
			syscall.RTAX_NETMASK: &route.Inet6Addr{IP: netip.MustParseAddr(net.IP(net.CIDRMask(destination.Bits(), 128)).String()).As16()},
			syscall.RTAX_GATEWAY: &route.Inet6Addr{IP: gateway.As16()},
		}
		if gatewayIndex != 0 {
			// Scope the gateway to its interface, which is required for
			// link-local IPv6 gateways.
			routeMessage.Addrs[syscall.RTAX_IFP] = &route.LinkAddr{Index: gatewayIndex}
		}
	}
	request, err := routeMessage.Marshal()
	if err != nil {
		return err
	}
	return useSocket(unix.AF_ROUTE, unix.SOCK_RAW, 0, func(socketFd int) error {
		err := unix.SetsockoptInt(socketFd, unix.SOL_SOCKET, unix.SO_SETFIB, fib)
		if err != nil {
			return os.NewSyscallError("SO_SETFIB", err)
		}
		n, err := unix.Write(socketFd, request)
		if err != nil {
			return os.NewSyscallError("write route", err)
		}
		if n != len(request) {
			return syscall.Errno(syscall.EIO)
		}
		return nil
	})
}

func useSocket(domain, typ, proto int, block func(socketFd int) error) error {
	socketFd, err := unix.Socket(domain, typ, proto)
	if err != nil {
		return err
	}
	defer unix.Close(socketFd)
	return block(socketFd)
}
