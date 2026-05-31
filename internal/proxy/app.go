package proxy

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/sen/dns-proxy/internal/config"
)

type App struct {
	mu        sync.RWMutex
	cfg       *config.Config
	logger    *slog.Logger
	resolvers map[string]resolverClient
	system    resolverClient
	cache     *responseCache
	qlog      queryLogger
	startedAt time.Time
}

func NewApp(cfg *config.Config, logger *slog.Logger) (*App, error) {
	resolvers := map[string]resolverClient{}
	for _, r := range cfg.Resolvers {
		resolvers[r.Name] = newResolverClient(r)
	}
	qlog, err := newQueryLogger(cfg.Logging)
	if err != nil {
		return nil, err
	}
	return &App{
		cfg:       cfg,
		logger:    logger,
		resolvers: resolvers,
		system:    newSystemResolverClient(5 * time.Second),
		cache:     newResponseCache(cfg.Cache),
		qlog:      qlog,
		startedAt: time.Now().UTC(),
	}, nil
}

func (a *App) Close() {
	if a.qlog != nil {
		_ = a.qlog.Close()
	}
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /api/v1/query/raw", a.handleRawQuery)
	mux.HandleFunc("POST /api/v1/query/lookup", a.handleLookup)
	mux.Handle("GET /admin", a.adminOnly(http.HandlerFunc(a.adminHome)))
	mux.Handle("GET /admin/logs", a.adminOnly(http.HandlerFunc(a.adminLogsPage)))
	mux.Handle("GET /admin/api/status", a.adminOnly(http.HandlerFunc(a.adminStatus)))
	mux.Handle("GET /admin/api/logs", a.adminOnly(http.HandlerFunc(a.adminLogs)))
	mux.Handle("GET /admin/api/config", a.adminOnly(http.HandlerFunc(a.adminConfig)))
	mux.Handle("GET /admin/api/cache", a.adminOnly(http.HandlerFunc(a.adminCache)))
	mux.Handle("POST /admin/api/cache/clear", a.adminOnly(http.HandlerFunc(a.adminCacheClear)))
	mux.Handle("PUT /admin/api/config/runtime", a.adminOnly(http.HandlerFunc(a.adminRuntimeConfig)))
	return requestID(mux)
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleRawQuery(w http.ResponseWriter, r *http.Request) {
	var req rawQueryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	wire, err := base64.StdEncoding.DecodeString(req.DNSMessage)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_dns_message", "dns_message must be base64 encoded DNS wire bytes")
		return
	}
	msg := new(dns.Msg)
	if err := msg.Unpack(wire); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_dns_message", "dns_message is not a valid DNS message")
		return
	}
	resp, entry, status, apiErr := a.resolve(r, req.ResolverName, msg)
	a.qlog.Log(r.Context(), entry)
	if apiErr != nil {
		writeAPIError(w, status, apiErr.Code, apiErr.Message)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *App) handleLookup(w http.ResponseWriter, r *http.Request) {
	var req lookupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := dns.Fqdn(strings.TrimSpace(req.Hostname))
	if name == "." {
		writeAPIError(w, http.StatusBadRequest, "invalid_hostname", "hostname is required")
		return
	}
	qtype := dns.StringToType[strings.ToUpper(defaultString(req.Type, "A"))]
	if qtype == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_type", "unsupported DNS record type")
		return
	}
	qclass := dns.StringToClass[strings.ToUpper(defaultString(req.Class, "IN"))]
	if qclass == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_class", "unsupported DNS class")
		return
	}
	msg := new(dns.Msg)
	msg.SetQuestion(name, qtype)
	msg.Question[0].Qclass = qclass

	rawResp, entry, status, apiErr := a.resolve(r, req.ResolverName, msg)
	a.qlog.Log(r.Context(), entry)
	if apiErr != nil {
		writeAPIError(w, status, apiErr.Code, apiErr.Message)
		return
	}
	wire, _ := base64.StdEncoding.DecodeString(rawResp.DNSMessage)
	dnsResp := new(dns.Msg)
	_ = dnsResp.Unpack(wire)
	writeJSON(w, http.StatusOK, lookupResponse{
		rawQueryResponse: rawResp,
		Question:         questionString(msg),
		Answers:          answersFromMsg(dnsResp),
		RCode:            dns.RcodeToString[dnsResp.Rcode],
	})
}

