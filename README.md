# Local Web Eyes

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![MCP Server](https://img.shields.io/badge/MCP-Stdio%20%7C%20Streamable%20HTTP-2EA44F)](docs/INTEGRATION.md)
[![Zero API Keys](https://img.shields.io/badge/API%20Keys-Zero%20Required-blue)](docs/CONFIGURATION.md)
[![Cache](https://img.shields.io/badge/Cache-SQLite%20FTS5%20(Pure%20Go)-003B57?logo=sqlite&logoColor=white)](internal/cache/sqlite.go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![GitHub stars](https://img.shields.io/github/stars/kzxl/local-web-eyes?style=social)](https://github.com/kzxl/local-web-eyes/stargazers)

> **Give local LLMs (Ollama, Qwen, LM Studio) live web search and clean Markdown reading — zero API keys, zero SaaS billing, single Go binary.**

`local-web-eyes` is a lightweight **Model Context Protocol (MCP)** server written in pure Go (`1.26`, zero CGO) that equips local and IDE coding agents with safe web search, clean boilerplate-stripped page extraction, persistent SQLite FTS5 caching, and defense-in-depth SSRF/prompt-injection protection.

---

## Why Local Web Eyes?

| Capability | Local Web Eyes | Paid Search APIs (Tavily / Brave / Serper) |
| :--- | :---: | :---: |
| **API Keys / Monthly Cost** | **$0 — No API keys required** | API key + per-request quota |
| **Deployment** | Single static Go binary or Docker | External SaaS dependency |
| **Bot-Throttle Resilience** | 3-tier router + local headless CDP fallback | Single upstream vendor |
| **Small-Context Optimization** | `go-trafilatura` + heading outline + paginated chunks | Raw HTML or fixed snippets |
| **Local Offline Recall** | Pure-Go SQLite TTL cache + FTS5 search | None |
| **Security Guardrails** | Socket-level SSRF guard + threat blocklist + prompt-injection stripping | Varies by vendor |

---

## 30-Second Quick Start

### Option 1: Build Single Binary (Go 1.26+)

```bash
git clone https://github.com/kzxl/local-web-eyes.git
cd local-web-eyes

# Build single binary (zero CGO)
go build -trimpath -ldflags="-s -w" -o bin/local-web-eyes ./cmd/server

# Run in Stdio mode (Claude Desktop, Cursor, Cline, Antigravity)
./bin/local-web-eyes --transport stdio

# Or run in Streamable HTTP mode (/mcp + /healthz)
./bin/local-web-eyes --transport http --addr 127.0.0.1:8090

# Or run both simultaneously
./bin/local-web-eyes --transport dual --addr 127.0.0.1:8090
```

### Option 2: Docker Compose

```bash
# Standalone (no SearXNG needed — listens on 127.0.0.1:35434)
docker compose up -d

# Optional: include local SearXNG as a Tier 1 search backend
SEARXNG_URL=http://searxng:8080 docker compose --profile searxng up -d
```

### MCP Client Configuration

Add either transport to your MCP host config (see [`deployments/mcp-clients.json`](deployments/mcp-clients.json)):

```json
{
  "mcpServers": {
    "local-web-eyes": {
      "command": "/path/to/local-web-eyes",
      "args": ["--transport", "stdio"],
      "env": {
        "LWE_DEFAULT_MAX_CHARS": "6000"
      }
    }
  }
}
```

Or via Streamable HTTP:

```json
{
  "mcpServers": {
    "local-web-eyes": {
      "type": "streamable-http",
      "url": "http://127.0.0.1:8090/mcp"
    }
  }
}
```

---

## MCP Tools

| Tool | Purpose |
| :--- | :--- |
| `web_search` | Tiered web search with Reciprocal Rank Fusion (RRF) ranking and URL deduplication. |
| `web_search_multi` | Parallel multi-query research merged into a single deduplicated result set. |
| `web_fetch` | Fetches a URL, strips ads/nav/hidden injection vectors, returns clean Markdown with heading outline and pagination (`max_chars`, `offset`, `next_offset`). |
| `web_crawl` | Same-origin bounded crawl respecting depth, page count, and character budgets. |

---

## Architecture & Key Features

- **Tiered Search Router (No Paid API Keys)**:
  - **Tier 1**: Wikimedia API + optional SearXNG JSON adapter.
  - **Tier 2**: Lightweight HTTP scrapers (DuckDuckGo HTML, Mojeek) guarded by a Circuit Breaker.
  - **Tier 3**: Automatic headless browser fallback via `go-rod` (CDP) using local Chrome/Edge when HTTP scrapers hit bot challenges.
- **Clean Extraction for Small-Context Local LLMs**:
  - `go-trafilatura/v2` + `html-to-markdown/v2` boilerplate removal.
  - Heading outline extraction and UTF-8/paragraph-aware paginated chunking (`max_chars` default `6000`).
- **Defense-in-Depth Security**:
  - Socket-level SSRF & DNS rebinding protection (`net.Dialer.Control`) + redirect hop guard.
  - Keyless threat-intelligence blocklist sync (HaGeZi TIF, OpenPhish, Phishing.Database; optional URLhaus key).
  - Strict `Content-Type` allowlist, executable extension blocklist, magic-byte inspection (`MZ`, `ELF`, `PK`), and bounded stream reading.
  - Isolated incognito CDP browser profile with downloads disabled and media/font/private-IP blocking.
  - Hidden DOM, script/comment, and invisible Unicode stripping to mitigate indirect prompt injection.
- **Pure-Go SQLite Cache + FTS5**: Persistent TTL caching and full-text local recall via `modernc.org/sqlite`.

---

## Verified Benchmarks

Measured on `go1.26.1 windows/amd64` with `go test -count=1 ./...` (**24/24 passing**) and `go run ./cmd/bench` (full report in [`docs/BENCHMARK_REPORT.md`](docs/BENCHMARK_REPORT.md)):

| Metric | Measured Result | Notes |
| :--- | :---: | :--- |
| **HTML Noise Reduction** | **79.1% stripped** | `7,217 B` noisy HTML → `1,505 B` clean Markdown |
| **Cold HTTP + Extract Avg** | **8.29 ms** | 50 uncached iterations (sanitize + trafilatura + markdown) |
| **Warm Cache + MCP HTTP Avg** | **820 µs** | 50 cached `web_fetch` calls over Streamable HTTP |
| **Threat Blocklist Lookup** | **499 ns** | Average over 10,000 subdomain lookups |
| **Live Internet Search** | **896 ms** | End-to-end DuckDuckGo + Wikipedia query |

---

## Documentation

- [Architecture Overview](docs/ARCHITECTURE.md)
- [Configuration Reference](docs/CONFIGURATION.md)
- [Integration & Ollama Guide](docs/INTEGRATION.md)
- [Benchmark & Verification Report](docs/BENCHMARK_REPORT.md)

---

## License

Licensed under the [MIT License](LICENSE).

---

If `local-web-eyes` helps your local LLM setup, consider giving it a ⭐ on GitHub!
