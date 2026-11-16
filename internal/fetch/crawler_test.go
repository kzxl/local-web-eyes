package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCrawlerRespectsSameOriginDepthPagesAndBudget(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `<html><head><title>Root</title></head><body><main>
				<h1>Root Page</h1><p>Welcome to the root documentation page with enough words.</p>
				<a href="/a">Page A</a>
				<a href="/b">Page B</a>
				<a href="https://external.example.com/out">External</a>
			</main></body></html>`)
		case "/a":
			fmt.Fprintf(w, `<html><head><title>Page A</title></head><body><main>
				<h1>Page A</h1><p>Details on topic A with sufficient content to extract cleanly.</p>
				<a href="/a/deep">Deep Page</a>
			</main></body></html>`)
		case "/b":
			fmt.Fprintf(w, `<html><head><title>Page B</title></head><body><main>
				<h1>Page B</h1><p>Details on topic B with sufficient content to extract cleanly.</p>
			</main></body></html>`)
		case "/a/deep":
			fmt.Fprintf(w, `<html><head><title>Deep</title></head><body><main>
				<h1>Deep Page</h1><p>Depth 2 content should be skipped when depth=1.</p>
			</main></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	crawler := NewCrawler(f, f.cfg)

	res, err := crawler.Crawl(context.Background(), srv.URL+"/", CrawlOptions{
		Depth:         1,
		MaxPages:      5,
		MaxTotalChars: 5000,
	})
	if err != nil {
		t.Fatalf("Crawl failed: %v", err)
	}
	if len(res.Pages) != 3 {
		t.Fatalf("expected 3 pages (root, /a, /b) at depth=1, got %d: %+v", len(res.Pages), res.Pages)
	}
	for _, p := range res.Pages {
		if p.URL == srv.URL+"/a/deep" {
			t.Errorf("depth=2 page should not have been crawled at depth=1")
		}
		if p.URL == "https://external.example.com/out" {
			t.Errorf("external domain should not have been crawled")
		}
	}
}
