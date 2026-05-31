package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sen/dns-proxy/internal/config"
)

type stubResolver struct {
	cfg   config.ResolverConfig
	calls int
}

func (s *stubResolver) Config() config.ResolverConfig { return s.cfg }

func (s *stubResolver) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, string, error) {
	s.calls++
	resp := new(dns.Msg)
	resp.SetReply(msg)
	resp.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: msg.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
		A:   []byte{192, 0, 2, 10},
	}}
	return resp, time.Millisecond, s.cfg.Address, nil
}

func TestRawQueryAndCache(t *testing.T) {
	app := testApp(t)
	stub := &stubResolver{cfg: config.ResolverConfig{Name: "test", Protocol: "udp", Address: "127.0.0.1:53"}}
	app.resolvers["test"] = stub

	reqMsg := new(dns.Msg)
	reqMsg.SetQuestion("example.com.", dns.TypeA)
	wire, _ := reqMsg.Pack()
	body := rawQueryRequest{ResolverName: "test", DNSMessage: base64.StdEncoding.EncodeToString(wire)}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		requestJSON(t, app.Routes(), http.MethodPost, "/api/v1/query/raw", body, rec)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var resp rawQueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if i == 1 && !resp.Cache.Hit {
			t.Fatal("second response should be cache hit")
		}
	}
	if stub.calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", stub.calls)
	}
}

func TestEmptyResolverUsesSystemDefault(t *testing.T) {
	app := testApp(t)
	system := &stubResolver{cfg: config.ResolverConfig{Name: systemResolverName, Protocol: "system", Address: "10.0.0.2:53"}}
	app.system = system

	rec := httptest.NewRecorder()
	requestJSON(t, app.Routes(), http.MethodPost, "/api/v1/query/lookup", lookupRequest{Hostname: "example.com", Type: "A"}, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp lookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ResolverName != systemResolverName {
		t.Fatalf("resolver_name = %q, want %q", resp.ResolverName, systemResolverName)
	}
	if resp.ResolverAddress != "10.0.0.2:53" {
		t.Fatalf("resolver_address = %q", resp.ResolverAddress)
	}
	if system.calls != 1 {
		t.Fatalf("system resolver calls = %d, want 1", system.calls)
	}
}

func TestLookupRejectsUnknownResolver(t *testing.T) {
	app := testApp(t)
	rec := httptest.NewRecorder()
	requestJSON(t, app.Routes(), http.MethodPost, "/api/v1/query/lookup", lookupRequest{ResolverName: "missing", Hostname: "example.com", Type: "A"}, rec)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminBasicAuth(t *testing.T) {
	app := testApp(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	req.SetBasicAuth("admin", "secret")
	rec = httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with auth = %d body=%s", rec.Code, rec.Body.String())
	}
}

func testApp(t *testing.T) *App {
	t.Helper()
	cfg := &config.Config{
		HTTP:      config.HTTPConfig{ListenAddr: ":0"},
		Admin:     config.AdminConfig{Enabled: true, Username: "admin", Password: "secret"},
		Resolvers: []config.ResolverConfig{{Name: "test", Protocol: "udp", Address: "127.0.0.1:53", Timeout: time.Second}},
		Cache:     config.CacheConfig{Enabled: true, MaxEntries: 10, MinTTL: time.Second, MaxTTL: time.Minute},
		Logging:   config.LoggingConfig{Enabled: false, MaxRows: 10},
	}
	app, err := NewApp(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, rec *httptest.ResponseRecorder) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
}
