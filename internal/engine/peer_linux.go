//go:build linux

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
	err = raw.Control(func(fd uintptr) {
		cred, e := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		queryErr = e
		if e == nil {
			pid = int(cred.Pid)
		}
	})
	if err != nil {
		return 0, err
	}
	return pid, queryErr
}
