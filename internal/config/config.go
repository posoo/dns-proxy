package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP      HTTPConfig       `yaml:"http" json:"http"`
	Admin     AdminConfig      `yaml:"admin" json:"admin"`
	Resolvers []ResolverConfig `yaml:"resolvers" json:"resolvers"`
	Cache     CacheConfig      `yaml:"cache" json:"cache"`
	Logging   LoggingConfig    `yaml:"logging" json:"logging"`
}

type HTTPConfig struct {
	ListenAddr        string        `yaml:"listen_addr" json:"listen_addr"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout" json:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
}

type AdminConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"-"`
}

type ResolverConfig struct {
	Name       string        `yaml:"name" json:"name"`
	Protocol   string        `yaml:"protocol" json:"protocol"`
	Address    string        `yaml:"address" json:"address"`
	URL        string        `yaml:"url" json:"url"`
	Timeout    time.Duration `yaml:"timeout" json:"timeout"`
	ServerName string        `yaml:"server_name" json:"server_name"`
}

type CacheConfig struct {
	Enabled    bool          `yaml:"enabled" json:"enabled"`
	MaxEntries int           `yaml:"max_entries" json:"max_entries"`
	MinTTL     time.Duration `yaml:"min_ttl" json:"min_ttl"`
	MaxTTL     time.Duration `yaml:"max_ttl" json:"max_ttl"`
}

type LoggingConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Path    string `yaml:"path" json:"path"`
	MaxRows int    `yaml:"max_rows" json:"max_rows"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.HTTP.ListenAddr == "" {
		c.HTTP.ListenAddr = ":8080"
	}
	if c.HTTP.ReadHeaderTimeout == 0 {
		c.HTTP.ReadHeaderTimeout = 5 * time.Second
	}
	if c.HTTP.ReadTimeout == 0 {
		c.HTTP.ReadTimeout = 15 * time.Second
	}
	if c.HTTP.WriteTimeout == 0 {
		c.HTTP.WriteTimeout = 15 * time.Second
	}
	if c.HTTP.IdleTimeout == 0 {
		c.HTTP.IdleTimeout = 60 * time.Second
	}
	if c.Admin.Username == "" {
		c.Admin.Username = "admin"
	}
	if c.Cache.MaxEntries == 0 {
		c.Cache.MaxEntries = 4096
	}
	if c.Cache.MinTTL == 0 {
		c.Cache.MinTTL = time.Second
	}
	if c.Cache.MaxTTL == 0 {
		c.Cache.MaxTTL = 5 * time.Minute
	}
	if c.Logging.Path == "" {
		c.Logging.Path = "dns-proxy.sqlite3"
	}
	if c.Logging.MaxRows == 0 {
		c.Logging.MaxRows = 10000
	}
	for i := range c.Resolvers {
		c.Resolvers[i].Protocol = strings.ToLower(strings.TrimSpace(c.Resolvers[i].Protocol))
		if c.Resolvers[i].Timeout == 0 {
			c.Resolvers[i].Timeout = 5 * time.Second
		}
	}
}

func (c *Config) Validate() error {
	if c.Admin.Enabled && c.Admin.Password == "" {
		return errors.New("admin.password is required when admin is enabled")
	}
	if len(c.Resolvers) == 0 {
		return errors.New("at least one resolver is required")
	}
	seen := map[string]struct{}{}
	for _, resolver := range c.Resolvers {
		if resolver.Name == "" {
			return errors.New("resolver name is required")
		}
		if _, ok := seen[resolver.Name]; ok {
			return fmt.Errorf("duplicate resolver name %q", resolver.Name)
		}
		seen[resolver.Name] = struct{}{}
		if err := validateResolver(resolver); err != nil {
			return fmt.Errorf("resolver %q: %w", resolver.Name, err)
		}
	}
	if c.Cache.MaxEntries < 1 {
		return errors.New("cache.max_entries must be positive")
	}
	if c.Cache.MinTTL < 0 || c.Cache.MaxTTL < 0 || c.Cache.MinTTL > c.Cache.MaxTTL {
		return errors.New("cache TTL bounds are invalid")
	}
	if c.Logging.MaxRows < 1 {
		return errors.New("logging.max_rows must be positive")
	}
	return nil
}

func validateResolver(r ResolverConfig) error {
	switch r.Protocol {
	case "udp", "tcp", "dot":
		if r.Address == "" {
			return fmt.Errorf("%s resolver requires address", r.Protocol)
		}
		host, port, err := net.SplitHostPort(r.Address)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("address must be host:port")
		}
		if r.URL != "" {
			return fmt.Errorf("%s resolver must not set url", r.Protocol)
		}
	case "doh":
		if r.URL == "" {
			return errors.New("doh resolver requires url")
		}
		u, err := url.Parse(r.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("doh url must be an https URL")
		}
		if r.Address != "" {
			return errors.New("doh resolver must not set address")
		}
	default:
		return fmt.Errorf("unsupported protocol %q", r.Protocol)
	}
	if r.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	return nil
}

func (c *Config) ResolverByName(name string) (ResolverConfig, bool) {
	for _, r := range c.Resolvers {
		if r.Name == name {
			return r, true
		}
	}
	return ResolverConfig{}, false
}
