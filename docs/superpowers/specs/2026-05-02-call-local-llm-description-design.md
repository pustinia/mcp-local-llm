# Design Spec: Optimizing `call_local_llm` Tool Description for Cost Reduction

**Date:** 2026-05-02
**Status:** Draft

## Background

Claude frequently generates text that could be handled by the local LLM, leading to unnecessary Anthropic token costs. The MCP tool description is read at tool-selection time, making it a direct lever for changing Claude's behavior. The current description ("간단한 요약, 번역, 코드 생성, 빠른 질문 응답 등에 활용하세요") is a soft suggestion — Claude has no strong reason to prefer it over generating directly.

## Goal

Reduce Anthropic token costs by making Claude delegate transformation tasks to `call_local_llm` by default, while maintaining output quality.

**Core criterion:** Tasks where the output is largely determined by the input ("transformation tasks") produce equivalent quality from the local LLM. Claude is only needed when multi-step reasoning, architectural judgment, or deep codebase context is required.

## Design

Both versions are written in English to minimize description token overhead.

### Version A: Keyword-Trigger (Short)

Relies on explicit task-type keywords and a token threshold to trigger delegation.

```
ALWAYS use this instead of generating text yourself for: summaries,
translations, PR/commit/issue drafts, release notes, documentation,
and any response over ~70 tokens. Cheaper than Claude.
Supports images (file path, https:// URL, data:image/ URI).
```

**Strengths:** Low token cost, strong imperative (`ALWAYS`), direct suppression of self-generation (`instead of generating text yourself`), break-even threshold aligned with CLAUDE.md (~70 tokens).

**Weakness:** Coverage limited to enumerated task types; edge cases require Claude's judgment.

### Version B: Principle-Based (Long)

Defines a decision rule based on task nature, covering cases not in the keyword list.

```
Prefer this over generating text yourself whenever output is a
transformation of given input (summaries, translations, drafts,
release notes, documentation, log analysis, long-form Q&A).
Use by default; fall back to Claude only for multi-step reasoning,
architectural judgment, or tasks requiring deep codebase context.
Supports images (file path, https:// URL, data:image/ URI).
```

**Strengths:** `transformation of given input` principle covers unlisted cases automatically; `Use by default` frames local LLM as the default choice; fallback conditions are narrow and explicit.

**Weakness:** Slightly longer — marginally higher description token cost per session.

### Comparison Method

1. Deploy **Version A** first.
2. Use for at least 3 sessions; observe `[usage: prompt=...]` tag frequency and `get_llm_usage` counts.
3. If Claude still generates directly for transformation tasks (PR descriptions, summaries, release notes), switch to **Version B**.
4. Record switch date in CLAUDE.md.

## Implementation

**File:** `cmd/mcp-local-llm/main.go`
**Change:** Replace the `Description` field in the `call_local_llm` tool definition.

```go
// Version A
Description: "ALWAYS use this instead of generating text yourself for: summaries, " +
    "translations, PR/commit/issue drafts, release notes, documentation, " +
    "and any response over ~70 tokens. Cheaper than Claude. " +
    "Supports images (file path, https:// URL, data:image/ URI).",
```

After `make build` and Claude Code restart, the new description takes effect immediately.

## Measurement

- **Primary:** `get_llm_usage` call count per session — higher = more delegation
- **Secondary:** Presence of `[usage: ...]` tag in Claude's responses — visible indicator that local LLM was used instead of direct generation

## CLAUDE.md Update

Add a version tracking note under **Local LLM Usage Standards**:

```markdown
### Current description version
Version A (keyword-trigger) — deployed since 2026-05-02.
Switch to Version B (principle-based) if Claude still generates directly for transformation tasks.
```
