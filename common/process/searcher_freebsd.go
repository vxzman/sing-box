//go:build freebsd

package process

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
)

// Implementation based on the bsd-box project
// (https://github.com/Vincent-Loeng/bsd-box).

var _ Searcher = (*freebsdSearcher)(nil)

type freebsdSearcher struct {
	access sync.Mutex
	// Cache of pid -> executable path.
	pathCache map[uint32]string
	// Cache of recent lookups to avoid repeated pcblist scans.
	resultCache map[lookupKey]resultEntry
}

type lookupKey struct {
	network     string
	source      netip.AddrPort
	destination netip.AddrPort
}

type resultEntry struct {
	owner      adapter.ConnectionOwner
	expiration time.Time
}

const (
	resultCacheCapacity = 256
	resultCacheTTL      = 30 * time.Second
)

func NewSearcher(_ Config) (Searcher, error) {
	if err := initOffsets(); err != nil {
		return nil, err
	}
	return &freebsdSearcher{
		pathCache:   make(map[uint32]string),
		resultCache: make(map[lookupKey]resultEntry),
	}, nil
}

func (s *freebsdSearcher) ResetCache() {
	s.access.Lock()
	s.resultCache = make(map[lookupKey]resultEntry)
	s.access.Unlock()
}

func (s *freebsdSearcher) Close() error {
	s.access.Lock()
	s.pathCache = nil
	s.resultCache = nil
	s.access.Unlock()
	return nil
}

func (s *freebsdSearcher) FindProcessInfo(ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error) {
	key := lookupKey{network, source, destination}
	now := time.Now()
	s.access.Lock()
	if entry, loaded := s.resultCache[key]; loaded && entry.expiration.After(now) {
		owner := entry.owner
		s.access.Unlock()
		return &owner, nil
	}
	s.access.Unlock()

	processName, err := findProcessName(network, source.Addr(), int(source.Port()), s.pathCache)
	if err != nil {
		return nil, err
	}
	owner := &adapter.ConnectionOwner{ProcessPath: processName, UserId: -1}

	s.access.Lock()
	if len(s.resultCache) >= resultCacheCapacity {
		for cachedKey := range s.resultCache {
			delete(s.resultCache, cachedKey)
			break
		}
	}
	s.resultCache[key] = resultEntry{owner: *owner, expiration: now.Add(resultCacheTTL)}
	s.access.Unlock()
	return owner, nil
}

func findProcessName(network string, ip netip.Addr, srcPort int, pathCache map[uint32]string) (string, error) {
	var spath string
	switch network {
	case "tcp":
		spath = "net.inet.tcp.pcblist"
	case "udp":
		spath = "net.inet.udp.pcblist"
	default:
		return "", E.New("invalid network")
	}

	value, err := syscall.Sysctl(spath)
	if err != nil {
		return "", err
	}

	buf := []byte(value)
	socket, err := searchSocket(buf, ip, uint16(srcPort), network == "tcp")
	if err != nil {
		return "", err
	}
	pid, err := searchSocketPid(socket)
	if err != nil {
		return "", err
	}

	if processPath, loaded := pathCache[pid]; loaded {
		return processPath, nil
	}
	processPath, err := getExecPathFromPID(pid)
	if err != nil {
		return "", err
	}
	pathCache[pid] = processPath
	return processPath, nil
}

func getExecPathFromPID(pid uint32) (string, error) {
	buf := make([]byte, 2048)
	size := uint64(len(buf))
	// CTL_KERN, KERN_PROC, KERN_PROC_PATHNAME, pid
	mib := [4]uint32{1, 14, 12, pid}

	_, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		0)
	if errno != 0 || size == 0 {
		return "", errno
	}

	return string(buf[:size-1]), nil
}

func readNativeUint32(b []byte) uint32 {
	return *(*uint32)(unsafe.Pointer(&b[0]))
}

// searcher holds the sizes and offsets of the kernel structures returned
// by the pcblist and kern.file sysctls. They are ABI-stable within a major
// FreeBSD release line but must be revalidated for new major versions.
type searcher struct {
	// sizeof(struct xinpgen)
	headSize int
	// sizeof(struct xtcpcb)
	tcpItemSize int
	// sizeof(struct xinpcb)
	udpItemSize int
	udpInpOffset int
	port         int
	ip           int
	vflag        int
	socket       int

	// sizeof(struct xfile)
	fileItemSize int
	data         int
	pid          int
}

var defaultSearcher *searcher

