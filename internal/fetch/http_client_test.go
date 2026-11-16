package fetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
