package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	ErrInvalidURL     = errors.New("invalid or unsupported URL")
	ErrSSRFBlocked    = errors.New("blocked by SSRF protection")
	ErrTooManyRedirs  = errors.New("too many redirects")
	ErrBlocklistMatch = errors.New("blocked by threat intelligence blocklist")

	cgnatCIDR     = mustParseCIDR("100.64.0.0/10")
	benchmarkCIDR = mustParseCIDR("198.18.0.0/15")
	testNet1CIDR  = mustParseCIDR("192.0.2.0/24")
	testNet2CIDR  = mustParseCIDR("198.51.100.0/24")
	testNet3CIDR  = mustParseCIDR("203.0.113.0/24")
	ipv6DocCIDR   = mustParseCIDR("2001:db8::/32")
	awsIPv6Meta   = net.ParseIP("fd00:ec2::254")
)

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// GuardOptions configures SSRF and URL validation behavior.
type GuardOptions struct {
	AllowPrivateIPs bool
	Blocklist       *Blocklist
	MaxRedirects    int
}

// Guard validates target URLs and enforces socket-level SSRF protection.
type Guard struct {
	allowPrivate bool
	blocklist    *Blocklist
	maxRedirects int
}

// NewGuard creates a new URL and network guard.
func NewGuard(opts GuardOptions) *Guard {
	maxRedir := opts.MaxRedirects
	if maxRedir <= 0 {
		maxRedir = 5
	}
	return &Guard{
		allowPrivate: opts.AllowPrivateIPs,
		blocklist:    opts.Blocklist,
		maxRedirects: maxRedir,
	}
}

// IsRestrictedIP returns true if ip belongs to loopback, private, link-local,
// multicast, CGNAT, unspecified, metadata, or other non-routable ranges.
func IsRestrictedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Unwrap IPv4-mapped IPv6 addresses (e.g., ::ffff:127.0.0.1).
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip.To4() != nil {
		return cgnatCIDR.Contains(ip) || benchmarkCIDR.Contains(ip)
	}
	if ipv6DocCIDR.Contains(ip) || ip.Equal(awsIPv6Meta) {
		return true
	}
	return false
}

// ValidateURL parses and checks scheme, credentials, restricted hostnames, literal IPs, and blocklist.
func (g *Guard) ValidateURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty URL", ErrInvalidURL)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%w: disallowed scheme %q", ErrSSRFBlocked, u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in URL are not allowed", ErrInvalidURL)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	if !g.allowPrivate && isBlockedHostname(host) {
		return nil, fmt.Errorf("%w: restricted host %q", ErrSSRFBlocked, host)
	}
	if ip := net.ParseIP(host); ip != nil && !g.allowPrivate && IsRestrictedIP(ip) {
		return nil, fmt.Errorf("%w: restricted IP %s", ErrSSRFBlocked, ip.String())
	}
	if g.blocklist != nil {
		if reason, blocked := g.blocklist.MatchURL(u.String()); blocked {
			return nil, fmt.Errorf("%w: %s", ErrBlocklistMatch, reason)
		}
	}
	return u, nil
}

// CheckHostIPs resolves host via DNS and ensures every resolved address is safe.
func (g *Guard) CheckHostIPs(ctx context.Context, host string) ([]net.IP, error) {
	cleanHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if !g.allowPrivate && isBlockedHostname(cleanHost) {
		return nil, fmt.Errorf("%w: restricted host %q", ErrSSRFBlocked, cleanHost)
	}
	if ip := net.ParseIP(cleanHost); ip != nil {
		if !g.allowPrivate && IsRestrictedIP(ip) {
			return nil, fmt.Errorf("%w: restricted IP %s", ErrSSRFBlocked, ip.String())
		}
		return []net.IP{ip}, nil
	}

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", cleanHost)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed for %q: %w", cleanHost, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: no IP addresses resolved for %q", ErrSSRFBlocked, cleanHost)
	}
	if !g.allowPrivate {
		for _, ip := range ips {
			if IsRestrictedIP(ip) {
				return nil, fmt.Errorf("%w: host %q resolved to restricted IP %s", ErrSSRFBlocked, cleanHost, ip.String())
			}
		}
	}
	return ips, nil
}

// DialContext resolves DNS, validates all IPs, pins the validated IP, and verifies
// the socket address at connect time to neutralize DNS rebinding.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	ips, err := g.CheckHostIPs(ctx, host)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if g.allowPrivate {
				return nil
			}
			dialHost, _, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return fmt.Errorf("%w: invalid socket address", ErrSSRFBlocked)
			}
			ip := net.ParseIP(dialHost)
			if IsRestrictedIP(ip) {
				return fmt.Errorf("%w: socket connect to restricted IP %s", ErrSSRFBlocked, dialHost)
			}
			return nil
		},
	}

	var lastErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip.String(), port)
		conn, dialErr := dialer.DialContext(ctx, network, target)
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, lastErr
}

// NewHTTPClient creates an http.Client protected against SSRF, DNS rebinding, and malicious redirects.
func (g *Guard) NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 nil, // Never bypass SSRF controls via ambient proxy env vars.
		DialContext:           g.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= g.maxRedirects {
				return ErrTooManyRedirs
			}
			if _, err := g.ValidateURL(req.URL.String()); err != nil {
				return err
			}
			if _, err := g.CheckHostIPs(req.Context(), req.URL.Hostname()); err != nil {
				return err
			}
			return nil
		},
	}
}

func isBlockedHostname(host string) bool {
	if host == "localhost" ||
		host == "metadata.google.internal" ||
		host == "metadata" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".localdomain") {
		return true
	}
	return false
}

// IsTestNetIP is exposed only to distinguish RFC 5737 documentation IPs if needed.
func IsTestNetIP(ip net.IP) bool {
	return testNet1CIDR.Contains(ip) || testNet2CIDR.Contains(ip) || testNet3CIDR.Contains(ip)
}
