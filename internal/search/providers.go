package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/local-web-eyes/local-web-eyes/internal/browser"
	"github.com/local-web-eyes/local-web-eyes/internal/config"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
	"golang.org/x/net/html"
)

const maxSearchBodyBytes = 2 * 1024 * 1024

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// BuildDefaultProviders constructs the tiered search pipeline from Config.
func BuildDefaultProviders(cfg config.Config, guard *security.Guard, pool *browser.Pool) []Provider {
	client := guard.NewHTTPClient(cfg.Search.Timeout)
	var providers []Provider

	if strings.TrimSpace(cfg.Search.SearXNGURL) != "" {
		providers = append(providers, &SearXNGProvider{
			baseURL: strings.TrimRight(cfg.Search.SearXNGURL, "/"),
			client:  client,
		})
	}
	if cfg.Search.EnableWikipedia {
		providers = append(providers, &WikipediaProvider{
			client:    client,
			userAgent: cfg.Search.WikipediaUserAgent,
			sem:       make(chan struct{}, 2),
		})
	}
	if cfg.Search.EnableDuckDuckGo {
		providers = append(providers, &DuckDuckGoProvider{
			client:    client,
			userAgent: cfg.Fetch.UserAgent,
		})
	}
	if cfg.Search.EnableMojeek {
		providers = append(providers, &MojeekProvider{
			client:    client,
			userAgent: cfg.Fetch.UserAgent,
		})
	}
	if cfg.Search.EnableBrowserFallback && pool != nil && pool.Available() {
		providers = append(providers, &BrowserSearchProvider{pool: pool})
	}
	return providers
}

// SearXNGProvider queries an optional self-hosted SearXNG JSON endpoint (Tier 1).
type SearXNGProvider struct {
	baseURL string
	client  *http.Client
}

func (p *SearXNGProvider) Name() string { return "searxng" }
func (p *SearXNGProvider) Tier() int    { return 1 }

func (p *SearXNGProvider) Search(ctx context.Context, query string, limit int, timeRange string) ([]Result, error) {
	u := p.baseURL + "/search?q=" + url.QueryEscape(query) + "&format=json"
	if timeRange != "" {
		u += "&time_range=" + url.QueryEscape(timeRange)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		return nil, ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("searxng status %d", resp.StatusCode)
	}

	var payload struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			Engine        string `json:"engine"`
			PublishedDate string `json:"publishedDate"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSearchBodyBytes)).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]Result, 0, minInt(limit, len(payload.Results)))
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		meta := map[string]string{}
		if r.PublishedDate != "" {
			meta["published_date"] = r.PublishedDate
		}
		out = append(out, Result{
			Title:         r.Title,
			URL:           r.URL,
			Snippet:       r.Content,
			SourceEngines: []string{"searxng"},
			Metadata:      meta,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// WikipediaProvider queries Wikimedia's official Search API with a compliant User-Agent (Tier 1).
type WikipediaProvider struct {
	client    *http.Client
	userAgent string
	sem       chan struct{}
}

func (p *WikipediaProvider) Name() string { return "wikipedia" }
func (p *WikipediaProvider) Tier() int    { return 1 }

func (p *WikipediaProvider) Search(ctx context.Context, query string, limit int, _ string) ([]Result, error) {
	if p.sem != nil {
		select {
		case p.sem <- struct{}{}:
			defer func() { <-p.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	lang := detectWikipediaLang(query)
	capLimit := minInt(limit, 5)
	endpoint := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&format=json&utf8=1&srlimit=%d&srsearch=%s",
		lang, capLimit, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	ua := p.userAgent
	if ua == "" {
		ua = "local-web-eyes/1.0 (local-llm-research-agent)"
	}
	req.Header.Set("User-Agent", ua)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("wikipedia status %d", resp.StatusCode)
	}

	var payload struct {
		Query struct {
			Search []struct {
				Title     string `json:"title"`
				PageID    int64  `json:"pageid"`
				Snippet   string `json:"snippet"`
				Timestamp string `json:"timestamp"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSearchBodyBytes)).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]Result, 0, len(payload.Query.Search))
	for _, s := range payload.Query.Search {
		slug := strings.ReplaceAll(s.Title, " ", "_")
		snippet := html.UnescapeString(htmlTagRe.ReplaceAllString(s.Snippet, ""))
		out = append(out, Result{
			Title:         s.Title + " - Wikipedia",
			URL:           fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, url.PathEscape(slug)),
			Snippet:       snippet,
			SourceEngines: []string{"wikipedia"},
			Metadata:      map[string]string{"updated": s.Timestamp},
		})
	}
	return out, nil
}

func detectWikipediaLang(query string) string {
	for _, r := range query {
		if (r >= 0x1EA0 && r <= 0x1EF9) || strings.ContainsRune("ăâđêôơưĂÂĐÊÔƠƯ", r) {
			return "vi"
		}
	}
	return "en"
}

