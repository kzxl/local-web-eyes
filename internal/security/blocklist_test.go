package security

import "testing"

func TestBlocklistMatchingAndParsers(t *testing.T) {
	bl := NewBlocklist(nil, nil)
	bl.LoadLines([]string{
		"# comment",
		"||evil-phish.example^",
		"0.0.0.0 malware-c2.example",
		"https://bad-site.org/login/steal",
		"198.51.100.77",
	})

	blocked := []string{
		"https://evil-phish.example/page",
		"https://sub.evil-phish.example/login",
		"http://malware-c2.example/",
		"https://bad-site.org/login/steal?id=1",
		"http://198.51.100.77/payload",
	}
	for _, u := range blocked {
		if reason, hit := bl.MatchURL(u); !hit {
			t.Errorf("expected %q to be blocked, got hit=false", u)
		} else if reason == "" {
			t.Errorf("expected non-empty block reason for %q", u)
		}
	}

	allowed := []string{
		"https://good-example.com/docs",
		"https://bad-site.org/about", // only exact URL path /login/steal was in OpenPhish-style line
	}
	for _, u := range allowed {
		if reason, hit := bl.MatchURL(u); hit {
			t.Errorf("expected %q to be allowed, got blocked (%s)", u, reason)
		}
	}
}

func TestBlocklistAllowlistOverride(t *testing.T) {
	bl := NewBlocklist([]string{"trusted.example"}, []string{"blocked.example"})
	bl.LoadLines([]string{"||trusted.example^"})

	if _, hit := bl.MatchURL("https://docs.trusted.example/guide"); hit {
		t.Fatalf("expected allowlist domain to override feed entry")
	}
	if _, hit := bl.MatchURL("https://blocked.example/x"); !hit {
		t.Fatalf("expected custom denylist domain to be blocked")
	}
}

func TestBlocklistSnapshotRoundTrip(t *testing.T) {
	src := NewBlocklist(nil, nil)
	src.LoadLines([]string{
		"||evil-phish.example^",
		"0.0.0.0 malware-c2.example",
		"https://bad-site.org/login/steal",
	})

	snap := src.ExportSnapshot()
	if snap == "" {
		t.Fatal("expected non-empty snapshot")
	}

	dst := NewBlocklist(nil, nil)
	domains, urls := dst.ImportSnapshot(snap)
	if domains != 2 || urls != 1 {
		t.Fatalf("expected 2 domains and 1 url imported, got domains=%d urls=%d", domains, urls)
	}
	if _, hit := dst.MatchURL("https://sub.evil-phish.example/a"); !hit {
		t.Errorf("expected imported snapshot to block subdomain of evil-phish.example")
	}
	if _, hit := dst.MatchURL("http://malware-c2.example/"); !hit {
		t.Errorf("expected imported snapshot to block malware-c2.example")
	}
	if _, hit := dst.MatchURL("https://bad-site.org/login/steal?x=1"); !hit {
		t.Errorf("expected imported snapshot to block bad-site.org URL")
	}
}
