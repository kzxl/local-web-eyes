package security

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const maxFeedDownloadBytes = 25 * 1024 * 1024 // 25 MB cap per threat feed

// Blocklist matches known phishing, malware, and C2 domains/URLs in O(1)/O(labels).
type Blocklist struct {
	mu           sync.RWMutex
	allowDomains map[string]struct{}
	denyDomains  map[string]struct{}
	denyURLs     map[string]struct{}
	feedDomains  map[string]struct{}
	feedURLs     map[string]struct{}
}

// NewBlocklist initializes a blocklist with optional custom allowlist and denylist domains.
func NewBlocklist(allowlist, denylist []string) *Blocklist {
	bl := &Blocklist{
		allowDomains: make(map[string]struct{}, len(allowlist)),
		denyDomains:  make(map[string]struct{}, len(denylist)),
		denyURLs:     make(map[string]struct{}),
		feedDomains:  make(map[string]struct{}),
		feedURLs:     make(map[string]struct{}),
	}
	for _, d := range allowlist {
		if norm := normalizeHost(d); norm != "" {
			bl.allowDomains[norm] = struct{}{}
		}
	}
	for _, d := range denylist {
		if norm := normalizeHost(d); norm != "" {
			bl.denyDomains[norm] = struct{}{}
		}
	}
	return bl
}

// Stats returns counts of custom deny rules and synced threat-intel entries.
func (b *Blocklist) Stats() (domains int, urls int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.denyDomains) + len(b.feedDomains), len(b.denyURLs) + len(b.feedURLs)
}

// LoadLines parses AdBlock (||domain^), hosts (0.0.0.0 domain), URL, or bare domain lines
// and replaces the active feed snapshot atomically.
func (b *Blocklist) LoadLines(lines []string) (int, int) {
	nextDomains := make(map[string]struct{}, len(lines))
	nextURLs := make(map[string]struct{})
	for _, line := range lines {
		parseBlocklistLine(line, nextDomains, nextURLs)
	}

	b.mu.Lock()
	b.feedDomains = nextDomains
	b.feedURLs = nextURLs
	b.mu.Unlock()
	return len(nextDomains), len(nextURLs)
}

// SyncFromFeeds fetches multiple remote threat-intel feeds and updates the snapshot.
func (b *Blocklist) SyncFromFeeds(ctx context.Context, client *http.Client, feeds []string) (int, int, error) {
	if client == nil {
		return 0, 0, fmt.Errorf("http client is required")
	}
	nextDomains := make(map[string]struct{})
	nextURLs := make(map[string]struct{})

	var errs []string
	for _, feedURL := range feeds {
		if strings.TrimSpace(feedURL) == "" {
			continue
		}
		if err := fetchAndParseFeed(ctx, client, feedURL, nextDomains, nextURLs); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", feedURL, err))
		}
	}

	if len(nextDomains) > 0 || len(nextURLs) > 0 {
		b.mu.Lock()
		b.feedDomains = nextDomains
		b.feedURLs = nextURLs
		b.mu.Unlock()
	}
	if len(errs) > 0 {
		return len(nextDomains), len(nextURLs), fmt.Errorf("partial feed sync errors: %s", strings.Join(errs, "; "))
	}
	return len(nextDomains), len(nextURLs), nil
}

// MatchURL returns (reason, true) if rawURL matches custom denylist or threat-intel feeds,
// unless overridden by the custom allowlist.
func (b *Blocklist) MatchURL(rawURL string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", false
	}
	host := normalizeHost(u.Hostname())
	if host == "" {
		return "", false
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if matchDomainSet(host, b.allowDomains) {
		return "", false
	}
	if matchDomainSet(host, b.denyDomains) {
		return "custom denylist domain: " + host, true
	}
	if matchDomainSet(host, b.feedDomains) {
		return "threat-intel domain: " + host, true
	}

	canon := canonicalURLKey(u)
	if _, ok := b.denyURLs[canon]; ok {
		return "custom denylist URL", true
	}
	if _, ok := b.feedURLs[canon]; ok {
		return "threat-intel malicious URL", true
	}
	return "", false
}

func fetchAndParseFeed(ctx context.Context, client *http.Client, feedURL string, domains, urls map[string]struct{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxFeedDownloadBytes))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		parseBlocklistLine(scanner.Text(), domains, urls)
	}
	return scanner.Err()
}

func parseBlocklistLine(raw string, domains, urls map[string]struct{}) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") {
		return
	}

	// AdBlock syntax: ||evil.example^ or ||evil.example^$important
	if strings.HasPrefix(line, "||") {
		entry := strings.TrimPrefix(line, "||")
		if idx := strings.IndexAny(entry, "^/$"); idx >= 0 {
			entry = entry[:idx]
		}
		if host := normalizeHost(entry); host != "" {
			domains[host] = struct{}{}
		}
		return
	}

	// Full URL feed entry (OpenPhish / Phishing.Database).
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		if u, err := url.Parse(line); err == nil {
			if canon := canonicalURLKey(u); canon != "" {
				urls[canon] = struct{}{}
			}
		}
		return
	}

	// Hosts file format: "0.0.0.0 bad.domain" or "127.0.0.1 bad.domain"
	fields := strings.Fields(line)
	if len(fields) >= 2 && net.ParseIP(fields[0]) != nil {
		if host := normalizeHost(fields[1]); host != "" && host != "localhost" {
			domains[host] = struct{}{}
		}
		return
	}

	// Plain domain or IP literal.
	if len(fields) == 1 {
		if host := normalizeHost(fields[0]); host != "" {
			domains[host] = struct{}{}
		}
	}
}

func matchDomainSet(host string, set map[string]struct{}) bool {
	if len(set) == 0 {
		return false
	}
	cur := host
	for cur != "" {
		if _, ok := set[cur]; ok {
			return true
		}
		dot := strings.IndexByte(cur, '.')
		if dot < 0 {
			break
		}
		cur = cur[dot+1:]
	}
	return false
}

func normalizeHost(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimSuffix(h, ".")
	if h == "" || strings.ContainsAny(h, " \t/*?") {
		return ""
	}
	return h
}

func canonicalURLKey(u *url.URL) string {
	host := normalizeHost(u.Hostname())
	if host == "" {
		return ""
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return strings.ToLower(u.Scheme) + "://" + host + path
}
