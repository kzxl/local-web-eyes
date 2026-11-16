# Benchmark & Verification Report

## 1. Environment
- **OS**: Windows (`windows/amd64`)
- **Compiler**: `go version go1.26.1 windows/amd64`
- **Binary Artifact**: `bin/local-web-eyes.exe` (`36,218,368` bytes, ~34.5 MiB single static-linked Go binary)

## 2. Unit & Integration Test Results
Executed `go test -count=1 -v ./...` (24/24 tests passed):
- `internal/cache`: 2 tests (`TestSQLiteCacheAndFTS5Recall`, `TestPurgeExpiredCleansFTSAndMetaKV`) — **PASS** (`1.235s` package total)
- `internal/fetch`: 9 tests (`TestCrawlerRespectsSameOriginDepthPagesAndBudget`, `TestExtractMarkdownAndHeadings`, `TestPaginateMarkdown`, `TestDetectSPAShell`, `TestExtractSectionFilter`, `TestFetchRejectsExecutableDisguisedAsHTML`, `TestFetchRejectsBinaryMimeAndExtension`, `TestFetchGuardsAgainstGzipDecompressionBomb`, `TestFetchRedirectCachesOriginalAndFinalURL`) — **PASS** (`2.751s` package total)
- `internal/mcpserver`: 1 test (`TestStreamableHTTPMCPToolsEndToEnd`) — **PASS** (`3.789s` package total)
- `internal/search`: 5 tests (`TestRouterRRFDedupeAndBlocklistFilter`, `TestRouterFallsBackToTier3AndTripsBreaker`, `TestCircuitBreakerTransitions`, `TestRouterEscalatesToTier3WhenOnlyWikipediaSucceeds`, `TestDetectWikipediaLang`) — **PASS** (`1.761s` package total)
- `internal/security`: 7 tests (`TestBlocklistMatchingAndParsers`, `TestBlocklistAllowlistOverride`, `TestBlocklistSnapshotRoundTrip`, `TestSanitizeHTMLStripsHiddenAndInjectionVectors`, `TestSanitizeNodeInPlace`, `TestIsRestrictedIP`, `TestValidateTargetURL`) — **PASS** (`1.231s` package total)

## 3. Micro-Benchmark (`go run ./cmd/bench`)

| Metric | Observed Result | Scope / Notes |
| :--- | :--- | :--- |
| **Noise Reduction** | `7,217` bytes raw HTML → `1,505` bytes Markdown (**79.1% stripped**) | Synthetic noisy HTML (nav ads, hidden prompt-injection div, script, cookie footer) |
| **Cold HTTP + Extract Avg** | `8.292 ms` (`9.084 ms` prior) | 50 uncached loopback iterations (single-pass `SanitizeNode` + `go-trafilatura/v2` + `html-to-markdown/v2`) |
| **Warm Cache + MCP HTTP Avg** | `819.778 µs` (`854.454 µs` prior) | 50 cached `web_fetch` calls over `StreamableClientTransport` |
| **Blocklist Lookup Avg** | `499 ns` (`451 ns` prior) | 10,000 subdomain matches |
| **Memory After 50 Cold Runs** | `289.52 MB` HeapAlloc / `308.21 MB` Sys (`295.62 MB` / `311.96 MB` prior) | Includes pre-GC Go heap + `wazero` regex runtime inside `go-trafilatura` across 50 tight uncollected iterations |
| **Live Internet Search Latency** | `896.42 ms` (`Hits: 5`, `Providers: [duckduckgo wikipedia]`, `Err: <nil>`) | Prior run with `--live-query "Model Context Protocol"` (not re-executed in offline pass) |

## 4. Not Executed on Host (External Daemons Stopped)
- **Docker Compose / SearXNG container run**: Docker Desktop Linux engine pipe was not running on host (`dockerDesktopLinuxEngine` unavailable).
- **Live Ollama `qwen3.5:9b` tool-calling run**: Ollama server failed to auto-start in non-interactive shell (`ollama 0.20.5`). Re-run via `go run ./cmd/bench --ollama-url http://127.0.0.1:11434 --model qwen3.5:9b` once Ollama is running.
