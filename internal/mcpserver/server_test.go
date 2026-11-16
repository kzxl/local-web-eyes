package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/local-web-eyes/local-web-eyes/internal/config"
	"github.com/local-web-eyes/local-web-eyes/internal/fetch"
	"github.com/local-web-eyes/local-web-eyes/internal/search"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mockSearchProvider struct {
	targetURL string
}

func (m *mockSearchProvider) Name() string { return "mock" }
func (m *mockSearchProvider) Tier() int    { return 1 }
func (m *mockSearchProvider) Search(_ context.Context, query string, _ int, _ string) ([]search.Result, error) {
	return []search.Result{
		{
			Title:         "Result for " + query,
			URL:           m.targetURL,
			Snippet:       "Clean snippet from mock engine",
			SourceEngines: []string{"mock"},
		},
	}, nil
}

func TestStreamableHTTPMCPToolsEndToEnd(t *testing.T) {
	webSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Local Web Eyes Docs</title></head><body><main>
			<h1>Local Web Eyes</h1>
			<p>Zero-cost MCP web access for Qwen 3.5 9B with SSRF protection.</p>
		</main></body></html>`)
	}))
	defer webSrv.Close()

	cfg := config.Default()
	cfg.Security.AllowPrivateIPs = true
	cfg.Browser.Enabled = false

	bl := security.NewBlocklist(nil, nil)
	guard := security.NewGuard(security.GuardOptions{AllowPrivateIPs: true, Blocklist: bl})
	fetcher := fetch.NewFetcher(cfg, guard, nil, nil)
	crawler := fetch.NewCrawler(fetcher, cfg)
	router := search.NewRouter(cfg, guard, nil, &mockSearchProvider{targetURL: webSrv.URL + "/docs"})

	deps := Deps{
		Config:    cfg,
		Router:    router,
		Fetcher:   fetcher,
		Crawler:   crawler,
		Blocklist: bl,
	}
	srv := NewServer(deps)
	mcpHTTP := httptest.NewServer(NewHTTPMux(srv, deps))
	defer mcpHTTP.Close()

	// Verify /healthz endpoint.
	hResp, err := http.Get(mcpHTTP.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	_ = hResp.Body.Close()
	if hResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", hResp.StatusCode)
	}

	// Connect official MCP client over Streamable HTTP transport.
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: mcpHTTP.URL + "/mcp",
	}, nil)
	if err != nil {
		t.Fatalf("MCP client connect failed: %v", err)
	}
	defer session.Close()

	toolsRes, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(toolsRes.Tools) != 4 {
		t.Fatalf("expected 4 MCP tools, got %d", len(toolsRes.Tools))
	}

	// Invoke web_search via MCP.
	searchCall, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "qwen mcp", "limit": 3},
	})
	if err != nil || searchCall.IsError {
		t.Fatalf("web_search tool failed: err=%v res=%+v", err, searchCall)
	}
	var sr search.SearchResponse
	if err := json.Unmarshal([]byte(searchCall.Content[0].(*mcp.TextContent).Text), &sr); err != nil {
		t.Fatalf("decode web_search response: %v", err)
	}
	if len(sr.Results) != 1 || sr.Results[0].URL != webSrv.URL+"/docs" {
		t.Fatalf("unexpected web_search results: %+v", sr.Results)
	}

	// Invoke web_fetch via MCP.
	fetchCall, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_fetch",
		Arguments: map[string]any{"url": webSrv.URL + "/docs"},
	})
	if err != nil || fetchCall.IsError {
		t.Fatalf("web_fetch tool failed: err=%v res=%+v", err, fetchCall)
	}
	var fr fetch.FetchResult
	if err := json.Unmarshal([]byte(fetchCall.Content[0].(*mcp.TextContent).Text), &fr); err != nil {
		t.Fatalf("decode web_fetch response: %v", err)
	}
	if !strings.Contains(fr.Title, "Local Web Eyes") || fr.ReturnedChars == 0 {
		t.Fatalf("unexpected web_fetch result: %+v", fr)
	}

	// Invoke web_search_multi via MCP.
	multiCall, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search_multi",
		Arguments: map[string]any{"queries": []string{"qwen mcp", "local llm"}, "limit_per_query": 2},
	})
	if err != nil || multiCall.IsError {
		t.Fatalf("web_search_multi tool failed: err=%v res=%+v", err, multiCall)
	}

	// Invoke web_crawl via MCP.
	crawlCall, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_crawl",
		Arguments: map[string]any{"url": webSrv.URL + "/docs", "depth": 1, "max_pages": 2},
	})
	if err != nil || crawlCall.IsError {
		t.Fatalf("web_crawl tool failed: err=%v res=%+v", err, crawlCall)
	}
}
