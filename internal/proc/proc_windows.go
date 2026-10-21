//go:build windows

package proc

import (
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	tcpTableOwnerPidAll   = 5
	afInet                = 2
	afInet6               = 23
	errInsufficientBuffer = 122
	// PROCESS_QUERY_LIMITED_INFORMATION，Vista 及以上即可用，无需提权
	processQueryLimitedInformation = 0x1000
)

var (
	modiphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetExtendedTcpTable        = modiphlpapi.NewProc("GetExtendedTcpTable")
	procOpenProcess                = modkernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = modkernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                = modkernel32.NewProc("CloseHandle")
)

// MIB_TCPROW_OWNER_PID
type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

// MIB_TCP6ROW_OWNER_PID
type mibTCP6RowOwnerPID struct {
	LocalAddr     [16]byte
	LocalScopeID  uint32
	LocalPort     uint32
	RemoteAddr    [16]byte
	RemoteScopeID uint32
	RemotePort    uint32
	State         uint32
	OwningPID     uint32
}

func localOwners() (map[uint16]Owner, error) {
	out := make(map[uint16]Owner, 256)
	names := make(map[uint32]string, 64)

	for _, fam := range []uint32{afInet, afInet6} {
		buf, err := tcpTable(fam)
		if err != nil || len(buf) < 4 {
			continue
		}
		rows := *(*uint32)(unsafe.Pointer(&buf[0]))
		rowSize := int(unsafe.Sizeof(mibTCPRowOwnerPID{}))
		if fam == afInet6 {
			rowSize = int(unsafe.Sizeof(mibTCP6RowOwnerPID{}))
		}
		for i := uint32(0); i < rows; i++ {
			off := 4 + int(i)*rowSize
			if off+rowSize > len(buf) {
				break
			}
			var port uint16
			var pid uint32
			if fam == afInet {
				row := (*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[off]))
				port = ntohs(uint16(row.LocalPort))
				pid = row.OwningPID
			} else {
				row := (*mibTCP6RowOwnerPID)(unsafe.Pointer(&buf[off]))
				port = ntohs(uint16(row.LocalPort))
				pid = row.OwningPID
			}
			if port == 0 || pid == 0 {
				continue
			}
			name, ok := names[pid]
			if !ok {
				name = processName(pid)
				names[pid] = name
			}
			out[port] = Owner{PID: int(pid), Name: name}
		}
	}
	return out, nil
}

// tcpTable 调用 GetExtendedTcpTable 取回带 PID 的整张 TCP 连接表。
// Windows 上这一步是最省事的：一次系统调用就拿到全部连接及其所属进程。
func tcpTable(family uint32) ([]byte, error) {
	var size uint32
	r, _, _ := procGetExtendedTcpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(family),
		uintptr(tcpTableOwnerPidAll),
		0,
	)
	if r != errInsufficientBuffer && r != 0 {
		return nil, syscall.Errno(r)
	}
	if size == 0 {
		return nil, errors.New("连接表为空")
	}
	buf := make([]byte, size)
	r, _, _ = procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(family),
		uintptr(tcpTableOwnerPidAll),
		0,
	)
	if r != 0 {
		return nil, syscall.Errno(r)
	}
	return buf[:size], nil
}

// ntohs 网络序转主机序（Windows 是小端）
func ntohs(v uint16) uint16 { return v>>8 | v<<8 }

func processName(pid uint32) string {
	h, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInformation), 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)

	buf := make([]uint16, 512)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(
		h,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 || size == 0 {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}
