# Integration & Operations Guide

## 1. Standard MCP Endpoints ("Ports")

| Mode | Command | Endpoint / Channel | Use Case |
| :--- | :--- | :--- | :--- |
| **Stdio** | `local-web-eyes.exe --transport stdio` | `stdin` / `stdout` (logs on `stderr`) | Local desktop/IDE agents (Claude Desktop, Cline, OpenCode, Antigravity) |
| **Streamable HTTP** | `local-web-eyes.exe --transport http --addr 127.0.0.1:8090` | `http://127.0.0.1:8090/mcp` & `/healthz` | Networked services, Docker, shared local LLM bridges |
| **Dual** | `local-web-eyes.exe --transport dual --addr 127.0.0.1:8090` | Both `stdio` and `:8090/mcp` | Hosts needing direct stdio plus HTTP health/secondary clients |

## 2. Client Configuration Snippet

See `deployments/mcp-clients.json` for ready-to-use `stdio` and `streamable-http` blocks.

## 3. Running with Docker Compose

```powershell
# Standalone mode (port 35434 -> container 8090, no SearXNG needed)
docker compose up -d

# Optional: enable SearXNG container as Tier 1 search backend
$env:SEARXNG_URL = "http://searxng:8080"
docker compose --profile searxng up -d
```

Verify health:

```powershell
Invoke-RestMethod http://127.0.0.1:35434/healthz
```

## 4. Integrating with Ollama (`qwen3.5:9b`)

1. Start Ollama with an explicit context window suitable for tool-calling + Thinking Mode:
   ```powershell
   $env:OLLAMA_CONTEXT_LENGTH = "32768"
   ollama serve
   ollama pull qwen3.5:9b
   ```
2. Connect an MCP-capable client (e.g., Cline, OpenCode, or a custom bridge calling `/api/chat` with MCP tool schemas) to either `stdio` or `http://127.0.0.1:8090/mcp`.
3. Run the bundled integration & benchmark check:
   ```powershell
   go run ./cmd/bench --ollama-url http://127.0.0.1:11434 --model qwen3.5:9b
   ```

## 5. Adding a Custom Search Provider

Implement `search.Provider` in `internal/search`:
- `Name() string`
- `Tier() int` (`1` = structured API, `2` = HTTP scraper, `3` = browser fallback)
- `Search(ctx context.Context, query string, limit int, timeRange string) ([]Result, error)`

Register the instance in `search.BuildDefaultProviders(...)` without modifying `internal/mcpserver`.
