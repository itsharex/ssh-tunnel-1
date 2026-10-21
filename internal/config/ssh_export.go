package config

import (
	"fmt"
	"net"
	"strings"
)

// ToSSHCommand 把隧道配置转换为等价的 ssh 命令行
// 例: ssh -N -L 8080:localhost:80 -i ~/.ssh/id_rsa user@host -p 22
func ToSSHCommand(t Tunnel) string {
	var parts []string
	parts = append(parts, "ssh", "-N")

	for _, f := range t.Forwards {
		switch f.Type {
		case ForwardLocal:
			parts = append(parts, "-L", forwardLocalArg(f))
		case ForwardRemote:
			parts = append(parts, "-R", forwardRemoteArg(f))
		case ForwardDynamic:
			parts = append(parts, "-D", f.Listen)
		}
	}

	if t.AuthType == AuthKey && t.KeyPath != "" {
		parts = append(parts, "-i", shellQuote(t.KeyPath))
	}

	if t.Port != 0 && t.Port != 22 {
		parts = append(parts, "-p", fmt.Sprintf("%d", t.Port))
	}

	parts = append(parts, fmt.Sprintf("%s@%s", t.User, t.Host))
	return strings.Join(parts, " ")
}

// forwardLocalArg 还原 -L 参数: [bind:]port:host:hport
func forwardLocalArg(f Forward) string {
	listen := f.Listen
	target := f.Target
	lh, lp := splitHostPort(listen)
	th, tp := splitHostPort(target)
	if lh == "" || lh == "127.0.0.1" || lh == "localhost" {
		// 简写：省略 bind
		return fmt.Sprintf("%s:%s:%s", lp, th, tp)
	}
	return fmt.Sprintf("%s:%s:%s:%s", lh, lp, th, tp)
}

// forwardRemoteArg 还原 -R 参数
func forwardRemoteArg(f Forward) string {
	listen := f.Listen
	target := f.Target
	lh, lp := splitHostPort(listen)
	th, tp := splitHostPort(target)
	if lh == "" || lh == "127.0.0.1" || lh == "localhost" {
		return fmt.Sprintf("%s:%s:%s", lp, th, tp)
	}
	return fmt.Sprintf("%s:%s:%s:%s", lh, lp, th, tp)
}

func splitHostPort(addr string) (host, port string) {
	if h, p, err := net.SplitHostPort(addr); err == nil {
		// net.SplitHostPort 会剥掉 IPv6 的方括号，这里还原成 ssh 需要的 [::1] 形式，
		// 否则 "::1:8080" 这种输出会被 ssh 当成非法参数。
		if strings.Contains(h, ":") {
			h = "[" + h + "]"
		}
		return h, p
	}
	// 兼容两种非标准写法：省略 host 的简写（"8080"）和没加方括号的 IPv6（"::1:8080"）
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return "", addr
	}
	h, p := addr[:idx], addr[idx+1:]
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	return h, p
}

// shellQuote 仅在参数含 shell 特殊字符时加单引号，
// 避免密钥路径里有空格时导出的命令不可用。
// 注意不能把 ~ 当作特殊字符：单引号内的 ~ 不会被 shell 展开，
// 会把 ~/.ssh/id_rsa 变成字面量路径。
func shellQuote(s string) string {
	if s == "" || !strings.ContainsAny(s, " \t\"'\\$&|;<>()*?[]{}!`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