func initOffsets() error {
	osRelease, err := syscall.Sysctl("kern.osrelease")
	if err != nil {
		return err
	}
	machine, err := syscall.Sysctl("hw.machine")
	if err != nil {
		return err
	}

	if dot := strings.Index(osRelease, "."); dot != -1 {
		osRelease = osRelease[:dot]
	}
	major, err := strconv.Atoi(osRelease)
	if err != nil {
		return err
	}
	defaultSearcher = newSearcher(major, machine)
	if defaultSearcher == nil {
		return fmt.Errorf("unsupported FreeBSD version %d (%s)", major, machine)
	}
	return nil
}

func newSearcher(major int, machine string) *searcher {
	// The layout of xinpgen/xinpcb/xtcpcb/xfile is identical on the 64-bit
	// (LP64) targets. 32-bit targets have different pointer and ksize_t
	// widths and are not supported.
	switch machine {
	case "amd64", "arm64", "riscv":
	default:
		return nil
	}
	// Offsets below were verified against freebsd-src (sys/sys/socketvar.h,
	// sys/netinet/in_pcb.h, sys/netinet/tcp_var.h, sys/sys/file.h) for
	// releng/12.4, releng/13.4, releng/14.2 and main (15.0), computing
	// struct layouts with a C compiler (SysV ABI, LP64). They are identical
	// across all these versions:
	//   sizeof(xinpgen) = 64, sizeof(xinpcb) = 400, sizeof(xtcpcb) = 744,
	//   sizeof(xfile) = 128.
	//   xinpcb: xso_so @ 16, ie_lport @ 254, v4 laddr @ 284, v6 laddr @ 272,
	//           inp_vflag @ 392. Note that inp_inc occupies 48 bytes inside
	//           xinpcb due to uint64 alignment padding.
	//   xfile:  xf_pid @ 8, xf_data @ 56.
	switch major {
	case 12, 13, 14, 15:
		return &searcher{
			headSize:     64,
			tcpItemSize:  744,
			udpItemSize:  400,
			// TCP items start with xt_len (8 bytes) so the embedded xinpcb
			// begins at offset 8; UDP items are plain xinpcb and begin
			// with xi_len at offset 0.
			udpInpOffset: 0,
			port:         254,
			ip:           284,
			vflag:        392,
			socket:       16,
			fileItemSize: 128,
			data:         56,
			pid:          8,
		}
	}
	return nil
}

func searchSocket(buf []byte, ip netip.Addr, port uint16, isTCP bool) (uint64, error) {
	s := defaultSearcher
	var itemSize int
	var inpOffset int
	if isTCP {
		// struct xtcpcb
		itemSize = s.tcpItemSize
		inpOffset = 8
	} else {
		// struct xinpcb
		itemSize = s.udpItemSize
		inpOffset = s.udpInpOffset
	}

	isIPv4 := ip.Is4()
	// skip the first xinpgen block
	for i := s.headSize; i+itemSize <= len(buf); i += itemSize {
		inp := i + inpOffset

		srcPort := binary.BigEndian.Uint16(buf[inp+s.port : inp+s.port+2])
		if port != srcPort {
			continue
		}

		// xinpcb.inp_vflag
		flag := buf[inp+s.vflag]

		var srcIP netip.Addr
		switch {
		case flag&0x1 > 0 && isIPv4:
			// ipv4
			srcIP, _ = netip.AddrFromSlice(buf[inp+s.ip : inp+s.ip+4])
		case flag&0x2 > 0 && !isIPv4:
			// ipv6
			srcIP, _ = netip.AddrFromSlice(buf[inp+s.ip-12 : inp+s.ip+4])
		default:
			continue
		}
		srcIP = srcIP.Unmap()

		if ip != srcIP {
			continue
		}

		// xsocket.xso_so, a kernel pointer compared against xf_data below
		return binary.BigEndian.Uint64(buf[inp+s.socket : inp+s.socket+8]), nil
	}
	return 0, ErrNotFound
}

func searchSocketPid(socket uint64) (uint32, error) {
	s := defaultSearcher
	value, err := syscall.Sysctl("kern.file")
	if err != nil {
		return 0, err
	}

	buf := []byte(value)

	// struct xfile
	itemSize := s.fileItemSize
	for i := 0; i+itemSize <= len(buf); i += itemSize {
		// xfile.xf_data
		data := binary.BigEndian.Uint64(buf[i+s.data : i+s.data+8])
		if data == socket {
			// xfile.xf_pid
			return readNativeUint32(buf[i+s.pid : i+s.pid+4]), nil
		}
	}
	return 0, ErrNotFound
}
