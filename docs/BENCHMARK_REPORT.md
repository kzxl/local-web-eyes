# Benchmark & Verification Report

## 1. Environment
- **OS**: Windows (`windows/amd64`)
- **Compiler**: `go version go1.26.1 windows/amd64`
- **Binary Artifact**: `bin/local-web-eyes.exe` (`36,199,936` bytes, ~34.5 MiB single static-linked Go binary)

## 2. Unit & Integration Test Results
Executed `go test -count=1 -v ./...` (all assertions passed):
- `internal/cache`: `TestSQLiteCacheAndFTS5Recall` — **PASS** (`0.10s`)
- `internal/fetch`: 7 tests (`TestCrawlerRespectsSameOriginDepthPagesAndBudget`, `TestExtractMarkdownAndHeadings`, `TestPaginateMarkdown`, `TestDetectSPAShell`, `TestFetchRejectsExecutableDisguisedAsHTML`, `TestFetchRejectsBinaryMimeAndExtension`, `TestFetchGuardsAgainstGzipDecompressionBomb`) — **PASS** (`2.436s` package total)
- `internal/mcpserver`: `TestStreamableHTTPMCPToolsEndToEnd` (connects official `mcp.NewClient` over `StreamableClientTransport` to `/mcp`, checks `/healthz`, lists 4 tools, invokes `web_search`, `web_fetch`, `web_search_multi`, `web_crawl`) — **PASS** (`0.33s`)
- `internal/search`: 3 tests (`TestRouterRRFDedupeAndBlocklistFilter`, `TestRouterFallsBackToTier3AndTripsBreaker`, `TestCircuitBreakerTransitions`) — **PASS** (`1.000s` package total)
- `internal/security`: 5 tests (`TestBlocklistMatchingAndParsers`, `TestBlocklistAllowlistOverride`, `TestSanitizeHTMLStripsHiddenAndInjectionVectors`, `TestIsRestrictedIP`, `TestValidateTargetURL`) — **PASS** (`0.606s` package total)

## 3. Micro-Benchmark & Live Search (`go run ./cmd/bench --live-query "Model Context Protocol"`)

| Metric | Observed Result | Scope / Notes |
| :--- | :--- | :--- |
| **Noise Reduction** | `7,217` bytes raw HTML → `1,505` bytes Markdown (**79.1% stripped**) | Synthetic noisy HTML (nav ads, hidden prompt-injection div, script, cookie footer) |
| **Cold HTTP + Extract Avg** | `9.084 ms` (`9.204 ms` on prior run) | 50 uncached loopback iterations (`go-trafilatura/v2` + `html-to-markdown/v2`) |
| **Warm Cache + MCP HTTP Avg** | `854.454 µs` (`844.544 µs` on prior run) | 50 cached `web_fetch` calls over `StreamableClientTransport` |
| **Blocklist Lookup Avg** | `451 ns` (`492 ns` on prior run) | 10,000 subdomain matches |
| **Memory After 50 Cold Runs** | `295.62 MB` HeapAlloc / `311.96 MB` Sys (`189.97 MB` / `263.71 MB` before live search) | Includes pre-GC Go heap + `wazero` regex runtime inside `go-trafilatura` across 50 tight uncollected iterations |
| **Live Internet Search Latency** | `896.42 ms` (`Hits: 5`, `Providers: [duckduckgo wikipedia]`, `Err: <nil>`) | Real query `"Model Context Protocol"` against live upstream providers |

## 4. Not Executed on Host (External Daemons Stopped)
- **Docker Compose / SearXNG container run**: Docker Desktop Linux engine pipe was not running on host (`dockerDesktopLinuxEngine` unavailable).
- **Live Ollama `qwen3.5:9b` tool-calling run**: Ollama server failed to auto-start in non-interactive shell (`ollama 0.20.5`). Re-run via `go run ./cmd/bench --ollama-url http://127.0.0.1:11434 --model qwen3.5:9b` once Ollama is running.
