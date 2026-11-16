package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds runtime configuration for the local-web-eyes MCP service.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Search   SearchConfig   `yaml:"search"`
	Fetch    FetchConfig    `yaml:"fetch"`
	Crawl    CrawlConfig    `yaml:"crawl"`
	Browser  BrowserConfig  `yaml:"browser"`
	Security SecurityConfig `yaml:"security"`
	Cache    CacheConfig    `yaml:"cache"`
}

type ServerConfig struct {
	Transport string `yaml:"transport"` // "stdio", "http", or "dual"
	HTTPAddr  string `yaml:"http_addr"` // e.g. "127.0.0.1:8090"
	HTTPPath  string `yaml:"http_path"` // e.g. "/mcp"
	LogLevel  string `yaml:"log_level"` // "debug", "info", "warn", "error"
}

type SearchConfig struct {
	Timeout               time.Duration `yaml:"timeout"`
	DefaultLimit          int           `yaml:"default_limit"`
	MaxLimit              int           `yaml:"max_limit"`
	MaxMultiQueries       int           `yaml:"max_multi_queries"`
	SearXNGURL            string        `yaml:"searxng_url"`
	EnableWikipedia       bool          `yaml:"enable_wikipedia"`
	EnableDuckDuckGo      bool          `yaml:"enable_duckduckgo"`
	EnableMojeek          bool          `yaml:"enable_mojeek"`
	EnableBrowserFallback bool          `yaml:"enable_browser_fallback"`
	WikipediaUserAgent    string        `yaml:"wikipedia_user_agent"`
	CacheTTL              time.Duration `yaml:"cache_ttl"`
}

type FetchConfig struct {
	Timeout                     time.Duration `yaml:"timeout"`
	MaxResponseBytes            int64         `yaml:"max_response_bytes"`
	MaxRedirects                int           `yaml:"max_redirects"`
	DefaultMaxChars             int           `yaml:"default_max_chars"`
	HardMaxChars                int           `yaml:"hard_max_chars"`
	MinExtractedCharsForBrowser int           `yaml:"min_extracted_chars_for_browser"`
	MaxConcurrentFetches        int           `yaml:"max_concurrent_fetches"`
	CacheTTL                    time.Duration `yaml:"cache_ttl"`
	UserAgent                   string        `yaml:"user_agent"`
}

type CrawlConfig struct {
	DefaultDepth    int `yaml:"default_depth"`
	HardMaxDepth    int `yaml:"hard_max_depth"`
	DefaultMaxPages int `yaml:"default_max_pages"`
	HardMaxPages    int `yaml:"hard_max_pages"`
	DefaultMaxChars int `yaml:"default_max_chars"`
	HardMaxChars    int `yaml:"hard_max_chars"`
}

type BrowserConfig struct {
	Enabled            bool          `yaml:"enabled"`
	BinaryPath         string        `yaml:"binary_path"`
	MaxConcurrentPages int           `yaml:"max_concurrent_pages"`
	PageTimeout        time.Duration `yaml:"page_timeout"`
	BlockMedia         bool          `yaml:"block_media"`
}

type SecurityConfig struct {
	BlocklistEnabled         bool          `yaml:"blocklist_enabled"`
	BlocklistRefreshInterval time.Duration `yaml:"blocklist_refresh_interval"`
	BlocklistFeeds           []string      `yaml:"blocklist_feeds"`
	CustomDenylist           []string      `yaml:"custom_denylist"`
	CustomAllowlist          []string      `yaml:"custom_allowlist"`
	AllowPrivateIPs          bool          `yaml:"allow_private_ips"`
}

type CacheConfig struct {
	DBPath string `yaml:"db_path"`
}

