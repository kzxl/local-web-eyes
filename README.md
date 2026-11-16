# Local Web Eyes

[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![MCP Server](https://img.shields.io/badge/MCP-Stdio%20%7C%20Streamable%20HTTP-2EA44F)](docs/INTEGRATION.md)
[![Zero Cost](https://img.shields.io/badge/API%20Keys-Zero%20Required-blue)](docs/CONFIGURATION.md)
[![Security](https://img.shields.io/badge/Security-SSRF%20%26%20Prompt--Injection%20Guard-critical)](docs/ARCHITECTURE.md)
[![Storage](https://img.shields.io/badge/Cache-SQLite%20FTS5%20(Pure%20Go)-003B57?logo=sqlite&logoColor=white)](internal/cache/sqlite.go)

Lightweight, zero-cost, single-binary **Local Web Access MCP Service** written in Go (`1.26`) to give local LLMs (e.g., `qwen3.5:9b` via Ollama) safe, structured internet search and webpage reading capabilities without paid APIs or mandatory SaaS dependencies.

## Features
- **Standard MCP Transports**: Supports `stdio`, `http` (Streamable HTTP on `/mcp` + `/healthz`), and `dual` (both simultaneously).
- **4 MCP Tools**: `web_search`, `web_search_multi`, `web_fetch`, and `web_crawl`.
- **Tiered Search Router (No Paid API Keys)**:
  - Tier 1: Wikimedia API + optional SearXNG JSON adapter.
  - Tier 2: Lightweight HTTP scrapers (DuckDuckGo HTML, Mojeek) protected by a Circuit Breaker.
  - Tier 3: Automatic headless browser fallback via `go-rod` (CDP) using local Microsoft Edge/Chrome when HTTP scrapers face bot throttling.
  - Ranking & Deduplication: Reciprocal Rank Fusion (RRF) with URL canonicalization.
- **Clean Extraction for Small-Context Local LLMs**:
  - `go-trafilatura/v2` + `html-to-markdown/v2` boilerplate removal.
  - Heading outline extraction + UTF-8/paragraph-aware paginated chunking (`max_chars` default `6000`, `offset`, `next_offset`).
- **Defense-in-Depth Security**:
  - Socket-level SSRF & DNS rebinding protection (`net.Dialer.Control`) + redirect hop guard.
  - Keyless threat-intelligence blocklist sync (HaGeZi TIF, OpenPhish, Phishing.Database; optional URLhaus Auth-Key).
  - Strict `Content-Type` allowlist, executable extension blocklist, magic-byte inspection (`MZ`, `ELF`, `PK`), and bounded stream reading.
  - Isolated incognito CDP browser profile with downloads disabled and media/font/private-IP request hijacking.
  - Hidden DOM, script/comment, and invisible Unicode stripping for indirect prompt injection mitigation.
- **Pure-Go SQLite Cache + FTS5**: Persistent TTL caching and full-text local recall (`modernc.org/sqlite`, zero CGO).

## Quick Start

```powershell
# Build single binary
go build -trimpath -ldflags="-s -w" -o bin/local-web-eyes.exe ./cmd/server

# Run in Streamable HTTP mode (127.0.0.1:8090/mcp + /healthz)
.\bin\local-web-eyes.exe --transport http --addr 127.0.0.1:8090

# Run in Stdio mode (for local MCP hosts)
.\bin\local-web-eyes.exe --transport stdio

# Run in Dual mode (Stdio + HTTP simultaneously)
.\bin\local-web-eyes.exe --transport dual --addr 127.0.0.1:8090
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), [docs/CONFIGURATION.md](docs/CONFIGURATION.md), [docs/INTEGRATION.md](docs/INTEGRATION.md), and [docs/BENCHMARK_REPORT.md](docs/BENCHMARK_REPORT.md).

## Topics & Use Cases
- **Local LLM Web Access**: Connect `Ollama`, `LM Studio`, or custom AI agents to live web search and clean Markdown extraction without Tavily, Brave, or Serper API keys.
- **Topics**: `mcp`, `mcp-server`, `model-context-protocol`, `web-search`, `web-scraper`, `local-llm`, `ollama`, `golang`, `ai-agents`, `markdown-extractor`, `searxng`, `duckduckgo`, `headless-browser`, `ssrf-protection`, `sqlite-fts5`, `zero-cost`, `rag`, `llm-tools`.
