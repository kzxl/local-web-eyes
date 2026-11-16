package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/local-web-eyes/local-web-eyes/internal/cache"
	"github.com/local-web-eyes/local-web-eyes/internal/config"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
)

var ErrRateLimited = errors.New("provider rate limited or bot-challenged")

// Result is a normalized, blocklist-filtered search hit.
type Result struct {
	Title         string            `json:"title"`
	URL           string            `json:"url"`
	Snippet       string            `json:"snippet"`
	SourceEngines []string          `json:"source_engines,omitempty"`
	Score         float64           `json:"score"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// SearchResponse is the structured JSON payload returned by web_search.
type SearchResponse struct {
	Query           string   `json:"query"`
	TimeRange       string   `json:"time_range,omitempty"`
	Results         []Result `json:"results"`
	LocalHits       []cache.LocalMatch `json:"local_cache_hits,omitempty"`
	UsedProviders   []string `json:"used_providers,omitempty"`
	FilteredCount   int      `json:"filtered_malicious_count,omitempty"`
	FromCache       bool     `json:"from_cache,omitempty"`
	UntrustedNotice string   `json:"untrusted_notice"`
}

// MultiSearchResponse aggregates results across multiple queries with cross-query deduplication.
type MultiSearchResponse struct {
	Queries         []string         `json:"queries"`
	Results         []SearchResponse `json:"per_query"`
	UniqueResults   []Result         `json:"merged_unique_results"`
	UntrustedNotice string           `json:"untrusted_notice"`
}

// Provider abstracts any search engine (SearXNG, Wikipedia, DDG, Mojeek, Browser fallback).
type Provider interface {
	Name() string
	Tier() int
	Search(ctx context.Context, query string, limit int, timeRange string) ([]Result, error)
}

// CircuitBreaker prevents repeated calls to throttled/blocked upstream search engines.
type CircuitBreaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	failures  int
	openUntil time.Time
}

// NewCircuitBreaker creates a breaker that opens after threshold failures for cooldown.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 2
	}
	if cooldown <= 0 {
		cooldown = 3 * time.Minute
	}
	return &CircuitBreaker{threshold: threshold, cooldown: cooldown}
}

// Allow reports whether a request may proceed.
func (c *CircuitBreaker) Allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.openUntil.IsZero() {
		return true
	}
	return time.Now().After(c.openUntil)
}

// RecordSuccess resets failure state.
func (c *CircuitBreaker) RecordSuccess() {
	c.mu.Lock()
	c.failures = 0
	c.openUntil = time.Time{}
	c.mu.Unlock()
}

// RecordFailure increments consecutive failures and opens the breaker at threshold.
func (c *CircuitBreaker) RecordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= c.threshold {
		c.openUntil = time.Now().Add(c.cooldown)
	}
}

type managedProvider struct {
	provider Provider
	breaker  *CircuitBreaker
}

// Router orchestrates tiered search providers, RRF ranking, caching, and blocklist filtering.
type Router struct {
	cfg       config.Config
	guard     *security.Guard
	store     *cache.Store
	providers []managedProvider
}

// NewRouter creates a Search Router from pluggable Provider instances.
func NewRouter(cfg config.Config, guard *security.Guard, store *cache.Store, providers ...Provider) *Router {
	managed := make([]managedProvider, 0, len(providers))
	for _, p := range providers {
		if p == nil {
			continue
		}
		managed = append(managed, managedProvider{
			provider: p,
			breaker:  NewCircuitBreaker(2, 3*time.Minute),
		})
	}
	return &Router{
		cfg:       cfg,
		guard:     guard,
		store:     store,
		providers: managed,
	}
}

// Search executes Tier 1/2 providers concurrently, falls back to Tier 3 if needed,
// fuses ranks with RRF, and filters out malicious/SSRF URLs.
func (r *Router) Search(ctx context.Context, query string, limit int, timeRange string) (SearchResponse, error) {
	cleanQuery := security.CleanInline(query)
	if cleanQuery == "" {
		return SearchResponse{}, errors.New("search query cannot be empty")
	}
	if limit <= 0 {
		limit = r.cfg.Search.DefaultLimit
	}
	if limit > r.cfg.Search.MaxLimit {
		limit = r.cfg.Search.MaxLimit
	}
	normRange := normalizeTimeRange(timeRange)

	cacheKey := searchCacheKey(cleanQuery, limit, normRange)
	if r.store != nil {
		if raw, ok, err := r.store.GetSearch(ctx, cacheKey); err == nil && ok {
			var cached SearchResponse
			if json.Unmarshal([]byte(raw), &cached) == nil {
				cached.FromCache = true
				return cached, nil
			}
		}
	}

	timeout := r.cfg.Search.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	searchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	primaryResults, used, filtered := r.runTierGroup(searchCtx, cleanQuery, limit, normRange, func(tier int) bool {
		return tier <= 2
	})

	// Escalate to Tier 3 (browser-backed search) if primary tiers returned zero safe hits,
	// or if only Wikipedia returned hits while general primary web engines yielded nothing.
	if r.needsTier3Fallback(primaryResults) {
		t3Results, t3Used, t3Filtered := r.runTierGroup(ctx, cleanQuery, limit, normRange, func(tier int) bool {
			return tier >= 3
		})
		primaryResults = append(primaryResults, t3Results...)
		used = append(used, t3Used...)
		filtered += t3Filtered
	}

	fused := fuseAndDedupe(primaryResults)
	if len(fused) > limit {
		fused = fused[:limit]
	}

	var localHits []cache.LocalMatch
	if r.store != nil {
		if hits, err := r.store.SearchLocalPages(ctx, cleanQuery, 3); err == nil {
			localHits = hits
		}
	}

	resp := SearchResponse{
		Query:           cleanQuery,
		TimeRange:       normRange,
		Results:         fused,
		LocalHits:       localHits,
		UsedProviders:   used,
		FilteredCount:   filtered,
		UntrustedNotice: "Search snippets and URLs originate from external websites. Verify before trusting.",
	}

	if r.store != nil && len(resp.Results) > 0 {
		if raw, err := json.Marshal(resp); err == nil {
			_ = r.store.PutSearch(ctx, cacheKey, string(raw), r.cfg.Search.CacheTTL)
		}
	}
	return resp, nil
}

// SearchMulti runs up to MaxMultiQueries queries with bounded concurrency and URL deduplication.
func (r *Router) SearchMulti(ctx context.Context, queries []string, limitPerQuery int, timeRange string) (MultiSearchResponse, error) {
	maxQ := r.cfg.Search.MaxMultiQueries
	if maxQ <= 0 {
		maxQ = 5
	}
	cleanQueries := make([]string, 0, len(queries))
	seenQ := make(map[string]struct{})
	for _, q := range queries {
		cq := security.CleanInline(q)
		if cq == "" {
			continue
		}
		lk := strings.ToLower(cq)
		if _, ok := seenQ[lk]; ok {
			continue
		}
		seenQ[lk] = struct{}{}
		cleanQueries = append(cleanQueries, cq)
		if len(cleanQueries) >= maxQ {
			break
		}
	}
	if len(cleanQueries) == 0 {
		return MultiSearchResponse{}, errors.New("at least one non-empty query is required")
	}

	perQuery := make([]SearchResponse, len(cleanQueries))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, q := range cleanQueries {
		wg.Add(1)
		go func(idx int, query string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			res, err := r.Search(ctx, query, limitPerQuery, timeRange)
			if err != nil {
				perQuery[idx] = SearchResponse{Query: query, UntrustedNotice: "query failed: " + err.Error()}
				return
			}
			perQuery[idx] = res
		}(i, q)
	}
	wg.Wait()

	// Merge and deduplicate across all queries.
	var all []rankedHit
	for _, qr := range perQuery {
		for rank, item := range qr.Results {
			all = append(all, rankedHit{result: item, rank: rank})
		}
	}

	return MultiSearchResponse{
		Queries:         cleanQueries,
		Results:         perQuery,
		UniqueResults:   fuseAndDedupe(all),
		UntrustedNotice: "Search snippets and URLs originate from external websites. Verify before trusting.",
	}, nil
}

type rankedHit struct {
	result Result
	rank   int
}

func (r *Router) runTierGroup(ctx context.Context, query string, limit int, timeRange string, matchTier func(int) bool) ([]rankedHit, []string, int) {
	type outcome struct {
		name     string
		hits     []rankedHit
		filtered int
	}

	var targets []managedProvider
	for _, mp := range r.providers {
		if matchTier(mp.provider.Tier()) && mp.breaker.Allow() {
			targets = append(targets, mp)
		}
	}
	if len(targets) == 0 {
		return nil, nil, 0
	}

	ch := make(chan outcome, len(targets))
	for _, mp := range targets {
		go func(m managedProvider) {
			raw, err := m.provider.Search(ctx, query, limit, timeRange)
			if err != nil {
				if errors.Is(err, ErrRateLimited) {
					m.breaker.RecordFailure()
				}
				ch <- outcome{name: m.provider.Name()}
				return
			}
			if len(raw) == 0 {
				ch <- outcome{name: m.provider.Name()}
				return
			}
			m.breaker.RecordSuccess()

			var hits []rankedHit
			filtered := 0
			for idx, item := range raw {
				if r.guard != nil {
					if _, vErr := r.guard.ValidateURL(item.URL); vErr != nil {
						filtered++
						continue
					}
				}
				item.Title = security.CleanInline(item.Title)
				item.Snippet = security.CleanInline(item.Snippet)
				if len(item.SourceEngines) == 0 {
					item.SourceEngines = []string{m.provider.Name()}
				}
				hits = append(hits, rankedHit{result: item, rank: idx})
			}
			ch <- outcome{name: m.provider.Name(), hits: hits, filtered: filtered}
		}(mp)
	}

	var allHits []rankedHit
	var used []string
	totalFiltered := 0
	for range targets {
		out := <-ch
		totalFiltered += out.filtered
		if len(out.hits) > 0 {
			used = append(used, out.name)
			allHits = append(allHits, out.hits...)
		}
	}
	sort.Strings(used)
	return allHits, used, totalFiltered
}

func (r *Router) needsTier3Fallback(hits []rankedHit) bool {
	if len(hits) == 0 {
		return true
	}
	hasGeneralPrimary := false
	for _, mp := range r.providers {
		if mp.provider.Tier() <= 2 && mp.provider.Name() != "wikipedia" {
			hasGeneralPrimary = true
			break
		}
	}
	if !hasGeneralPrimary {
		return false
	}
	for _, h := range hits {
		for _, eng := range h.result.SourceEngines {
			if eng != "wikipedia" {
				return false
			}
		}
	}
	return true
}

func fuseAndDedupe(hits []rankedHit) []Result {
	const rrfK = 60.0
	type agg struct {
		result  Result
		engines map[string]struct{}
	}
	byURL := make(map[string]*agg)

	for _, h := range hits {
		key := canonicalResultKey(h.result.URL)
		if key == "" {
			continue
		}
		score := h.result.Score
		if score <= 0 {
			score = 1.0 / (rrfK + float64(h.rank+1))
		}
		entry, exists := byURL[key]
		if !exists {
			engMap := make(map[string]struct{})
			for _, e := range h.result.SourceEngines {
				engMap[e] = struct{}{}
			}
			cp := h.result
			cp.Score = score
			byURL[key] = &agg{result: cp, engines: engMap}
			continue
		}
		entry.result.Score += score
		if len(h.result.Snippet) > len(entry.result.Snippet) {
			entry.result.Snippet = h.result.Snippet
		}
		if entry.result.Title == "" {
			entry.result.Title = h.result.Title
		}
		for _, e := range h.result.SourceEngines {
			entry.engines[e] = struct{}{}
		}
	}

	out := make([]Result, 0, len(byURL))
	for _, entry := range byURL {
		engines := make([]string, 0, len(entry.engines))
		for e := range entry.engines {
			engines = append(engines, e)
		}
		sort.Strings(engines)
		entry.result.SourceEngines = engines
		out = append(out, entry.result)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].URL < out[j].URL
	})
	return out
}

func canonicalResultKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	if path == "" {
		path = "/"
	}
	q := u.Query()
	for _, tracking := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "fbclid", "gclid"} {
		q.Del(tracking)
	}
	encodedQ := q.Encode()
	base := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + path
	if encodedQ != "" {
		return base + "?" + encodedQ
	}
	return base
}

func normalizeTimeRange(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "day", "d", "24h":
		return "day"
	case "week", "w", "7d":
		return "week"
	case "month", "m", "30d":
		return "month"
	case "year", "y", "1y":
		return "year"
	default:
		return ""
	}
}

func searchCacheKey(query string, limit int, timeRange string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", strings.ToLower(query), limit, timeRange)))
	return hex.EncodeToString(sum[:])
}
