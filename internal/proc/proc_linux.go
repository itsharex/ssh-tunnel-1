//go:build linux

package proc

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func localOwners() (map[uint16]Owner, error) {
	// 1) /proc/net/tcp{,6} 提供「本地端口 ↔ socket inode」，但没有 pid
	inodePort := make(map[string]uint16, 256)
	scanProcNet("/proc/net/tcp", inodePort)
	scanProcNet("/proc/net/tcp6", inodePort)
	if len(inodePort) == 0 {
		return nil, nil
	}

	// 2) 扫 /proc/<pid>/fd/*，把 socket:[inode] 反查成 pid
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, 64)
	out := make(map[uint16]Owner, len(inodePort))
	for _, p := range procs {
		if len(out) >= len(inodePort) {
			break // 已经全部命中，不必继续扫
		}
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue // 非进程目录
		}
		fdDir := filepath.Join("/proc", p.Name(), "fd")
		ents, err := os.ReadDir(fdDir)
		if err != nil {
			continue // 其他用户的进程，无权读取
		}
		for _, e := range ents {
			link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(link)
			if !ok {
				continue
			}
			port, ok := inodePort[inode]
			if !ok {
				continue
			}
			name, ok := names[p.Name()]
			if !ok {
				name = readComm(p.Name())
				names[p.Name()] = name
			}
			out[port] = Owner{PID: pid, Name: name}
		}
	}
	return out, nil
}

// socketInode 从 "socket:[12345]" 里取出 inode
func socketInode(link string) (string, bool) {
	const prefix = "socket:["
	if !strings.HasPrefix(link, prefix) || !strings.HasSuffix(link, "]") {
		return "", false
	}
	return link[len(prefix) : len(link)-1], true
}

// scanProcNet 解析 /proc/net/tcp 格式：
//
//	sl  local_address rem_address st ... uid timeout inode
//	0:  0100007F:1F90 00000000:0000 0A ... 1000 0       12345 1 ...
func scanProcNet(path string, inodePort map[string]uint16) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Scan() // 跳过表头
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		i := strings.LastIndexByte(fields[1], ':')
		if i < 0 {
			continue
		}
		p, err := strconv.ParseUint(fields[1][i+1:], 16, 16)
		if err != nil {
			continue
		}
		inodePort[fields[9]] = uint16(p)
	}
}

func readComm(pid string) string {
	b, err := os.ReadFile(filepath.Join("/proc", pid, "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
