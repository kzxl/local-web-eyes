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

func TestPurgeExpiredCleansFTSAndMetaKV(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "purge.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	if err := store.PutPage(ctx, CachedPage{
		URL:          "https://expired.example/doc",
		Title:        "Expired Doc",
		Markdown:     "UniqueTokenForPurgeTest content inside expired page.",
		HeadingsJSON: `[]`,
		MetadataJSON: `{}`,
		SourceMode:   "http",
	}, -1*time.Second); err != nil {
		t.Fatalf("PutPage expired failed: %v", err)
	}

	if err := store.PurgeExpired(ctx); err != nil {
		t.Fatalf("PurgeExpired failed: %v", err)
	}

	recalled, err := store.SearchLocalPages(ctx, "UniqueTokenForPurgeTest", 5)
	if err != nil {
		t.Fatalf("SearchLocalPages failed: %v", err)
	}
	if len(recalled) != 0 {
		t.Fatalf("expected expired page to be removed from page_fts, got %+v", recalled)
	}

	if err := store.PutMeta(ctx, "blocklist_synced_at", "1700000000"); err != nil {
		t.Fatalf("PutMeta failed: %v", err)
	}
	val, ok, err := store.GetMeta(ctx, "blocklist_synced_at")
	if err != nil || !ok || val != "1700000000" {
		t.Fatalf("GetMeta unexpected: val=%q ok=%v err=%v", val, ok, err)
	}
	if _, missingOK, err := store.GetMeta(ctx, "missing_key"); err != nil || missingOK {
		t.Fatalf("expected missing key miss, got ok=%v err=%v", missingOK, err)
	}
}
