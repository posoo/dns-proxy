package proxy

import (
	"container/list"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/sen/dns-proxy/internal/config"
)

type responseCache struct {
	mu      sync.Mutex
	enabled bool
	max     int
	minTTL  time.Duration
	maxTTL  time.Duration
	entries map[string]*list.Element
	order   *list.List
}

type cacheEntry struct {
	key       string
	response  []byte
	expiresAt time.Time
	ttl       time.Duration
}

func newResponseCache(cfg config.CacheConfig) *responseCache {
	return &responseCache{
		enabled: cfg.Enabled,
		max:     cfg.MaxEntries,
		minTTL:  cfg.MinTTL,
		maxTTL:  cfg.MaxTTL,
		entries: map[string]*list.Element{},
		order:   list.New(),
	}
}

func (c *responseCache) Get(key string, id uint16) ([]byte, int, bool) {
	if !c.enabled {
		return nil, 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return nil, 0, false
	}
	entry := elem.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.remove(elem)
		return nil, 0, false
	}
	c.order.MoveToFront(elem)
	msg := new(dns.Msg)
	if err := msg.Unpack(entry.response); err == nil {
		msg.Id = id
		if packed, err := msg.Pack(); err == nil {
			ttl := int(time.Until(entry.expiresAt).Seconds())
			if ttl < 0 {
				ttl = 0
			}
			return packed, ttl, true
		}
	}
	cp := append([]byte(nil), entry.response...)
	ttl := int(time.Until(entry.expiresAt).Seconds())
	if ttl < 0 {
		ttl = 0
	}
	return cp, ttl, true
}

func (c *responseCache) Set(key string, msg *dns.Msg, packed []byte) int {
	if !c.enabled || msg.Rcode != dns.RcodeSuccess {
		return 0
	}
	ttl := ttlFromMessage(msg)
	if ttl <= 0 {
		return 0
	}
	if ttl < c.minTTL {
		ttl = c.minTTL
	}
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}
	entry := &cacheEntry{
		key:       key,
		response:  append([]byte(nil), packed...),
		expiresAt: time.Now().Add(ttl),
		ttl:       ttl,
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		elem.Value = entry
		c.order.MoveToFront(elem)
	} else {
		c.entries[key] = c.order.PushFront(entry)
	}
	for len(c.entries) > c.max {
		c.remove(c.order.Back())
	}
	return int(ttl.Seconds())
}

func (c *responseCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]*list.Element{}
	c.order.Init()
}

func (c *responseCache) Stats() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{
		"enabled":     c.enabled,
		"entries":     len(c.entries),
		"max_entries": c.max,
	}
}

func (c *responseCache) remove(elem *list.Element) {
	if elem == nil {
		return
	}
	entry := elem.Value.(*cacheEntry)
	delete(c.entries, entry.key)
	c.order.Remove(elem)
}

func ttlFromMessage(msg *dns.Msg) time.Duration {
	min := uint32(0)
	for _, rr := range msg.Answer {
		if min == 0 || rr.Header().Ttl < min {
			min = rr.Header().Ttl
		}
	}
	if min == 0 {
		for _, rr := range msg.Ns {
			if min == 0 || rr.Header().Ttl < min {
				min = rr.Header().Ttl
			}
		}
	}
	if min == 0 {
		return 0
	}
	return time.Duration(min) * time.Second
}