func (a *App) resolve(r *http.Request, resolverName string, msg *dns.Msg) (rawQueryResponse, queryLogEntry, int, *apiError) {
	start := time.Now()
	entry := queryLogEntry{
		Timestamp:    start.UTC(),
		ClientAddr:   clientAddr(r),
		Question:     questionString(msg),
		QueryType:    queryType(msg),
		RCode:        "",
		Answers:      nil,
		CacheHit:     false,
		ResolverName: resolverName,
	}
	resolver, ok := a.getResolver(resolverName)
	if !ok {
		entry.Error = "unknown resolver"
		entry.DurationMS = time.Since(start).Milliseconds()
		return rawQueryResponse{}, entry, http.StatusBadRequest, &apiError{Code: "unknown_resolver", Message: "resolver_name is not configured"}
	}
	cfg := resolver.Config()
	entry.ResolverName = cfg.Name
	entry.Protocol = cfg.Protocol
	if len(msg.Question) == 0 {
		entry.Error = "missing question"
		entry.DurationMS = time.Since(start).Milliseconds()
		return rawQueryResponse{}, entry, http.StatusBadRequest, &apiError{Code: "invalid_dns_message", Message: "DNS message must contain at least one question"}
	}

	cacheKey := cacheKey(cfg.Name, cfg.Protocol, msg)
	if cached, ttl, ok := a.cache.Get(cacheKey, msg.Id); ok {
		entry.CacheHit = true
		entry.DurationMS = time.Since(start).Milliseconds()
		entry.ResolverAddr = cfg.Address
		respMsg := new(dns.Msg)
		_ = respMsg.Unpack(cached)
		entry.RCode = dns.RcodeToString[respMsg.Rcode]
		entry.Answers = answerStrings(respMsg)
		return rawQueryResponse{
			DNSMessage:      base64.StdEncoding.EncodeToString(cached),
			ResolverName:    cfg.Name,
			Protocol:        cfg.Protocol,
			ResolverAddress: cfg.Address,
			Cache:           cacheDetails{Hit: true, TTL: ttl},
			DurationMS:      entry.DurationMS,
		}, entry, http.StatusOK, nil
	}

	ctx := r.Context()
	respMsg, _, resolverAddress, err := resolver.Exchange(ctx, msg.Copy())
	entry.DurationMS = time.Since(start).Milliseconds()
	entry.ResolverAddr = resolverAddress
	if err != nil {
		entry.Error = err.Error()
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusGatewayTimeout
		}
		return rawQueryResponse{}, entry, status, &apiError{Code: "upstream_error", Message: err.Error()}
	}
	if respMsg == nil {
		entry.Error = "empty upstream response"
		return rawQueryResponse{}, entry, http.StatusBadGateway, &apiError{Code: "upstream_error", Message: "empty upstream response"}
	}
	respMsg.Id = msg.Id
	packed, err := respMsg.Pack()
	if err != nil {
		entry.Error = err.Error()
		return rawQueryResponse{}, entry, http.StatusBadGateway, &apiError{Code: "invalid_upstream_response", Message: "upstream returned an invalid DNS response"}
	}
	ttl := a.cache.Set(cacheKey, respMsg, packed)
	entry.RCode = dns.RcodeToString[respMsg.Rcode]
	entry.Answers = answerStrings(respMsg)
	return rawQueryResponse{
		DNSMessage:      base64.StdEncoding.EncodeToString(packed),
		ResolverName:    cfg.Name,
		Protocol:        cfg.Protocol,
		ResolverAddress: resolverAddress,
		Cache:           cacheDetails{Hit: false, TTL: ttl},
		DurationMS:      entry.DurationMS,
	}, entry, http.StatusOK, nil
}

func (a *App) getResolver(name string) (resolverClient, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if name == "" {
		return a.system, true
	}
	resolver, ok := a.resolvers[name]
	return resolver, ok
}

