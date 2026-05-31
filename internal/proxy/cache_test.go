package proxy

import (
	"fmt"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sen/dns-proxy/internal/config"
)

func TestResponseCachePreservesRequestedID(t *testing.T) {
	cache := newResponseCache(config.CacheConfig{Enabled: true, MaxEntries: 10, MinTTL: time.Second, MaxTTL: time.Minute})
	msg := new(dns.Msg)
	msg.SetReply((&dns.Msg{}).SetQuestion("example.com.", dns.TypeA))
	msg.Id = 10
	msg.Answer = []dns.RR{testA("example.com.", "192.0.2.1", 30)}
	packed, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if ttl := cache.Set("key", msg, packed); ttl == 0 {
		t.Fatal("expected cache set")
	}
	out, _, ok := cache.Get("key", 99)
	if !ok {
		t.Fatal("expected cache hit")
	}
	got := new(dns.Msg)
	if err := got.Unpack(out); err != nil {
		t.Fatal(err)
	}
	if got.Id != 99 {
		t.Fatalf("cached response id = %d, want 99", got.Id)
	}
}

func testA(name, ip string, ttl uint32) dns.RR {
	rr, err := dns.NewRR(fmt.Sprintf("%s %d IN A %s", name, ttl, ip))
	if err != nil {
		panic(err)
	}
	return rr
}
