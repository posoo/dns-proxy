package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(`
admin:
  enabled: true
  username: admin
  password: secret
resolvers:
  - name: udp1
    protocol: udp
    address: "127.0.0.1:53"
cache:
  enabled: true
logging:
  enabled: false
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTP.ListenAddr != ":8080" {
		t.Fatalf("default listen addr = %q", cfg.HTTP.ListenAddr)
	}
	if cfg.Resolvers[0].Timeout != 5*time.Second {
		t.Fatalf("resolver timeout = %v", cfg.Resolvers[0].Timeout)
	}
}

func TestValidateRejectsBadResolverShapes(t *testing.T) {
	tests := []ResolverConfig{
		{Name: "udp", Protocol: "udp", URL: "https://example.com/dns-query", Timeout: time.Second},
		{Name: "doh", Protocol: "doh", Address: "1.1.1.1:53", Timeout: time.Second},
		{Name: "bad", Protocol: "ftp", Address: "1.1.1.1:53", Timeout: time.Second},
	}
	for _, resolver := range tests {
		cfg := &Config{Resolvers: []ResolverConfig{resolver}, Cache: CacheConfig{MaxEntries: 1}, Logging: LoggingConfig{MaxRows: 1}}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() accepted %#v", resolver)
		}
	}
}