func (a *App) adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		admin := a.cfg.Admin
		a.mu.RUnlock()
		if !admin.Enabled {
			http.NotFound(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(admin.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(admin.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="dns-proxy-admin"`)
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "admin credentials are required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) adminHome(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	renderAdmin(w, "DNS Proxy Admin", map[string]any{
		"StartedAt": a.startedAt,
		"Resolvers": cfg.Resolvers,
		"Cache":     a.cache.Stats(),
	})
}

func (a *App) adminLogsPage(w http.ResponseWriter, r *http.Request) {
	logs, _ := a.qlog.List(r.Context(), 100)
	renderAdmin(w, "DNS Proxy Logs", map[string]any{"Logs": logs})
}

func (a *App) adminStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	cfg := a.cfg
	resolvers := cfg.Resolvers
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"started_at":     a.startedAt,
		"uptime_seconds": int(time.Since(a.startedAt).Seconds()),
		"system_resolver": map[string]string{
			"name":     systemResolverName,
			"protocol": "system",
		},
		"resolvers": resolvers,
		"cache":     a.cache.Stats(),
	})
}

func (a *App) adminLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	logs, err := a.qlog.List(r.Context(), limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "log_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

func (a *App) adminConfig(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	writeJSON(w, http.StatusOK, a.cfg)
}

func (a *App) adminCache(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.cache.Stats())
}

func (a *App) adminCacheClear(w http.ResponseWriter, r *http.Request) {
	a.cache.Clear()
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}

func (a *App) adminRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CacheEnabled *bool `json:"cache_enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.CacheEnabled != nil {
		a.cfg.Cache.Enabled = *req.CacheEnabled
		a.cache.enabled = *req.CacheEnabled
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "config": a.cfg})
}

func cacheKey(resolverName, protocol string, msg *dns.Msg) string {
	flags := fmt.Sprintf("rd=%t;cd=%t;ad=%t;do=%t", msg.RecursionDesired, msg.CheckingDisabled, msg.AuthenticatedData, hasDO(msg))
	qs := make([]string, 0, len(msg.Question))
	for _, q := range msg.Question {
		qs = append(qs, fmt.Sprintf("%s/%d/%d", strings.ToLower(q.Name), q.Qtype, q.Qclass))
	}
	sort.Strings(qs)
	return resolverName + "|" + protocol + "|" + flags + "|" + strings.Join(qs, ",")
}

func hasDO(msg *dns.Msg) bool {
	if opt := msg.IsEdns0(); opt != nil {
		return opt.Do()
	}
	return false
}

func questionString(msg *dns.Msg) string {
	if len(msg.Question) == 0 {
		return ""
	}
	parts := make([]string, 0, len(msg.Question))
	for _, q := range msg.Question {
		parts = append(parts, fmt.Sprintf("%s %s %s", q.Name, dns.ClassToString[q.Qclass], dns.TypeToString[q.Qtype]))
	}
	return strings.Join(parts, "; ")
}

func queryType(msg *dns.Msg) string {
	if len(msg.Question) == 0 {
		return ""
	}
	return dns.TypeToString[msg.Question[0].Qtype]
}

func answersFromMsg(msg *dns.Msg) []dnsAnswer {
	out := make([]dnsAnswer, 0, len(msg.Answer))
	for _, rr := range msg.Answer {
		h := rr.Header()
		out = append(out, dnsAnswer{Name: h.Name, Type: dns.TypeToString[h.Rrtype], TTL: h.Ttl, Data: rr.String()})
	}
	return out
}

func answerStrings(msg *dns.Msg) []string {
	out := make([]string, 0, len(msg.Answer))
	for _, rr := range msg.Answer {
		out = append(out, rr.String())
	}
	return out
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

var adminTemplate = template.Must(template.New("admin").Parse(`<!doctype html>
<html>
<head><title>{{.Title}}</title><style>
body{font-family:system-ui,-apple-system,Segoe UI,sans-serif;margin:2rem;line-height:1.4;color:#202124}
nav a{margin-right:1rem} table{border-collapse:collapse;width:100%;margin-top:1rem}
td,th{border:1px solid #ddd;padding:.45rem;text-align:left;font-size:.9rem} code,pre{background:#f6f8fa;padding:.2rem .35rem;border-radius:4px}
</style></head>
<body><nav><a href="/admin">Status</a><a href="/admin/logs">Logs</a></nav><h1>{{.Title}}</h1>{{.Body}}</body></html>`))

func renderAdmin(w http.ResponseWriter, title string, data map[string]any) {
	var body strings.Builder
	body.WriteString("<pre>")
	enc, _ := json.MarshalIndent(data, "", "  ")
	body.WriteString(template.HTMLEscapeString(string(enc)))
	body.WriteString("</pre>")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminTemplate.Execute(w, map[string]any{"Title": title, "Body": template.HTML(body.String())})
}
