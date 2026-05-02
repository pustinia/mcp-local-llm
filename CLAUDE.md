# CLAUDE.md

Internal documentation for the AI coding assistant. README.md is for human readers, while this file is for understanding context before coding tasks.

## Local LLM Usage Standards

`call_local_llm` (Gemma 4 26B, 4-bit) is available — use it aggressively to save Anthropic token costs.

Break-even point: ~280 chars (~70 tokens) of expected output. Below this, tool call overhead makes direct generation cheaper.

### Delegate to `call_local_llm` when expected output exceeds ~280 chars:
- GitHub issue bodies and PR descriptions
- Multi-line commit message bodies (subject line is OK to write directly)
- Translations of substantial text
- Summarization of files, logs, diffs, or text content
- Drafts of documentation paragraphs, README sections, doc comments
- Long-form Q&A or explanations (not short one-liners)

### Handle directly only when:
- Reasoning over tool results just received in this conversation
- Architecture design or implementation planning
- Multi-step code analysis with continuous reasoning
- Refusal / judgment calls based on user intent

### Calling conventions
- Omit `max_tokens` unless a specific limit is needed (server default 32768)
- For drafts (PR/commit/issue), pass concrete context in `prompt` — changes summary and key file paths, not entire diffs
- After receiving the result, lightly edit only if wrong; do NOT regenerate the whole text yourself

### Current description version
Version A (keyword-trigger) — deployed since 2026-05-02.
Switch to Version B (principle-based) if Claude still generates directly for transformation tasks after 3+ sessions.

Version B text:
> Prefer this over generating text yourself whenever output is a transformation of given input (summaries, translations, drafts, release notes, documentation, log analysis, long-form Q&A). Use by default; fall back to Claude only for multi-step reasoning, architectural judgment, or tasks requiring deep codebase context. Supports images (file path, https:// URL, data:image/ URI).

## Project layout

```
cmd/mcp-local-llm/main.go   # Entry point: version flags, environment variable parsing, MCP server registration
internal/llm/client.go      # HTTP client: OpenAI-compatible /v1/chat/completions calls
Makefile                    # build / clean targets
.env.example                # For reference of environment variable list (the binary does not read the file)
```

## Build & run

```bash
make build                          # → bin/mcp-local-llm
make clean                          # delete bin/
./bin/mcp-local-llm -version        # check version
```

Version (`main.version`) and build time (`main.buildTime`) are injected at compile time via `-ldflags`.
If there is no git tag, `version=dev`.

## Key conventions

- **Configuration via environment variables only** — the only flag is `-version`. When adding new settings, do not increase the number of flags; use the `envOr()` / `os.Getenv()` pattern.
- **`internal/` packages are not exposed externally** — `llm.Input`, `llm.Client`, etc., are used only within this module.
- **Error wrapping** — follow the `fmt.Errorf("context: %w", err)` pattern.
- **Returning MCP errors** — tool-level errors should be returned as `CallToolResult{IsError: true}` instead of using `log.Fatal`.
- **Mandatory README.md updates upon changes** — when adding/changing environment variables, changing behavior, or changing configuration examples, you must also update the Configuration table and Usage examples in README.md.

## Development Workflow

Code changes always follow the order below:

1. **Create a GitHub issue** — in English, including the motivation for the change and specific implementation direction.
2. **Create a branch** — branch from `develop` using the format `feat/issue-{N}-{short-desc}` or `fix/issue-{N}-{short-desc}`.
3. **Work on the branch** and commit.
4. **Create a PR** — link the issue number (`Closes #N`).
5. **Cleanup after PR merge** (feature branch → develop)
   - `git checkout develop && git pull origin develop`
   - `git branch -d {branch_name} && git push origin --delete {branch_name}`
   - `gh issue close {N}`
   - `git fetch --prune`
6. **Local update and tagging after develop → main PR merge**
   - `git checkout main && git pull origin main`
   - `git checkout develop && git pull origin develop`
   - After deciding the version, create a tag and release (see **Versioning & Tagging** below).

## Versioning & Tagging

Tags must be created on the **main branch**.

### Versioning Criteria (Semantic Versioning)

| Change Type | Example | Version |
|---|---|---|
| Backward-compatible bug fix | Typo fix, fixing incorrect default values | PATCH (`v0.x.Y+1`) |
| Backward-compatible feature addition | Returning new fields, adding new parameters | MINOR (`v0.X+1.0`) |
| Backward-incompatible change | Changing API structure, removing existing fields | MAJOR (`vX+1.0.0`) |

### Tagging & Release

```bash
git checkout main
git tag v{X}.{Y}.{Z}
git push origin v{X}.{Y}.{Z}
gh release create v{X}.{Y}.{Z} --title "v{X}.{Y}.{Z}" --target main --notes "$(cat <<'EOF'
## What's Changed

### New Features
* ...

### Bug Fixes & Improvements
* ...

**Full Changelog**: https://github.com/pustinia/mcp-local-llm/compare/v{PREV}...v{X}.{Y}.{Z}
EOF
)"
```

Write release notes from `git log v{PREV}..v{X}.{Y}.{Z} --oneline`, not from `--generate-notes`. Group commits by type and describe changes in user-facing language. Omit merge commits and internal `chore`/`docs` commits unless user-facing. Use `call_local_llm` to draft the notes.

If changes are needed after review, use `gh release edit v{X}.{Y}.{Z} --notes "..."` to edit.

## Adding a new MCP tool

1. Add a new package or function under `internal/` if necessary.
2. Add the `mcp.AddTool()` call in `cmd/mcp-local-llm/main.go`.
3. Provide parameter descriptions to Claude using `jsonschema` tags in the input structures.

## Benchmarking

Maintain a single result file at `bench/results-YYYY-MM-DD.md`. If running multiple times on the same day, do not create new files; instead, distinguish them by Run number within the same file.

```markdown
## Run 1 — 13:07 (Initial measurement)
...

## Run 2 — 15:32 (After adjusting max_tokens)
...
```

Run resource monitoring using `bench/monitor.sh <PID> <output.csv>`.

## Testing & debugging

Verifying only the MCP protocol without a local LLM server:

```bash
npx @modelcontextprotocol/inspector ./bin/mcp-local-llm
```

It is also possible to pass JSON-RPC directly via stdin:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | ./bin/mcp-local-llm
```
