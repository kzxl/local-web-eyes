package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/local-web-eyes/local-web-eyes/internal/config"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
)

type stubProvider struct {
	name    string
	tier    int
	results []Result
	err     error
	called  int
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Tier() int    { return s.tier }
func (s *stubProvider) Search(ctx context.Context, query string, limit int, timeRange string) ([]Result, error) {
	s.called++
	return s.results, s.err
}

func TestRouterRRFDedupeAndBlocklistFilter(t *testing.T) {
	bl := security.NewBlocklist(nil, []string{"evil.example"})
	guard := security.NewGuard(security.GuardOptions{Blocklist: bl})

	p1 := &stubProvider{
		name: "p1",
		tier: 1,
		results: []Result{
			{Title: "Evil Phish", URL: "https://evil.example/login", Snippet: "steal"},
			{Title: "Go Docs", URL: "https://go.dev/doc/", Snippet: "Official Go docs"},
			{Title: "MCP Spec", URL: "https://modelcontextprotocol.io/docs", Snippet: "Protocol spec"},
		},
	}
	p2 := &stubProvider{
		name: "p2",
		tier: 2,
		results: []Result{
			{Title: "MCP Spec Mirror", URL: "https://modelcontextprotocol.io/docs", Snippet: "Canonical MCP docs"},
			{Title: "Ollama", URL: "https://ollama.com/", Snippet: "Local LLMs"},
		},
	}
	p3 := &stubProvider{
		name: "browser-fallback",
		tier: 3,
		results: []Result{
			{Title: "Should Not Run", URL: "https://unused.example/"},
		},
	}

	r := NewRouter(config.Default(), guard, nil, p1, p2, p3)
	resp, err := r.Search(context.Background(), "go mcp", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if p3.called != 0 {
		t.Errorf("expected Tier 3 browser fallback not to run when Tier 1/2 succeeded, called=%d", p3.called)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 safe deduplicated results, got %d: %+v", len(resp.Results), resp.Results)
	}
	for _, res := range resp.Results {
		if res.URL == "https://evil.example/login" {
			t.Errorf("malicious URL leaked into search results")
		}
	}
	// Shared URL across p1 and p2 should rank first via RRF.
	if resp.Results[0].URL != "https://modelcontextprotocol.io/docs" {
		t.Errorf("expected RRF top result to be modelcontextprotocol.io, got %s", resp.Results[0].URL)
	}
	if len(resp.Results[0].SourceEngines) != 2 {
		t.Errorf("expected merged SourceEngines=2, got %v", resp.Results[0].SourceEngines)
	}
}

func TestRouterFallsBackToTier3AndTripsBreaker(t *testing.T) {
	guard := security.NewGuard(security.GuardOptions{})
	p2 := &stubProvider{name: "ddg", tier: 2, err: ErrRateLimited}
	p3 := &stubProvider{
		name: "browser-ddg",
		tier: 3,
		results: []Result{
			{Title: "Recovered Result", URL: "https://example.org/ok", Snippet: "Via browser"},
		},
	}

	r := NewRouter(config.Default(), guard, nil, p2, p3)
	resp, err := r.Search(context.Background(), "test query", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if p3.called != 1 || len(resp.Results) != 1 {
		t.Fatalf("expected Tier 3 fallback to recover results, p3.called=%d len=%d", p3.called, len(resp.Results))
	}

	// Second failure should trip the circuit breaker on p2.
	_, _ = r.Search(context.Background(), "second query", 5, "")
	p2CallsAfterTrip := p2.called
	_, _ = r.Search(context.Background(), "third query", 5, "")
	if p2.called != p2CallsAfterTrip {
		t.Errorf("expected circuit breaker to skip p2 after repeated rate limits")
	}
	if !errors.Is(ErrRateLimited, ErrRateLimited) {
		t.Fatal("unreachable")
	}
}

func TestCircuitBreakerTransitions(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected initial breaker closed")
	}
	cb.RecordFailure()
	if !cb.Allow() {
		t.Fatal("expected breaker still closed after 1 failure")
	}
	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("expected breaker open after 2 failures")
	}
	time.Sleep(60 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected breaker half-open after cooldown")
	}
	cb.RecordSuccess()
	if !cb.Allow() {
		t.Fatal("expected breaker closed after success")
	}
}

func TestRouterEscalatesToTier3WhenOnlyWikipediaSucceeds(t *testing.T) {
	guard := security.NewGuard(security.GuardOptions{})
	wiki := &stubProvider{
		name: "wikipedia",
		tier: 1,
		results: []Result{
			{Title: "Go - Wikipedia", URL: "https://en.wikipedia.org/wiki/Go", Snippet: "Encyclopedia entry"},
		},
	}
	ddg := &stubProvider{name: "duckduckgo", tier: 2, err: ErrRateLimited}
	browserDDG := &stubProvider{
		name: "browser-ddg",
		tier: 3,
		results: []Result{
			{Title: "Go Official Site", URL: "https://go.dev/", Snippet: "Build simple, secure, scalable systems with Go"},
		},
	}

	r := NewRouter(config.Default(), guard, nil, wiki, ddg, browserDDG)
	resp, err := r.Search(context.Background(), "golang", 5, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if browserDDG.called != 1 {
		t.Fatalf("expected Tier 3 fallback to run when only Wikipedia returned hits, called=%d", browserDDG.called)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected both Wikipedia and Tier 3 results, got %d: %+v", len(resp.Results), resp.Results)
	}
}

func TestDetectWikipediaLang(t *testing.T) {
	if got := detectWikipediaLang("lập trình đồng thời trong Go"); got != "vi" {
		t.Errorf("expected vi for Vietnamese query, got %q", got)
	}
	if got := detectWikipediaLang("Go concurrency patterns"); got != "en" {
		t.Errorf("expected en for English query, got %q", got)
	}
}
