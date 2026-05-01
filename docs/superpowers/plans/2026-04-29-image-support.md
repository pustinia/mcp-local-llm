# Image Support for call_local_llm — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional `images` parameter to `call_local_llm` that accepts file paths, URLs, and base64 data URIs, forwarding them to the local LLM server in OpenAI vision format.

**Architecture:** Auto-detect each image string's format in `buildImageContent()` inside `internal/llm/client.go`. When `in.Images` is non-empty, build a multimodal `[]contentPart` array as `chatMessage.Content`; otherwise keep the existing plain string path unchanged. Maximum images per call is controlled by a new `LOCAL_LLM_MAX_IMAGES` env var (default 5).

**Tech Stack:** Go 1.22, `encoding/base64`, `net/http` (MIME detection), `os` (file read), OpenAI `/v1/chat/completions` vision format.

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `internal/llm/client.go` | Modify | Add types, `buildImageContent()`, update `Call()` and `NewClient()` |
| `internal/llm/client_test.go` | Create | Unit tests for `buildImageContent()` |
| `cmd/mcp-local-llm/main.go` | Modify | Parse `LOCAL_LLM_MAX_IMAGES`, pass to `NewClient()` |
| `README.md` | Modify | Document new env var and `images` parameter |

---

## Task 1: GitHub Issue and Branch

**Files:**
- No code changes

- [ ] **Step 1: Create GitHub issue**

```bash
gh issue create \
  --title "feat: add image input support to call_local_llm" \
  --body "$(mcp__local-llm__call_local_llm '
Write a GitHub issue body in English for the following feature:

Add image input support to the call_local_llm MCP tool.

Motivation:
- The current tool only accepts text prompts
- The default model (Gemma 4 26B) is multimodal and supports image input
- Callers cannot leverage vision capabilities without this change

Implementation direction:
- Add optional images []string field to Input struct
- Auto-detect format per string: http(s):// URL → pass as-is, data:image/ → pass as-is, other → treat as file path and encode to base64
- Change chatMessage.Content from string to interface{} to support both string and []contentPart
- Add LOCAL_LLM_MAX_IMAGES env var (default 5, 0 = disabled) to cap images per call
- Text-only calls remain unchanged (no images field = same behavior as before)
- Update README.md with new parameter and env var
')"
```

Note the issue number from the output (e.g., `#14`).

- [ ] **Step 2: Create feature branch**

```bash
git checkout develop
git pull origin develop
git checkout -b feat/issue-{N}-image-support
```

Replace `{N}` with the actual issue number.

---

## Task 2: Add Types and Struct Changes to client.go

**Files:**
- Modify: `internal/llm/client.go`

- [ ] **Step 1: Add imports**

In `internal/llm/client.go`, update the import block to include `encoding/base64` and `os`:

```go
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)
```

- [ ] **Step 2: Add contentPart and imageURL types**

Insert after the `thinkingRe` and `defaultTimeout` declarations (before `type Input struct`):

```go
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}
```

- [ ] **Step 3: Add Images field to Input**

In the `Input` struct, add after the `FilterThinking` field:

```go
Images []string `json:"images,omitempty"          jsonschema:"이미지 목록 (파일 경로·URL·base64 data URI 혼합 가능)"`
```

The complete `Input` struct becomes:

```go
type Input struct {
	Prompt         string   `json:"prompt"                    jsonschema:"LLM에 전달할 사용자 메시지,required"`
	System         string   `json:"system,omitempty"          jsonschema:"시스템 프롬프트 (선택사항)"`
	Model          string   `json:"model,omitempty"           jsonschema:"사용할 모델 이름 (기본값: gemma-4-26b-a4b-it-4bit)"`
	MaxTokens      int      `json:"max_tokens,omitempty"      jsonschema:"최대 출력 토큰 수 (생략 시 서버 기본값 사용)"`
	FilterThinking *bool    `json:"filter_thinking,omitempty" jsonschema:"thinking 블록 필터링 여부 (true=필터, false=유지). 생략 시 서버 기본값(LOCAL_LLM_FILTER_THINKING) 적용"`
	Images         []string `json:"images,omitempty"          jsonschema:"이미지 목록 (파일 경로·URL·base64 data URI 혼합 가능)"`
}
```

- [ ] **Step 4: Change chatMessage.Content to interface{}**

```go
type chatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}
```

- [ ] **Step 5: Add MaxImages to Client struct**

Add `MaxImages int` after `FilterThinking bool`:

```go
type Client struct {
	BaseURL          string
	DefaultModel     string
	DefaultMaxTokens int
	FilterThinking   bool
	MaxImages        int
	httpClient       *http.Client
	sem              chan struct{}
}
```

- [ ] **Step 6: Update NewClient signature and body**

