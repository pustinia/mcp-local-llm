# call_local_llm Description A/B Test

테스트 목적: description 문구에 따라 Claude가 `call_local_llm`을 얼마나 적절하게 선택하는지 측정.

## 측정 방법

각 테스트 케이스를 **최소 컨텍스트 서브에이전트**에 디스패치한다. 서브에이전트는 CLAUDE.md 없이 도구 description만으로 판단하므로, description의 순수한 유도 효과를 측정할 수 있다.

서브에이전트 프롬프트 형식:
```
You are a helpful assistant. Complete the task below.
You have access to the `call_local_llm` MCP tool. Use your own judgment about whether to use it or generate the output directly yourself.

**Task:** [task]

After completing the task, end your response with:
USED_TOOL: YES or NO
REASON: one sentence why
```

## 버전 교체 방법

**Version A → Version B:**
```go
// cmd/mcp-local-llm/main.go:148
// Version A (keyword-trigger)
Description: "ALWAYS use this instead of generating text yourself for: summaries, " +
    "translations, PR/commit/issue drafts, release notes, documentation, " +
    "and any response over ~70 tokens. Cheaper than Claude. " +
    "Supports images (file path, https:// URL, data:image/ URI).",

// Version B (principle-based)
Description: "Prefer this over generating text yourself whenever output is a " +
    "transformation of given input (summaries, translations, drafts, " +
    "release notes, documentation, log analysis, long-form Q&A). " +
    "Use by default; fall back to Claude only for multi-step reasoning, " +
    "architectural judgment, or tasks requiring deep codebase context. " +
    "Supports images (file path, https:// URL, data:image/ URI).",
```

교체 후 반드시 `make build` 실행.

## 테스트 케이스

### T1: Release notes (도구 사용 기대)
```
Write release notes from the following git log:

feat: add image input support to call_local_llm
fix: add file size limit and MIME validation
fix: resolve defer-in-loop bug
docs: document images parameter and LOCAL_LLM_MAX_IMAGES env var
```

### T2: PR description (도구 사용 기대)
```
Write a GitHub PR description for the following change:

Added `images` parameter to `call_local_llm` MCP tool. Supports file paths, https:// URLs,
and data:image/ URIs. New env var `LOCAL_LLM_MAX_IMAGES` (default 5, 0=disabled,
negative=unlimited). Added 10 unit tests.
```

### T3: Translation (도구 사용 기대)
```
Translate the following paragraph to Korean:

"The local LLM server runs on port 8000 and accepts OpenAI-compatible requests.
Images can be passed as file paths, HTTPS URLs, or base64 data URIs. The maximum
number of images per request is controlled by the LOCAL_LLM_MAX_IMAGES environment variable."
```

### T4: Commit message (도구 사용 기대)
```
Write a git commit message for the following change:

Changed the `Description` field of the `call_local_llm` MCP tool in
`cmd/mcp-local-llm/main.go` from a Korean soft-suggestion string to an English
imperative string that explicitly tells Claude to always use the tool instead of
generating text directly.
```

### T5: Bug analysis (직접 생성 기대)
```
Analyze why this test is failing and explain the root cause:

--- FAIL: TestBuildImageContent_FilePath
    client_test.go:45: got "data:application/octet-stream;base64,..." want prefix "data:image/"

The function `readImageAsDataURI` uses `http.DetectContentType` to determine MIME type.
The test uses a PNG file created programmatically.
```

### T6: Architecture design (직접 생성 기대)
```
Design the architecture for adding audio input support to the `call_local_llm` MCP tool
(currently supports text + images). Consider: input format, file size limits, MIME detection,
and how it fits the existing `Input` struct pattern.
```

## 결과 기록

### Run 1 — 2026-05-02 (초기 측정)

| # | 태스크 | 기대 | Version A | Version B |
|---|--------|------|-----------|-----------|
| T1 | Release notes | 도구 사용 ✅ | YES ✅ | YES ✅ |
| T2 | PR description | 도구 사용 ✅ | YES ✅ | YES ✅ |
| T3 | Translation | 도구 사용 ✅ | YES ✅ | NO ⚠️ |
| T4 | Commit message | 도구 사용 ✅ | NO ❌ | YES ✅ |
| T5 | Bug analysis | 직접 생성 ✅ | NO ✅ | NO ✅ |
| T6 | Architecture design | 직접 생성 ✅ | NO ✅ | YES ❌ |
| | **정확도** | 6/6 목표 | **5/6 (83%)** | **4/6 (67%)** |

**T3 Version B 주석:** 서브에이전트에서 MCP 서버 연결 실패로 `NO` 응답. 실제 description 유도 효과가 아닌 환경 문제일 가능성 있음 — 재테스트 필요.

**분석:**
- Version A: T4(커밋 메시지) 미스 — "짧은 출력이라 직접 생성" 판단. `~70 tokens` 기준이 오히려 커밋 메시지를 제외시킴.
- Version B: T6(아키텍처 설계) 오사용 — "transformation" 원칙이 너무 넓어 설계 문서도 포함.
- Version A의 오류 방향(과소 위임)이 Version B의 오류 방향(과잉 위임)보다 품질 리스크 낮음.

**결론: Version A 유지 (2026-05-02)**
