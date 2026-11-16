# Architecture Design

## Overview

`local-web-eyes` is structured as decoupled internal packages wired at startup in `cmd/server/main.go`. MCP transport handlers depend only on domain service interfaces (`search.Router`, `fetch.Fetcher`, `fetch.Crawler`), keeping providers and storage replaceable.

```text
+-------------------------------------------------------------------+
|                        MCP Clients / LLMs                         |
|      (Ollama / OpenCode / Cline / Claude Desktop / Antigravity)   |
+------------------+--------------------------------+---------------+
                   | Stdio (JSON-RPC)               | Streamable HTTP (/mcp)
                   v                                v
+-------------------------------------------------------------------+
|                   internal/mcpserver (go-sdk)                     |
|   Tools: web_search | web_search_multi | web_fetch | web_crawl    |
+-------+-------------------------+------------------------+--------+
        |                         |                        |
        v                         v                        v
+---------------+       +------------------+     +------------------+
|internal/search|       |  internal/fetch  |     |  internal/fetch  |
| Tiered Router |       |  HTTP Fetcher +  |     |  Bounded Crawler |
| + RRF + CB    |       |  Trafilatura/MD  |     |  (Same-Origin)   |
+-------+-------+       +--------+---------+     +------------------+
        |                        |
        |   +--------------------+--------------------+
        |   |                                         |
        v   v                                         v
+-------------------+                       +-------------------+
|  internal/browser |                       |  internal/cache   |
|  go-rod CDP Pool  |                       |  SQLite + FTS5    |
|  (Edge / Chrome)  |                       |  (modernc/sqlite) |
+---------+---------+                       +-------------------+
          |
          v
+-------------------------------------------------------------------+
|                        internal/security                          |
|  SSRF Dialer.Control | Threat Blocklist | HTML/Unicode Sanitizer  |
+-------------------------------------------------------------------+
```

## Module Responsibilities
- `internal/config`: Typed configuration with YAML loading and environment overrides; enforces context-budget defaults tuned for 8–16 GB local LLMs (`qwen3.5:9b`).
- `internal/security`:
  - `ssrf.go`: Enforces `http`/`https` only, rejects embedded credentials, validates DNS results, and verifies the target IP inside `net.Dialer.Control` before TCP connect.
  - `blocklist.go`: Syncs and evaluates AdBlock (`||domain^`), hosts, and URL threat feeds in memory with custom allowlist/denylist overrides.
  - `sanitizer.go`: Strips `<script>`, `<style>`, `<iframe>`, comments, hidden elements, and zero-width/bidi control runes.
- `internal/cache`: Pure-Go SQLite (`WAL` mode) storing `search_cache`, `page_cache`, and an FTS5 virtual table (`page_fts`) for local recall.
- `internal/browser`: Lazy CDP browser pool managing an isolated incognito context per navigation, denying file downloads, dropping media/font requests, and hijacking private/non-HTTP(S) subrequests.
- `internal/fetch`: Safe streaming HTTP fetcher (MIME allowlist, extension blocklist, magic-byte binary rejection, decompression size cap), `go-trafilatura/v2` + `html-to-markdown/v2` extractor, heading outline builder, UTF-8 safe paginator, and same-origin BFS crawler.
- `internal/search`: Pluggable `Provider` pipeline across Tier 1 (APIs / optional SearXNG), Tier 2 (HTTP scrapers with Circuit Breaker), and Tier 3 (browser fallback), merged with Reciprocal Rank Fusion.
- `internal/mcpserver`: Registers typed MCP tools using `github.com/modelcontextprotocol/go-sdk/mcp` and exposes `/mcp` and `/healthz`.
