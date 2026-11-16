package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteCacheAndFTS5Recall(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Search cache hit & expiry.
	if err := store.PutSearch(ctx, "k1", `[{"title":"Go MCP"}]`, 5*time.Minute); err != nil {
		t.Fatalf("PutSearch failed: %v", err)
	}
	got, ok, err := store.GetSearch(ctx, "k1")
	if err != nil || !ok || got != `[{"title":"Go MCP"}]` {
		t.Fatalf("GetSearch got=(%q,%v,%v)", got, ok, err)
	}

	if err := store.PutSearch(ctx, "expired", `[]`, -1*time.Second); err != nil {
		t.Fatalf("PutSearch expired failed: %v", err)
	}
	if _, ok, err := store.GetSearch(ctx, "expired"); err != nil || ok {
		t.Fatalf("expected expired key miss, got ok=%v err=%v", ok, err)
	}

	// Page cache + FTS5 recall.
	err = store.PutPage(ctx, CachedPage{
		URL:          "https://go.dev/doc/effective_go",
		Title:        "Effective Go",
		Markdown:     "# Effective Go\n\nGo concurrency uses goroutines and channels cleanly.",
		HeadingsJSON: `["Effective Go"]`,
		MetadataJSON: `{"lang":"en"}`,
		SourceMode:   "http",
	}, 1*time.Hour)
	if err != nil {
		t.Fatalf("PutPage failed: %v", err)
	}

	page, ok, err := store.GetPage(ctx, "https://go.dev/doc/effective_go")
	if err != nil || !ok || page.Title != "Effective Go" {
		t.Fatalf("GetPage unexpected: %+v ok=%v err=%v", page, ok, err)
	}

	recalled, err := store.SearchLocalPages(ctx, "goroutines", 5)
	if err != nil {
		t.Fatalf("SearchLocalPages failed: %v", err)
	}
	if len(recalled) != 1 || recalled[0].URL != "https://go.dev/doc/effective_go" {
		t.Fatalf("unexpected FTS5 recall results: %+v", recalled)
	}
}