// Default returns opinionated defaults tuned for local LLMs such as Qwen 3.5 9B.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Transport: "stdio",
			HTTPAddr:  "127.0.0.1:8090",
			HTTPPath:  "/mcp",
			LogLevel:  "info",
		},
		Search: SearchConfig{
			Timeout:               8 * time.Second,
			DefaultLimit:          5,
			MaxLimit:              10,
			MaxMultiQueries:       5,
			SearXNGURL:            "",
			EnableWikipedia:       true,
			EnableDuckDuckGo:      true,
			EnableMojeek:          true,
			EnableBrowserFallback: true,
			WikipediaUserAgent:    "local-web-eyes/1.0 (local-llm-research-agent)",
			CacheTTL:              20 * time.Minute,
		},
		Fetch: FetchConfig{
			Timeout:                     15 * time.Second,
			MaxResponseBytes:            5 * 1024 * 1024,
			MaxRedirects:                5,
			DefaultMaxChars:             6000,
			HardMaxChars:                24000,
			MinExtractedCharsForBrowser: 200,
			MaxConcurrentFetches:        4,
			CacheTTL:                    6 * time.Hour,
			UserAgent:                   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		},
		Crawl: CrawlConfig{
			DefaultDepth:    1,
			HardMaxDepth:    2,
			DefaultMaxPages: 5,
			HardMaxPages:    10,
			DefaultMaxChars: 12000,
			HardMaxChars:    32000,
		},
		Browser: BrowserConfig{
			Enabled:            true,
			BinaryPath:         "",
			MaxConcurrentPages: 2,
			PageTimeout:        20 * time.Second,
			BlockMedia:         true,
		},
		Security: SecurityConfig{
			BlocklistEnabled:         true,
			BlocklistRefreshInterval: 12 * time.Hour,
			BlocklistFeeds: []string{
				"https://raw.githubusercontent.com/hagezi/dns-blocklists/main/adblock/tif.txt",
				"https://raw.githubusercontent.com/openphish/public_feed/refs/heads/main/feed.txt",
				"https://raw.githubusercontent.com/Phishing-Database/Phishing.Database/master/phishing-links-ACTIVE.txt",
			},
			AllowPrivateIPs: false,
		},
		Cache: CacheConfig{
			DBPath: "data/web_eyes.db",
		},
	}
}

// Load reads an optional YAML file and overlays environment variables.
func Load(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, err
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return cfg, err
		}
	}
	applyEnv(&cfg)
	normalize(&cfg)
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("LWE_TRANSPORT")); v != "" {
		cfg.Server.Transport = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("LWE_HTTP_ADDR")); v != "" {
		cfg.Server.HTTPAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("LWE_HTTP_PATH")); v != "" {
		cfg.Server.HTTPPath = v
	}
	if v := strings.TrimSpace(os.Getenv("SEARXNG_URL")); v != "" {
		cfg.Search.SearXNGURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("LWE_DB_PATH")); v != "" {
		cfg.Cache.DBPath = v
	}
	if v := strings.TrimSpace(os.Getenv("LWE_BROWSER_BIN")); v != "" {
		cfg.Browser.BinaryPath = v
	}
	if v := strings.TrimSpace(os.Getenv("LWE_BROWSER_ENABLED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Browser.Enabled = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("LWE_BLOCKLIST_ENABLED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Security.BlocklistEnabled = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("LWE_DEFAULT_MAX_CHARS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Fetch.DefaultMaxChars = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("URLHAUS_AUTH_KEY")); v != "" {
		cfg.Security.BlocklistFeeds = append(cfg.Security.BlocklistFeeds,
			"https://urlhaus-api.abuse.ch/v2/files/exports/"+v+"/hostfile.txt",
		)
	}
}

func normalize(cfg *Config) {
	if cfg.Server.HTTPPath == "" || !strings.HasPrefix(cfg.Server.HTTPPath, "/") {
		cfg.Server.HTTPPath = "/mcp"
	}
	if cfg.Fetch.DefaultMaxChars <= 0 {
		cfg.Fetch.DefaultMaxChars = 6000
	}
	if cfg.Fetch.HardMaxChars < cfg.Fetch.DefaultMaxChars {
		cfg.Fetch.HardMaxChars = cfg.Fetch.DefaultMaxChars
	}
	if cfg.Crawl.HardMaxDepth <= 0 {
		cfg.Crawl.HardMaxDepth = 2
	}
	if cfg.Crawl.HardMaxPages <= 0 {
		cfg.Crawl.HardMaxPages = 10
	}
	if cfg.Browser.MaxConcurrentPages <= 0 {
		cfg.Browser.MaxConcurrentPages = 2
	}
	if cfg.Fetch.MaxConcurrentFetches <= 0 {
		cfg.Fetch.MaxConcurrentFetches = 4
	}
}