// DuckDuckGoProvider scrapes DuckDuckGo HTML results and detects 202/CAPTCHA throttling (Tier 2).
type DuckDuckGoProvider struct {
	client    *http.Client
	userAgent string
}

func (p *DuckDuckGoProvider) Name() string { return "duckduckgo" }
func (p *DuckDuckGoProvider) Tier() int    { return 2 }

func (p *DuckDuckGoProvider) Search(ctx context.Context, query string, limit int, timeRange string) ([]Result, error) {
	form := url.Values{}
	form.Set("q", query)
	if df := mapDDGTimeRange(timeRange); df != "" {
		form.Set("df", df)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		return nil, ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ddg status %d", resp.StatusCode)
	}

	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes))
	if err != nil {
		return nil, err
	}
	body := string(rawBytes)
	if strings.Contains(strings.ToLower(body), "anomaly-modal") || strings.Contains(strings.ToLower(body), "bots use duckduckgo") {
		return nil, ErrRateLimited
	}
	return parseDuckDuckGoHTML(body, limit, "duckduckgo")
}

// MojeekProvider queries Mojeek HTML search results as a secondary Tier 2 web engine.
type MojeekProvider struct {
	client    *http.Client
	userAgent string
}

func (p *MojeekProvider) Name() string { return "mojeek" }
func (p *MojeekProvider) Tier() int    { return 2 }

func (p *MojeekProvider) Search(ctx context.Context, query string, limit int, _ string) ([]Result, error) {
	endpoint := "https://www.mojeek.com/search?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		return nil, ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mojeek status %d", resp.StatusCode)
	}

	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes))
	if err != nil {
		return nil, err
	}
	return parseMojeekHTML(string(rawBytes), limit)
}

// BrowserSearchProvider uses headless Edge/Chrome (Tier 3) when HTTP scrapers hit bot blocks.
type BrowserSearchProvider struct {
	pool *browser.Pool
}

func (p *BrowserSearchProvider) Name() string { return "browser-ddg" }
func (p *BrowserSearchProvider) Tier() int    { return 3 }

func (p *BrowserSearchProvider) Search(ctx context.Context, query string, limit int, timeRange string) ([]Result, error) {
	if p.pool == nil || !p.pool.Available() {
		return nil, browser.ErrBrowserDisabled
	}
	searchCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()

	target := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	if df := mapDDGTimeRange(timeRange); df != "" {
		target += "&df=" + df
	}
	rendered, err := p.pool.RenderHTMLWithWait(searchCtx, target, ".result__a")
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoHTML(rendered, limit, "browser-ddg")
}

func parseDuckDuckGoHTML(rawHTML string, limit int, engine string) ([]Result, error) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return nil, err
	}

	var results []Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= limit {
			return
		}
		if n.Type == html.ElementNode && hasClass(n, "result") && !hasClass(n, "result--ad") {
			title, link := findAnchorByClass(n, "result__a")
			snippet := findTextByClass(n, "result__snippet")
			resolved := unwrapDDGRedirect(link)
			if title != "" && resolved != "" {
				results = append(results, Result{
					Title:         title,
					URL:           resolved,
					Snippet:       snippet,
					SourceEngines: []string{engine},
				})
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results, nil
}

func parseMojeekHTML(rawHTML string, limit int) ([]Result, error) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return nil, err
	}

	var results []Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= limit {
			return
		}
		if n.Type == html.ElementNode && n.Data == "li" {
			title, link := findAnchorByClass(n, "title")
			snippet := findTextByClass(n, "s")
			if title != "" && strings.HasPrefix(link, "http") {
				results = append(results, Result{
					Title:         title,
					URL:           link,
					Snippet:       snippet,
					SourceEngines: []string{"mojeek"},
				})
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results, nil
}

func unwrapDDGRedirect(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "//") {
		trimmed = "https:" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if strings.Contains(u.Hostname(), "duckduckgo.com") && u.Path == "/l/" {
		if uddg := u.Query().Get("uddg"); uddg != "" {
			return uddg
		}
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.String()
	}
	return ""
}

func mapDDGTimeRange(tr string) string {
	switch tr {
	case "day":
		return "d"
	case "week":
		return "w"
	case "month":
		return "m"
	case "year":
		return "y"
	default:
		return ""
	}
}

func hasClass(n *html.Node, target string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, cls := range strings.Fields(a.Val) {
				if cls == target {
					return true
				}
			}
		}
	}
	return false
}

func findAnchorByClass(root *html.Node, className string) (string, string) {
	var title, href string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if href != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, className) {
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
			title = strings.TrimSpace(extractNodeText(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return title, href
}

func findTextByClass(root *html.Node, className string) string {
	var text string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if text != "" {
			return
		}
		if n.Type == html.ElementNode && hasClass(n, className) {
			text = strings.TrimSpace(extractNodeText(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return text
}

func extractNodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
