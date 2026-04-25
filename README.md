# mcp-local-llm

An MCP server that exposes a locally running LLM as a tool to Claude and other MCP clients.

## How it works

`mcp-local-llm` acts as a bridge between any OpenAI-compatible local LLM server (e.g. [MLX-LM](https://github.com/ml-explore/mlx-lm)) and MCP clients such as Claude Desktop. It registers a single `call_local_llm` tool and communicates over stdio transport, so no network port is required on the MCP side.

```
Claude Desktop  ──stdio──►  mcp-local-llm  ──HTTP──►  Local LLM server (port 8000)
```

## Prerequisites

- Go 1.22 or later
- A locally running LLM server that speaks the OpenAI chat completions API (e.g. `mlx_lm.server --model <model>`)

## Build

```bash
make build
./bin/mcp-local-llm -version
```

The binary is written to `bin/mcp-local-llm`. Version and build time are injected at compile time from `git describe --tags`.

## Configuration

All settings are controlled via environment variables. The binary has no config file.

| Variable | Default | Description |
|---|---|---|
| `LOCAL_LLM_BASE_URL` | `http://localhost:8000` | Base URL of the local LLM server |
| `LOCAL_LLM_MODEL` | `mlx-community/gemma-4-26b-a4b-it-4bit` | Model name passed to the server |
| `LOCAL_LLM_MAX_TOKENS` | `32768` | Default maximum output tokens |
| `LOCAL_LLM_TIMEOUT_SECONDS` | `300` | HTTP request timeout in seconds |
| `LOCAL_LLM_MAX_CONCURRENT` | `0` (unlimited) | Max concurrent requests to the LLM server (recommended: `2`) |

Copy `.env.example` to `.env` for reference (the binary itself reads from the process environment, not a file).

## Usage with Claude Desktop

Add the following to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "local-llm": {
      "command": "/absolute/path/to/bin/mcp-local-llm",
      "env": {
        "LOCAL_LLM_BASE_URL": "http://localhost:8000",
        "LOCAL_LLM_MODEL": "mlx-community/gemma-4-26b-a4b-it-4bit",
        "LOCAL_LLM_MAX_TOKENS": "32768",
        "LOCAL_LLM_MAX_CONCURRENT": "2"
      }
    }
  }
}
```

Restart Claude Desktop after editing the config. The `call_local_llm` tool will appear in the tool list.

## Tool reference

### `call_local_llm`

Calls the local LLM and returns its response as plain text.

| Parameter | Required | Description |
|---|---|---|
| `prompt` | yes | User message to send to the model |
| `system` | no | System prompt (prepended before the user message) |
| `model` | no | Override the default model for this call |
| `max_tokens` | no | Override the default token limit for this call |

## License

MIT
