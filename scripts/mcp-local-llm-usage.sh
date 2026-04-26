#!/usr/bin/env bash
set -euo pipefail

# Directory containing the mcp-local-llm binary (and usage.json).
# Override with MCP_LOCAL_LLM_BINARY_DIR if the binary is not in the default location.
BINARY_DIR="${MCP_LOCAL_LLM_BINARY_DIR:-$HOME/.local/bin}"
USAGE="$BINARY_DIR/usage.json"
SNAPSHOT_DIR="${HOME}/.claude/state"
SNAPSHOT="$SNAPSHOT_DIR/mcp-local-llm-snapshot.json"

[ ! -f "$USAGE" ] && exit 0
mkdir -p "$SNAPSHOT_DIR"

cur_calls=$(jq '.total_calls // 0' "$USAGE")
cur_tokens=$(jq '.total_tokens // 0' "$USAGE")

if [ -f "$SNAPSHOT" ]; then
  prev_calls=$(jq '.total_calls // 0' "$SNAPSHOT")
  prev_tokens=$(jq '.total_tokens // 0' "$SNAPSHOT")
else
  prev_calls=0
  prev_tokens=0
fi

delta_calls=$((cur_calls - prev_calls))
delta_tokens=$((cur_tokens - prev_tokens))

if [ "$delta_calls" -gt 0 ]; then
  echo "[local-llm] this turn: +${delta_calls} calls, +${delta_tokens} tokens (cumulative: ${cur_calls} calls, ${cur_tokens} tokens)"
fi

cp "$USAGE" "$SNAPSHOT"
