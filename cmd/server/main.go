package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/local-web-eyes/local-web-eyes/internal/browser"
	"github.com/local-web-eyes/local-web-eyes/internal/cache"
	"github.com/local-web-eyes/local-web-eyes/internal/config"
	"github.com/local-web-eyes/local-web-eyes/internal/fetch"
	"github.com/local-web-eyes/local-web-eyes/internal/mcpserver"
	"github.com/local-web-eyes/local-web-eyes/internal/search"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	var (
		configPath = flag.String("config", "", "Path to YAML configuration file")
		transport  = flag.String("transport", "", "MCP transport mode: stdio, http, or dual")
		httpAddr   = flag.String("addr", "", "HTTP listen address (default 127.0.0.1:8090)")
		httpPath   = flag.String("path", "", "Streamable HTTP MCP endpoint path (default /mcp)")
	)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("failed to load config", "err", err)
		os.Exit(1)
	}
	if *transport != "" {
		cfg.Server.Transport = strings.ToLower(strings.TrimSpace(*transport))
	}
	if *httpAddr != "" {
		cfg.Server.HTTPAddr = *httpAddr
	}
	if *httpPath != "" {
		cfg.Server.HTTPPath = *httpPath
	}

	logger := newStderrLogger(cfg.Server.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := cache.Open(cfg.Cache.DBPath)
	if err != nil {
		logger.Error("failed to open sqlite cache", "path", cfg.Cache.DBPath, "err", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	bl := security.NewBlocklist(cfg.Security.CustomAllowlist, cfg.Security.CustomDenylist)
	guard := security.NewGuard(security.GuardOptions{
		AllowPrivateIPs: cfg.Security.AllowPrivateIPs,
		Blocklist:       bl,
		MaxRedirects:    cfg.Fetch.MaxRedirects,
	})

	if cfg.Security.BlocklistEnabled && len(cfg.Security.BlocklistFeeds) > 0 {
		go runBlocklistSyncLoop(ctx, logger, bl, guard, store, cfg.Security.BlocklistFeeds, cfg.Security.BlocklistRefreshInterval)
	} else if store != nil {
		_ = store.PurgeExpired(ctx)
	}

	pool := browser.NewPool(cfg.Browser, guard)
	defer func() { _ = pool.Close() }()

	fetcher := fetch.NewFetcher(cfg, guard, store, pool)
	crawler := fetch.NewCrawler(fetcher, cfg)
	providers := search.BuildDefaultProviders(cfg, guard, pool)
	router := search.NewRouter(cfg, guard, store, providers...)

	deps := mcpserver.Deps{
		Config:    cfg,
		Router:    router,
		Fetcher:   fetcher,
		Crawler:   crawler,
		Blocklist: bl,
		Browser:   pool,
	}
	srv := mcpserver.NewServer(deps)

	switch cfg.Server.Transport {
	case "http":
		if err := runHTTPServer(ctx, logger, srv, deps); err != nil {
			logger.Error("HTTP MCP server exited with error", "err", err)
			os.Exit(1)
		}
	case "dual":
		errCh := make(chan error, 1)
		go func() {
			errCh <- runHTTPServer(ctx, logger, srv, deps)
		}()
		logger.Info("starting MCP server in dual mode (stdio + Streamable HTTP)", "addr", cfg.Server.HTTPAddr, "path", cfg.Server.HTTPPath)
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("stdio MCP server error", "err", err)
			os.Exit(1)
		}
		stop()
		if httpErr := <-errCh; httpErr != nil {
			logger.Error("HTTP MCP server error", "err", httpErr)
			os.Exit(1)
		}
	default:
		logger.Info("starting MCP server on stdio", "browser_available", pool.Available())
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("stdio MCP server error", "err", err)
			os.Exit(1)
		}
	}
}

func runHTTPServer(ctx context.Context, logger *slog.Logger, srv *mcp.Server, deps mcpserver.Deps) error {
	mux := mcpserver.NewHTTPMux(srv, deps)
	httpSrv := &http.Server{
		Addr:              deps.Config.Server.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening for MCP Streamable HTTP requests", "addr", deps.Config.Server.HTTPAddr, "path", deps.Config.Server.HTTPPath)
	err := httpSrv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runBlocklistSyncLoop(ctx context.Context, logger *slog.Logger, bl *security.Blocklist, guard *security.Guard, store *cache.Store, feeds []string, interval time.Duration) {
	if interval <= 0 {
		interval = 12 * time.Hour
	}

	skipInitialSync := false
	if store != nil {
		_ = store.PurgeExpired(ctx)
		if snap, ok, err := store.GetMeta(ctx, "blocklist_snapshot_v1"); err == nil && ok && snap != "" {
			domains, urls := bl.ImportSnapshot(snap)
			logger.Info("loaded cached blocklist snapshot", "domains", domains, "urls", urls)
			if domains > 0 || urls > 0 {
				if rawTS, tsOK, tsErr := store.GetMeta(ctx, "blocklist_synced_at"); tsErr == nil && tsOK {
					if unixSec, parseErr := strconv.ParseInt(rawTS, 10, 64); parseErr == nil {
						if time.Since(time.Unix(unixSec, 0)) < interval {
							skipInitialSync = true
						}
					}
				}
			}
		}
	}

	syncClient := guard.NewHTTPClient(25 * time.Second)
	syncOnce := func() {
		syncCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		domains, urls, err := bl.SyncFromFeeds(syncCtx, syncClient, feeds)
		if (domains > 0 || urls > 0) && store != nil {
			_ = store.PutMeta(ctx, "blocklist_snapshot_v1", bl.ExportSnapshot())
			_ = store.PutMeta(ctx, "blocklist_synced_at", strconv.FormatInt(time.Now().Unix(), 10))
		}
		if err != nil {
			logger.Warn("blocklist feed sync completed with warnings", "domains", domains, "urls", urls, "err", err)
			return
		}
		logger.Info("blocklist feeds synced", "domains", domains, "urls", urls)
	}

	if !skipInitialSync {
		syncOnce()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if store != nil {
				_ = store.PurgeExpired(ctx)
			}
			syncOnce()
		}
	}
}

func newStderrLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
