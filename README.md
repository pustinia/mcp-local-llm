# mcp-local-llm

An MCP server that exposes a locally running LLM as a tool to Claude and other MCP clients.

## How it works

`mcp-local-llm` acts as a bridge between any OpenAI-compatible local LLM server (e.g. [MLX-LM](https://github.com/ml-explore/mlx-lm)) and MCP clients such as Claude Desktop. It registers `call_local_llm` and `get_llm_usage` tools and communicates over stdio transport, so no network port is required on the MCP side.

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
| `LOCAL_LLM_MAX_TOKENS` | — | Cap on output tokens per request. If unset, `max_tokens` is omitted and the server decides. |
| `LOCAL_LLM_TIMEOUT_SECONDS` | `300` | HTTP request timeout in seconds |
| `LOCAL_LLM_MAX_CONCURRENT` | `0` (unlimited) | Max concurrent requests to the LLM server (recommended: `2`) |
| `LOCAL_LLM_FILTER_THINKING` | `true` | Strip `<\|channel>thought...<channel\|>` thinking blocks from responses. Can be overridden per-call via the `filter_thinking` parameter. |
| `LOCAL_LLM_MAX_IMAGES` | `5` | Maximum images per `call_local_llm` call. Set to `0` to disable image input. Negative values allow unlimited images. |

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
        "LOCAL_LLM_MAX_CONCURRENT": "2"
      }
    }
  }
}
```

Restart Claude Desktop after editing the config. The `call_local_llm` and `get_llm_usage` tools will appear in the tool list.

## Tool reference

### `call_local_llm`

Calls the local LLM and returns its response as plain text. Each response includes a usage summary line: `[usage: prompt=N, completion=M, total=L]`.

| Parameter | Required | Description |
|---|---|---|
| `prompt` | yes | User message to send to the model |
| `system` | no | System prompt (prepended before the user message) |
| `model` | no | Override the default model for this call |
| `max_tokens` | no | Override the default token limit for this call |
| `filter_thinking` | no | Override server-level thinking block filtering for this call (`true` = filter, `false` = keep). Omit to use the server default (`LOCAL_LLM_FILTER_THINKING`). |
| `images` | no | List of images to send with the prompt. Each entry can be a file path, an `https://` URL, or a `data:image/...;base64,...` URI. Mixed formats allowed. Maximum per call is controlled by `LOCAL_LLM_MAX_IMAGES`. |

**Recommended use cases**

`call_local_llm` is designed to be used aggressively as the default for tasks that don't require live codebase context:

- Writing GitHub issue bodies and PR descriptions
- Drafting documentation paragraphs and README sections
- Translating text
- Summarizing files, logs, or diffs
- Standalone Q&A

Handle directly in Claude only when the task requires reasoning over fresh tool results, architecture decisions, or multi-step code analysis.

### `get_llm_usage`

Returns cumulative token usage stats across all `call_local_llm` invocations. Stats persist across server restarts via `usage.json` stored in the same directory as the binary.

```
total_calls=5, prompt_tokens=1234, completion_tokens=567, total_tokens=1801
```

No parameters.

## Usage tracking hook

`scripts/mcp-local-llm-usage.sh` is a [Claude Code Stop hook](https://docs.anthropic.com/en/docs/claude-code/hooks) that prints per-session local LLM usage deltas each time Claude finishes responding:

```
[local-llm] this turn: +3 calls, +5906 tokens (cumulative: 12 calls, 18234 tokens)
```

**Setup:**

1. Copy the hook script and make it executable:
   ```bash
   cp scripts/mcp-local-llm-usage.sh ~/.claude/hooks/
   chmod +x ~/.claude/hooks/mcp-local-llm-usage.sh
   ```

2. Register it as a Stop hook in `.claude/settings.local.json`:
   ```json
   {
     "hooks": {
       "Stop": [
         {
           "matcher": "",
           "hooks": [
             { "type": "command", "command": "~/.claude/hooks/mcp-local-llm-usage.sh" }
           ]
         }
       ]
     }
   }
   ```

3. If your binary is not in `~/.local/bin`, set `MCP_LOCAL_LLM_BINARY_DIR` to the directory containing the binary (and `usage.json`) in the hook environment or at the top of the script.

The script reads `usage.json` from the binary directory and compares it against a snapshot in `~/.claude/state/` to report only the delta for the current session.

## Benchmarking

Scripts in `bench/` help measure your local LLM setup's capacity and inform the `LOCAL_LLM_MAX_CONCURRENT` setting.

### Resource monitoring

```bash
bench/monitor.sh <PID> <output.csv>
```

Samples CPU, memory (RSS), and process metrics at 1-second intervals. Run in a separate terminal alongside a benchmark.

### Concurrency & throughput test

```bash
python3 bench/parallel_bench.py [concurrency] [max_tokens]
```

Sends requests at the given concurrency level and reports latency, TPS, and success rate. If `concurrency` is omitted, runs a full sweep at 1 → 2 → 4 → 8 automatically. GPU utilization is sampled via `ioreg` (macOS only).

Benchmark results on Gemma 4 26B (4-bit, Apple Silicon) show GPU saturation at ~2 concurrent requests with ~66 TPS peak throughput — the basis for the recommended `LOCAL_LLM_MAX_CONCURRENT=2`.

## License

MIT
