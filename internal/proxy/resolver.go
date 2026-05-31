package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/sen/dns-proxy/internal/config"
)

type resolverClient interface {
	Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, string, error)
	Config() config.ResolverConfig
}

const systemResolverName = "system-default"

func newResolverClient(cfg config.ResolverConfig) resolverClient {
	if cfg.Protocol == "doh" {
		return &dohResolver{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
	}
	return &dnsResolver{cfg: cfg}
}

func newSystemResolverClient(timeout time.Duration) resolverClient {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &systemResolver{
		timeout:        timeout,
		resolvConfPath: "/etc/resolv.conf",
		cfg: config.ResolverConfig{
			Name:     systemResolverName,
			Protocol: "system",
			Timeout:  timeout,
		},
	}
}

type dnsResolver struct {
	cfg config.ResolverConfig
}

func (r *dnsResolver) Config() config.ResolverConfig {
	return r.cfg
}

func (r *dnsResolver) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, string, error) {
	network := r.cfg.Protocol
	client := &dns.Client{Net: network, Timeout: r.cfg.Timeout}
	if r.cfg.Protocol == "dot" {
		network = "tcp-tls"
		client.Net = network
		client.TLSConfig = &tls.Config{ServerName: r.cfg.ServerName, MinVersion: tls.VersionTLS12}
	}
	resp, rtt, err := exchangeDNS(ctx, client, msg, r.cfg.Address)
	return resp, rtt, r.cfg.Address, err
}

type dohResolver struct {
	cfg    config.ResolverConfig
	client *http.Client
}

func (r *dohResolver) Config() config.ResolverConfig {
	return r.cfg
}

func (r *dohResolver) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, string, error) {
	wire, err := msg.Pack()
	if err != nil {
		return nil, 0, r.cfg.URL, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.URL, bytes.NewReader(wire))
	if err != nil {
		return nil, 0, r.cfg.URL, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	start := time.Now()
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, 0, r.cfg.URL, err
	}
	defer resp.Body.Close()
	rtt := time.Since(start)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, rtt, r.cfg.URL, fmt.Errorf("doh status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	if err != nil {
		return nil, rtt, r.cfg.URL, err
	}
	out := new(dns.Msg)
	if err := out.Unpack(body); err != nil {
		return nil, rtt, r.cfg.URL, err
	}
	return out, rtt, r.cfg.URL, nil
}

type systemResolver struct {
	timeout        time.Duration
	resolvConfPath string
	mu             sync.RWMutex
	cfg            config.ResolverConfig
}

func (r *systemResolver) Config() config.ResolverConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

func (r *systemResolver) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, time.Duration, string, error) {
	clientConfig, err := dns.ClientConfigFromFile(r.resolvConfPath)
	if err != nil {
		return nil, 0, "", err
	}
	if len(clientConfig.Servers) == 0 {
		return nil, 0, "", fmt.Errorf("%s has no nameservers", r.resolvConfPath)
	}
	port := clientConfig.Port
	if port == "" {
		port = "53"
	}
	client := &dns.Client{Net: "udp", Timeout: r.timeout}
	var lastErr error
	for _, server := range clientConfig.Servers {
		address := net.JoinHostPort(server, port)
		resp, rtt, err := exchangeDNS(ctx, client, msg, address)
		r.rememberAddress(address)
		if err == nil {
			return resp, rtt, address, nil
		}
		lastErr = err
	}
	return nil, 0, r.Config().Address, lastErr
}

func (r *systemResolver) rememberAddress(address string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.Address = address
}

func exchangeDNS(ctx context.Context, client *dns.Client, msg *dns.Msg, address string) (*dns.Msg, time.Duration, error) {
	type result struct {
		resp *dns.Msg
		rtt  time.Duration
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, rtt, err := client.Exchange(msg, address)
		ch <- result{resp: resp, rtt: rtt, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case res := <-ch:
		return res.resp, res.rtt, res.err
	}
}
