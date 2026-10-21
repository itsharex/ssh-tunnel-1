package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// 配置文件损坏时必须能启动：备份坏文件 + 以默认配置继续，而不是直接报错退出。
func TestStoreRecoversFromCorruptConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"tunnels": [ 坏掉了`), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &Store{path: path, cfg: &Config{Version: CurrentVersion}}
	if err := s.load(); err != nil {
		t.Fatalf("load 应回退到默认配置，实际返回错误: %v", err)
	}

	backups, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("期望 1 个备份文件，实际 %d 个", len(backups))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("新配置未写入: %v", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("重写后的配置仍不是合法 JSON: %v", err)
	}
	if c.Version != CurrentVersion {
		t.Errorf("version = %q, want %q", c.Version, CurrentVersion)
	}
}

// 并发保存不应写坏配置文件（persist 用临时文件 + rename）。
func TestStoreConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s := &Store{path: path, cfg: &Config{Version: CurrentVersion, Settings: Settings{MinimizeToTray: true}}}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.SaveTunnel(Tunnel{ID: "", Name: "t", Host: "h", Port: 22}); err != nil {
				t.Errorf("save: %v", err)
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("并发写入后配置损坏: %v", err)
	}
	if len(c.Tunnels) != 20 {
		t.Errorf("tunnels = %d, want 20", len(c.Tunnels))
	}
}
