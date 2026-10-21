//go:build darwin

package proc

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <arpa/inet.h>
#include <stdlib.h>
#include <string.h>

// collect_sockets 遍历所有进程的 socket fd，把 TCP 连接的 (本地端口, pid)
// 写入调用方给的数组，返回总条数。总条数可能大于 cap，调用方据此扩容重试。
//
// 结构体字段依据 <sys/proc_info.h>：
//   socket_fdinfo.psi.soi_kind            == SOCKINFO_TCP 表示 TCP
//   socket_info.soi_proto.pri_tcp.tcpsi_ini.insi_lport 为网络序端口
static int collect_sockets(uint32_t *ports, int32_t *pids, int cap) {
	int n = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
	if (n <= 0) return -1;
	int capPid = n / (int)sizeof(pid_t) + 64;
	pid_t *plist = (pid_t *)malloc(sizeof(pid_t) * capPid);
	if (plist == NULL) return -1;
	int got = proc_listpids(PROC_ALL_PIDS, 0, plist, sizeof(pid_t) * capPid);
	int count = got / (int)sizeof(pid_t);
	int out = 0;
	for (int i = 0; i < count; i++) {
		pid_t pid = plist[i];
		if (pid <= 0) continue;
		int bytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
		if (bytes <= 0) continue;
		struct proc_fdinfo *fds = (struct proc_fdinfo *)malloc(bytes);
		if (fds == NULL) continue;
		int r = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds, bytes);
		if (r > 0) {
			int nfd = r / (int)sizeof(struct proc_fdinfo);
			for (int j = 0; j < nfd; j++) {
				if (fds[j].proc_fdtype != PROX_FDTYPE_SOCKET) continue;
				struct socket_fdinfo si;
				memset(&si, 0, sizeof(si));
				if (proc_pidfdinfo(pid, fds[j].proc_fd, PROC_PIDFDSOCKETINFO, &si, sizeof(si)) <= 0) continue;
				if (si.psi.soi_kind != SOCKINFO_TCP) continue;
				if (si.psi.soi_family != AF_INET && si.psi.soi_family != AF_INET6) continue;
				uint16_t lport = ntohs((uint16_t)si.psi.soi_proto.pri_tcp.tcpsi_ini.insi_lport);
				if (lport == 0) continue;
				if (out < cap) {
					ports[out] = lport;
					pids[out] = pid;
				}
				out++;
			}
		}
		free(fds);
	}
	free(plist);
	return out;
}

static int name_of(int32_t pid, char *buf, int len) {
	return proc_name(pid, buf, len);
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

func localOwners() (map[uint16]Owner, error) {
	capN := 4096
	var (
		ports []uint32
		pids  []int32
		n     int
	)
	// 最多重试 4 轮：并发高时 socket 数量会在两次调用之间增长
	for attempt := 0; attempt < 4; attempt++ {
		ports = make([]uint32, capN)
		pids = make([]int32, capN)
		n = int(C.collect_sockets(
			(*C.uint32_t)(unsafe.Pointer(&ports[0])),
			(*C.int32_t)(unsafe.Pointer(&pids[0])),
			C.int(capN),
		))
		if n < 0 {
			return nil, errors.New("proc_listpids 调用失败")
		}
		if n <= capN {
			break
		}
		capN = n + 1024
	}
	if n > capN {
		n = capN
	}

	// 同一个 pid 可能有多条连接，进程名按 pid 缓存，避免重复调用 proc_name
	names := make(map[int32]string, 64)
	out := make(map[uint16]Owner, n)
	for i := 0; i < n; i++ {
		pid := pids[i]
		name, ok := names[pid]
		if !ok {
			name = procName(pid)
			names[pid] = name
		}
		out[uint16(ports[i])] = Owner{PID: int(pid), Name: name}
	}
	return out, nil
}

func procName(pid int32) string {
	var buf [256]C.char
	if C.name_of(C.int32_t(pid), &buf[0], C.int(len(buf))) <= 0 {
		return ""
	}
	return C.GoString(&buf[0])
}
