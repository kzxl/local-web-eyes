package security

import (
	"net"
	"testing"
)

func TestIsRestrictedIP(t *testing.T) {
	cases := []struct {
		ip       string
		blocked  bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.5.4", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true}, // CGNAT RFC 6598
		{"0.0.0.0", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"1.1.1.1", false},
		{"8.8.8.8", false},
		{"93.184.216.34", false},
		{"2606:4700:4700::1111", false},
	}

	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", tc.ip)
		}
		got := IsRestrictedIP(ip)
		if got != tc.blocked {
			t.Errorf("IsRestrictedIP(%s) = %v, want %v", tc.ip, got, tc.blocked)
		}
	}
}

func TestValidateTargetURL(t *testing.T) {
	g := NewGuard(GuardOptions{})

	badURLs := []string{
		"",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"gopher://127.0.0.1:6379/_INFO",
		"http://user:pass@example.com/",
		"http://localhost:8080/admin",
		"http://127.0.0.1/metrics",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/",
		"https://sub.localhost/test",
	}

	for _, raw := range badURLs {
		if _, err := g.ValidateURL(raw); err == nil {
			t.Errorf("expected error for %q, got nil", raw)
		}
	}

	okURLs := []string{
		"https://example.com/docs",
		"http://93.184.216.34/index.html",
	}
	for _, raw := range okURLs {
		if _, err := g.ValidateURL(raw); err != nil {
			t.Errorf("expected %q to pass syntax/policy check, got %v", raw, err)
		}
	}
}