```go
func NewClient(baseURL, model string, maxTokens int, timeout time.Duration, maxConcurrent int, filterThinking bool, maxImages int) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	var sem chan struct{}
	if maxConcurrent > 0 {
		sem = make(chan struct{}, maxConcurrent)
	}
	return &Client{
		BaseURL:          baseURL,
		DefaultModel:     model,
		DefaultMaxTokens: maxTokens,
		FilterThinking:   filterThinking,
		MaxImages:        maxImages,
		httpClient:       &http.Client{Timeout: timeout},
		sem:              sem,
	}
}
```

- [ ] **Step 7: Verify compilation**

```bash
make build
```

Expected: build succeeds. The `main.go` will fail to compile because `NewClient` now has one extra argument — that's expected and will be properly fixed in Task 5.

Fix the compile error temporarily by updating the `NewClient` call in `main.go` to pass `5` as the last argument:

```go
client := llm.NewClient(baseURL, model, maxTokens, timeout, maxConcurrent, filterThinking, 5)
```

Run again:

```bash
make build
```

Expected: `bin/mcp-local-llm` created successfully.

---

## Task 3: Write Unit Tests for buildImageContent (TDD)

**Files:**
- Create: `internal/llm/client_test.go`

- [ ] **Step 1: Create test file**

```go
package llm

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestBuildImageContent_URL(t *testing.T) {
	parts, err := buildImageContent("describe this", []string{"https://example.com/photo.jpg"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "describe this" {
		t.Errorf("text part: got %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "https://example.com/photo.jpg" {
		t.Errorf("image part: got %+v", parts[1])
	}
}

func TestBuildImageContent_DataURI(t *testing.T) {
	dataURI := "data:image/jpeg;base64,/9j/4AAQSkZJRgAB"
	parts, err := buildImageContent("hello", []string{dataURI}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts[1].ImageURL.URL != dataURI {
		t.Errorf("expected data URI to pass through unchanged, got %s", parts[1].ImageURL.URL)
	}
}

func TestBuildImageContent_FilePath(t *testing.T) {
	f, err := os.CreateTemp("", "test-image-*.png")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if _, err := f.Write(pngHeader); err != nil {
		t.Fatal(err)
	}
	f.Close()

	parts, err := buildImageContent("hello", []string{f.Name()}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader)
	if parts[1].ImageURL.URL != expected {
		t.Errorf("expected %s, got %s", expected, parts[1].ImageURL.URL)
	}
}

func TestBuildImageContent_TextPartIsFirst(t *testing.T) {
	parts, err := buildImageContent("my prompt", []string{"https://example.com/img.jpg"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts[0].Type != "text" || parts[0].Text != "my prompt" {
		t.Errorf("first part should be text with prompt, got %+v", parts[0])
	}
}

func TestBuildImageContent_MultipleImages(t *testing.T) {
	images := []string{
		"https://example.com/1.jpg",
		"https://example.com/2.jpg",
	}
	parts, err := buildImageContent("compare", images, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 3 {
		t.Errorf("expected 3 parts (1 text + 2 images), got %d", len(parts))
	}
}

func TestBuildImageContent_ExactLimit(t *testing.T) {
	images := []string{
		"https://example.com/1.jpg",
		"https://example.com/2.jpg",
	}
	parts, err := buildImageContent("hello", images, 2)
	if err != nil {
		t.Fatalf("unexpected error at exact limit: %v", err)
	}
	if len(parts) != 3 {
		t.Errorf("expected 3 parts, got %d", len(parts))
	}
}

func TestBuildImageContent_ExceedsLimit(t *testing.T) {
	images := []string{
		"https://example.com/1.jpg",
		"https://example.com/2.jpg",
	}
	_, err := buildImageContent("hello", images, 1)
	if err == nil {
		t.Fatal("expected error for exceeding limit")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildImageContent_DisabledWithZero(t *testing.T) {
	_, err := buildImageContent("hello", []string{"https://example.com/img.jpg"}, 0)
	if err == nil {
		t.Fatal("expected error when images disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildImageContent_FileNotFound(t *testing.T) {
	_, err := buildImageContent("hello", []string{"/nonexistent/path/image.png"}, 5)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
go test ./internal/llm/...
```

Expected: compile error — `undefined: buildImageContent`. This confirms TDD setup is correct.

---

## Task 4: Implement buildImageContent and Update Call()

**Files:**
- Modify: `internal/llm/client.go`

- [ ] **Step 1: Add buildImageContent function**

Insert before `func (c *Client) Call(...)`:

