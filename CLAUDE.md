# CLAUDE.md

Internal documentation for the AI coding assistant. README.md is for human readers, while this file is for understanding context before coding tasks.

## Local LLM Usage Standards

`call_local_llm` (Gemma 4 26B, 4-bit) is available.
**By default, call the local LLM first.** Only handle directly in the following cases:
- Complex reasoning or multi-step code analysis
- Architecture design or implementation planning
- When judgment is required based on tool call results

Examples of tasks suitable for the local LLM: translation, summarization, drafting, simple Q&A, writing GitHub issue/PR bodies, writing commit messages.
When calling `call_local_llm`, omit `max_tokens` unless there is a specific reason for token limits (uses server default 32768).

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
gh release create v{X}.{Y}.{Z} --title "v{X}.{Y}.{Z}" --generate-notes --target main
```

After creating a release, **always review the release notes**. `--generate-notes` may sometimes list the feature PR (develop→main) and its constituent PRs redundantly. Consolidate duplicate items and connect PR numbers with commas:

```
* feat: some feature by @user in https://.../pull/7, https://.../pull/8
```

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
