//go:build !freebsd

package route

import (
	"syscall"

	"github.com/sagernet/sing/common/control"
)

func setSocketFIB(fib int) control.Func {
	return func(network, address string, conn syscall.RawConn) error {
		return nil
	}
}

func setupOutputFIB(fib int) error {
	return nil
}
