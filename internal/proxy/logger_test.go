package proxy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sen/dns-proxy/internal/config"
)

func TestSQLiteQueryLoggerRetention(t *testing.T) {
	logger, err := newQueryLogger(config.LoggingConfig{Enabled: true, Path: filepath.Join(t.TempDir(), "logs.sqlite3"), MaxRows: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	for i := 0; i < 3; i++ {
		logger.Log(context.Background(), queryLogEntry{
			Timestamp:    time.Now().Add(time.Duration(i) * time.Second),
			ClientAddr:   "127.0.0.1",
			ResolverName: "test",
			Protocol:     "udp",
			Question:     "example.com. IN A",
			QueryType:    "A",
			RCode:        "NOERROR",
			Answers:      []string{"example.com. 30 IN A 192.0.2.1"},
			DurationMS:   1,
		})
	}
	logs, err := logger.List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("len(logs) = %d, want 2", len(logs))
	}
}
