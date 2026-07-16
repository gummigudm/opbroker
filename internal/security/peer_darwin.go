//go:build darwin

// Package security provides caller verification for Unix domain socket
// connections on macOS.
package security

/*
#include <libproc.h>
#include <sys/proc_info.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// LocalPeerPID is the macOS getsockopt name for peer PID on AF_UNIX sockets.
// Defined in <sys/un.h> as LOCAL_PEERPID = 0x002.
const localPeerPID = 0x002

// PeerInfo describes the process on the other end of a Unix socket connection.
type PeerInfo struct {
	PID     int
	ExePath string
	EUID    uint32
}

// InspectPeer returns the peer PID, executable path, and effective UID.
func InspectPeer(conn net.Conn) (*PeerInfo, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("not a unix connection: %T", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("syscall conn: %w", err)
	}

	var pid int
	var euid uint32
	var innerErr error
	if err := raw.Control(func(fd uintptr) {
		p, e := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, localPeerPID)
		if e != nil {
			innerErr = fmt.Errorf("getsockopt LOCAL_PEERPID: %w", e)
			return
		}
		pid = p

		// Peer effective UID.
		ucred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			innerErr = fmt.Errorf("getsockopt LOCAL_PEERCRED: %w", e)
			return
		}
		euid = ucred.Uid
	}); err != nil {
		return nil, fmt.Errorf("raw control: %w", err)
	}
	if innerErr != nil {
		return nil, innerErr
	}

	exe, err := pidPath(pid)
	if err != nil {
		return nil, err
	}
	return &PeerInfo{PID: pid, ExePath: exe, EUID: euid}, nil
}

// pidPath calls proc_pidpath(2) to resolve a PID to its executable path.
func pidPath(pid int) (string, error) {
	var buf [C.PROC_PIDPATHINFO_MAXSIZE]byte
	n, err := C.proc_pidpath(C.int(pid), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf)))
	if n <= 0 {
		if err == nil {
			err = syscall.EINVAL
		}
		return "", fmt.Errorf("proc_pidpath(%d): %w", pid, err)
	}
	return string(buf[:n]), nil
}

// VerifyPeer inspects the caller of conn and confirms the executable path
// is in allowedPaths and the effective UID matches the current process.
func VerifyPeer(conn net.Conn, allowedPaths []string) (*PeerInfo, error) {
	info, err := InspectPeer(conn)
	if err != nil {
		return nil, err
	}
	if info.EUID != uint32(os.Getuid()) {
		return info, fmt.Errorf("peer euid %d != agent uid %d", info.EUID, os.Getuid())
	}
	if len(allowedPaths) == 0 {
		return nil, errors.New("no allowed_callers configured")
	}
	realPeer, err := filepath.EvalSymlinks(info.ExePath)
	if err != nil {
		realPeer = info.ExePath
	}
	for _, p := range allowedPaths {
		if p == info.ExePath || p == realPeer {
			return info, nil
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			if resolved == info.ExePath || resolved == realPeer {
				return info, nil
			}
		}
	}
	return info, fmt.Errorf("peer %s (pid=%d) not in allowed_callers", info.ExePath, info.PID)
}
