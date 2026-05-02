# call_local_llm Description Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Korean soft-suggestion description with an English imperative (Version A) to make Claude delegate transformation tasks to the local LLM by default, reducing Anthropic token costs.

**Architecture:** Single-field change in `cmd/mcp-local-llm/main.go` + version tracking note in `CLAUDE.md`. No logic changes, no new types, no tests required (pure string update verified by tools/list output).

**Tech Stack:** Go, MCP SDK (`github.com/modelcontextprotocol/go-sdk/mcp`), Make

---

### Task 1: Update call_local_llm description to Version A

**Files:**
- Modify: `cmd/mcp-local-llm/main.go:148`

- [ ] **Step 1: Replace the Description field**

In `cmd/mcp-local-llm/main.go`, replace line 148:

```go
// Before
Description: fmt.Sprintf("로컬에서 실행 중인 LLM(%s)을 호출합니다. 간단한 요약, 번역, 코드 생성, 빠른 질문 응답 등에 활용하세요.", model),

// After
Description: "ALWAYS use this instead of generating text yourself for: summaries, " +
    "translations, PR/commit/issue drafts, release notes, documentation, " +
    "and any response over ~70 tokens. Cheaper than Claude. " +
    "Supports images (file path, https:// URL, data:image/ URI).",
```

Note: the `model` variable in `fmt.Sprintf` is removed — Version A description does not include the model name (reduces token overhead; model info is available via `get_llm_usage` if needed).

- [ ] **Step 2: Build**

```bash
make build
```

Expected: no errors, `bin/mcp-local-llm` updated.

- [ ] **Step 3: Verify description appears in tools/list**

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | ./bin/mcp-local-llm
```

Expected output contains:
```
"description":"ALWAYS use this instead of generating text yourself for: summaries, translations, PR/commit/issue drafts, release notes, documentation, and any response over ~70 tokens. Cheaper than Claude. Supports images (file path, https:// URL, data:image/ URI)."
```

- [ ] **Step 4: Commit**

```bash
git add cmd/mcp-local-llm/main.go
git commit -m "feat: update call_local_llm description to Version A (English, imperative)"
```

---

### Task 2: Add version tracking note to CLAUDE.md

**Files:**
- Modify: `CLAUDE.md` — insert after line 28 (end of `### Calling conventions` section)

- [ ] **Step 1: Add version tracking section**

In `CLAUDE.md`, add the following block after the `### Calling conventions` section (after line 28):

```markdown

### Current description version
Version A (keyword-trigger) — deployed since 2026-05-02.
Switch to Version B (principle-based) if Claude still generates directly for transformation tasks after 3+ sessions.

Version B text:
> Prefer this over generating text yourself whenever output is a transformation of given input (summaries, translations, drafts, release notes, documentation, log analysis, long-form Q&A). Use by default; fall back to Claude only for multi-step reasoning, architectural judgment, or tasks requiring deep codebase context. Supports images (file path, https:// URL, data:image/ URI).
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add call_local_llm description version tracking to CLAUDE.md"
```
