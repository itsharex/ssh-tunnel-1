package config

import (
	"strings"
	"testing"
)

func TestToSSHCommand(t *testing.T) {
	base := Tunnel{
		User:     "root",
		Host:     "example.com",
		Port:     22,
		AuthType: AuthKey,
		KeyPath:  "~/.ssh/id_rsa",
		Forwards: []Forward{{Type: ForwardLocal, Listen: "8080", Target: "localhost:80"}},
	}

	got := ToSSHCommand(base)
	want := "ssh -N -L 8080:localhost:80 -i ~/.ssh/id_rsa root@example.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// 非默认端口要带上 -p，且必须在 user@host 之前
	withPort := base
	withPort.Port = 2222
	got = ToSSHCommand(withPort)
	if !strings.Contains(got, "-p 2222") || strings.Index(got, "-p 2222") > strings.Index(got, "root@example.com") {
		t.Errorf("端口参数位置不对: %q", got)
	}
}

// 含空格的密钥路径要加引号，而 ~ 不能被引号包住（否则 shell 不展开）
func TestToSSHCommandQuotesKeyPath(t *testing.T) {
	tun := Tunnel{
		User: "root", Host: "h", Port: 22, AuthType: AuthKey,
		KeyPath:  "/Users/me/My Keys/id_rsa",
		Forwards: []Forward{{Type: ForwardLocal, Listen: "8080", Target: "localhost:80"}},
	}
	if got := ToSSHCommand(tun); !strings.Contains(got, "-i '/Users/me/My Keys/id_rsa'") {
		t.Errorf("含空格路径未加引号: %q", got)
	}

	tun.KeyPath = "~/.ssh/id_rsa"
	if got := ToSSHCommand(tun); strings.Contains(got, "'") {
		t.Errorf("~ 路径不应被引号包住: %q", got)
	}
}

func TestToSSHCommandIPv6(t *testing.T) {
	tun := Tunnel{
		User: "root", Host: "h", Port: 22, AuthType: AuthKey, KeyPath: "/k",
		Forwards: []Forward{
			{Type: ForwardLocal, Listen: "[::1]:8080", Target: "[::1]:80"},
			{Type: ForwardRemote, Listen: "9090", Target: "localhost:90"},
			{Type: ForwardDynamic, Listen: "1080"},
		},
	}
	got := ToSSHCommand(tun)
	for _, want := range []string{"-L [::1]:8080:[::1]:80", "-R 9090:localhost:90", "-D 1080"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q: %q", want, got)
		}
	}
}

// 未加方括号的 IPv6 在回退路径里也要补回括号，否则导出命令无法解析
func TestToSSHCommandIPv6WithoutBrackets(t *testing.T) {
	tun := Tunnel{
		User: "root", Host: "h", Port: 22, AuthType: AuthKey, KeyPath: "/k",
		Forwards: []Forward{{Type: ForwardLocal, Listen: "8080", Target: "::1:80"}},
	}
	if got := ToSSHCommand(tun); !strings.Contains(got, "-L 8080:[::1]:80") {
		t.Errorf("裸 IPv6 目标未补括号: %q", got)
	}
}
