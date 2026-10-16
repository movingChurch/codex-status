//go:build darwin

package engine

import (
	"net"
	"syscall"
)

func peerPID(conn net.Conn) (int, error) {
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var queryErr error
	err = raw.Control(func(fd uintptr) { pid, queryErr = syscall.GetsockoptInt(int(fd), 0, 0x002) }) // SDK sys/un.h LOCAL_PEERPID
	if err != nil {
		return 0, err
	}
	return pid, queryErr
}