```go
func buildImageContent(prompt string, images []string, maxImages int) ([]contentPart, error) {
	if maxImages == 0 {
		return nil, fmt.Errorf("image input is disabled (LOCAL_LLM_MAX_IMAGES=0)")
	}
	if maxImages > 0 && len(images) > maxImages {
		return nil, fmt.Errorf("image count %d exceeds limit %d", len(images), maxImages)
	}
	parts := []contentPart{{Type: "text", Text: prompt}}
	for _, img := range images {
		var dataURL string
		switch {
		case strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://"):
			dataURL = img
		case strings.HasPrefix(img, "data:image/"):
			dataURL = img
		default:
			data, err := os.ReadFile(img)
			if err != nil {
				return nil, fmt.Errorf("read image %q: %w", img, err)
			}
			mime := http.DetectContentType(data)
			dataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
		parts = append(parts, contentPart{
			Type:     "image_url",
			ImageURL: &imageURL{URL: dataURL},
		})
	}
	return parts, nil
}
```

- [ ] **Step 2: Update Call() — replace user message construction**

Find this block in `Call()`:

```go
messages = append(messages, chatMessage{Role: "user", Content: in.Prompt})
```

Replace with:

```go
if len(in.Images) > 0 {
    parts, err := buildImageContent(in.Prompt, in.Images, c.MaxImages)
    if err != nil {
        return "", Usage{}, err
    }
    messages = append(messages, chatMessage{Role: "user", Content: parts})
} else {
    messages = append(messages, chatMessage{Role: "user", Content: in.Prompt})
}
```

- [ ] **Step 3: Run tests — verify they pass**

```bash
go test ./internal/llm/... -v
```

Expected: all 9 tests PASS.

- [ ] **Step 4: Build**

```bash
make build
```

Expected: `bin/mcp-local-llm` builds successfully.

- [ ] **Step 5: Commit**

```bash
git add internal/llm/client.go internal/llm/client_test.go
git commit -m "feat: add image input support to call_local_llm

- Add contentPart/imageURL types for OpenAI vision format
- Add Images []string to Input; MaxImages int to Client
- Implement buildImageContent() with auto-detect (URL/data URI/file path)
- Update Call() to send multimodal content when images present
- Text-only calls unchanged (backward compatible)"
```

---

## Task 5: Update main.go

**Files:**
- Modify: `cmd/mcp-local-llm/main.go`

- [ ] **Step 1: Add LOCAL_LLM_MAX_IMAGES parsing**

In `main()`, after the `filterThinking` line:

```go
filterThinking := envOr("LOCAL_LLM_FILTER_THINKING", "true") != "false"
```

Add:

```go
maxImages := 5
if v := os.Getenv("LOCAL_LLM_MAX_IMAGES"); v != "" {
    if n, err := strconv.Atoi(v); err == nil && n >= 0 {
        maxImages = n
    }
}
```

- [ ] **Step 2: Update NewClient call**

Replace the temporary call from Task 2:

```go
client := llm.NewClient(baseURL, model, maxTokens, timeout, maxConcurrent, filterThinking, 5)
```

With:

```go
client := llm.NewClient(baseURL, model, maxTokens, timeout, maxConcurrent, filterThinking, maxImages)
```

- [ ] **Step 3: Build and verify**

```bash
make build
./bin/mcp-local-llm -version
```

Expected: version string printed, no errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/mcp-local-llm/main.go
git commit -m "feat: parse LOCAL_LLM_MAX_IMAGES env var and pass to client"
```

---

## Task 6: Update README.md

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add LOCAL_LLM_MAX_IMAGES to Configuration table**

In the Configuration table, after the `LOCAL_LLM_FILTER_THINKING` row, add:

```markdown
| `LOCAL_LLM_MAX_IMAGES` | `5` | Maximum images per `call_local_llm` call. Set to `0` to disable image input. |
```

- [ ] **Step 2: Add images parameter to call_local_llm tool reference**

In the `call_local_llm` parameter table, add after `filter_thinking`:

```markdown
| `images` | no | List of images to send with the prompt. Each entry can be a file path, an `https://` URL, or a `data:image/...;base64,...` URI. Mixed formats allowed. Capped by `LOCAL_LLM_MAX_IMAGES`. |
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document images parameter and LOCAL_LLM_MAX_IMAGES env var"
```

---

## Task 7: Create PR

**Files:**
- No code changes

- [ ] **Step 1: Push branch**

```bash
git push -u origin feat/issue-{N}-image-support
```

- [ ] **Step 2: Create PR**

```bash
gh pr create \
  --title "feat: add image input support to call_local_llm" \
  --base develop \
  --body "$(mcp__local-llm__call_local_llm '
Write a GitHub PR description in English for the following changes:

Added image input support to the call_local_llm MCP tool.

Changes:
- internal/llm/client.go: added contentPart/imageURL types, Images field on Input, MaxImages on Client, buildImageContent() function with auto-detection of URL/data URI/file path, updated Call() to send multimodal content
- internal/llm/client_test.go: 9 unit tests for buildImageContent covering all format types, limit enforcement, and error cases
- cmd/mcp-local-llm/main.go: parse LOCAL_LLM_MAX_IMAGES env var (default 5, 0=disabled), pass to NewClient
- README.md: document new images parameter and LOCAL_LLM_MAX_IMAGES

Closes #{N}
')"
```
