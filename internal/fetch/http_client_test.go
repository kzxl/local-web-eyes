package fetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local-web-eyes/local-web-eyes/internal/cache"
	"github.com/local-web-eyes/local-web-eyes/internal/config"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
)

func newTestFetcher(t *testing.T) *Fetcher {
	t.Helper()
	cfg := config.Default()
	cfg.Security.AllowPrivateIPs = true
	cfg.Fetch.MaxResponseBytes = 4096
	cfg.Fetch.Timeout = 5 * time.Second
	cfg.Browser.Enabled = false

	guard := security.NewGuard(security.GuardOptions{
		AllowPrivateIPs: true,
		MaxRedirects:    3,
	})
	return NewFetcher(cfg, guard, nil, nil)
}

func TestFetchRejectsExecutableDisguisedAsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("MZ\x90\x00fake-pe-binary"))
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	_, err := f.Fetch(context.Background(), srv.URL+"/doc", FetchOptions{})
	if err == nil {
		t.Fatalf("expected error for MZ binary payload, got nil")
	}
}

func TestFetchRejectsBinaryMimeAndExtension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	if _, err := f.Fetch(context.Background(), srv.URL+"/setup.exe", FetchOptions{}); err == nil {
		t.Fatalf("expected error for .exe URL")
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/download", FetchOptions{}); err == nil {
		t.Fatalf("expected error for application/octet-stream")
	}
}

func TestFetchGuardsAgainstGzipDecompressionBomb(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(strings.Repeat("A", 32*1024)))
	_ = gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	res, err := f.Fetch(context.Background(), srv.URL+"/bomb", FetchOptions{MaxChars: 10000})
	if err != nil {
		t.Fatalf("expected capped read without panic, got err=%v", err)
	}
	if len(res.Content) > 4096 {
		t.Fatalf("expected response capped at MaxResponseBytes=4096, got %d", len(res.Content))
	}
}

func TestFetchRedirectCachesOriginalAndFinalURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old":
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		case "/new":
			hits.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><head><title>New Location</title></head><body><main><article><h1>New Location</h1><p>This article moved permanently to a canonical URL with enough content for extraction.</p></article></main></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store, err := cache.Open(filepath.Join(t.TempDir(), "redirect.db"))
	if err != nil {
		t.Fatalf("cache.Open failed: %v", err)
	}
	defer store.Close()

	cfg := config.Default()
	cfg.Security.AllowPrivateIPs = true
	cfg.Browser.Enabled = false
	guard := security.NewGuard(security.GuardOptions{AllowPrivateIPs: true, MaxRedirects: 3})
	f := NewFetcher(cfg, guard, store, nil)

	ctx := context.Background()
	res1, err := f.Fetch(ctx, srv.URL+"/old", FetchOptions{})
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if res1.URL != srv.URL+"/new" {
		t.Fatalf("expected redirected final URL %s/new, got %s", srv.URL, res1.URL)
	}

	// Second fetch to original /old URL must hit SQLite cache without hitting HTTP server again.
	res2, err := f.Fetch(ctx, srv.URL+"/old", FetchOptions{})
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	if !strings.HasPrefix(res2.SourceMode, "cache:") {
		t.Fatalf("expected cache hit on redirected original URL, got source_mode=%q", res2.SourceMode)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected only 1 upstream HTTP hit, got %d", hits.Load())
	}
}
