# Configuration Reference

Configuration loads defaults first, overlays `--config <path>` YAML if provided, and then applies environment variables.

## Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `LWE_TRANSPORT` | `stdio` | Transport mode: `stdio`, `http`, or `dual` |
| `LWE_HTTP_ADDR` | `127.0.0.1:8090` | Listen host and port for `http` / `dual` modes |
| `LWE_HTTP_PATH` | `/mcp` | Streamable HTTP endpoint path |
| `LWE_DB_PATH` | `data/web_eyes.db` | SQLite cache and FTS5 database path |
| `LWE_DEFAULT_MAX_CHARS` | `6000` | Default `web_fetch` character budget per call |
| `LWE_BROWSER_ENABLED` | `true` | Enable lazy headless Edge/Chrome fallback |
| `LWE_BROWSER_BIN` | `""` | Explicit browser executable path (auto-detected if empty) |
| `LWE_BLOCKLIST_ENABLED` | `true` | Enable threat-intelligence feed sync and filtering |
| `SEARXNG_URL` | `""` | Optional SearXNG instance base URL (e.g. `http://127.0.0.1:8888`) |
| `URLHAUS_AUTH_KEY` | `""` | Optional free abuse.ch Auth-Key to append URLhaus feed |

## Recommended Profiles for `qwen3.5:9b`

| Host GPU VRAM | Recommended Ollama `num_ctx` | Recommended `LWE_DEFAULT_MAX_CHARS` | Notes |
| :--- | :--- | :--- | :--- |
| **8 GB** | `16384` – `24576` | `5000` – `6000` | Keeps weights + KV cache on GPU; paginate long docs with `offset` |
| **16 GB+** | `32768` – `65536` | `8000` – `12000` | Comfortable headroom for Thinking Mode + multi-source synthesis |
