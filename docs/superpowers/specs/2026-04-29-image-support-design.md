# Design: Image Support for call_local_llm

**Date:** 2026-04-29
**Status:** Draft

## Overview

`call_local_llm` 도구에 이미지 입력 지원을 추가한다. 파일 경로, URL, base64 data URI를 자동 감지하여 OpenAI vision API 형식으로 변환한 뒤 로컬 LLM 서버에 전달한다. 호출당 최대 이미지 수는 환경변수로 제한한다.

## Goals

- `call_local_llm` 단일 호출로 텍스트+이미지를 함께 전달 가능
- 파일 경로 / URL / base64 data URI 세 가지 형식 혼합 지원
- 최대 이미지 수를 `LOCAL_LLM_MAX_IMAGES` 환경변수로 제어
- 이미지 미사용 호출은 기존 동작과 완전히 동일

## Non-Goals

- 음성/오디오 입력 지원
- 이미지 URL 다운로드 후 base64 변환 (URL은 그대로 서버에 전달)
- 이미지 포맷 검증 (MIME 감지는 파일 경로 인코딩 시에만 수행)

## Architecture

```
call_local_llm(prompt, images[])
    │
    ▼
buildImageContent()          ← 자동 감지 및 변환
    ├─ http(s):// URL    → image_url 파트 (URL 그대로)
    ├─ data:image/ URI   → image_url 파트 (그대로)
    └─ 파일 경로         → os.ReadFile → base64 → data URI → image_url 파트
    │
    ▼
chatMessage.Content = []contentPart{text, image, image, ...}
    │
    ▼
POST /v1/chat/completions    ← OpenAI vision 형식
```

텍스트 전용 호출 시 `Content`는 기존과 동일하게 `string` 유지 (하위 호환).

## Data Structures

### 신규 타입 (`internal/llm/client.go`)

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

### 변경: `chatMessage.Content`

```go
// Before
type chatMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

// After
type chatMessage struct {
    Role    string      `json:"role"`
    Content interface{} `json:"content"` // string (텍스트 전용) 또는 []contentPart (이미지 포함)
}
```

### 변경: `Input` 구조체

```go
// 추가 필드
Images []string `json:"images,omitempty" jsonschema:"이미지 목록 (파일 경로·URL·base64 data URI 혼합 가능)"`
```

### 변경: `Client` 구조체

```go
// 추가 필드
MaxImages int // 0 = 이미지 비활성화, 양수 = 최대 이미지 수
```

## Image Processing Logic

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
        default: // 파일 경로
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

`Call()` 내에서:

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

## Configuration

| Variable | Default | Description |
|---|---|---|
| `LOCAL_LLM_MAX_IMAGES` | `5` | 호출당 최대 이미지 수. `0`으로 설정 시 이미지 비활성화 |

`main.go` 파싱:

```go
maxImages := 5
if v := os.Getenv("LOCAL_LLM_MAX_IMAGES"); v != "" {
    if n, err := strconv.Atoi(v); err == nil && n >= 0 {
        maxImages = n
    }
}
```

`NewClient()` 시그니처:

```go
func NewClient(baseURL, model string, maxTokens int, timeout time.Duration,
               maxConcurrent int, filterThinking bool, maxImages int) *Client
```

## Error Handling

모든 에러는 기존 패턴대로 `CallToolResult{IsError: true}`로 반환한다. `main.go` handler 구조 변경 없음.

| 상황 | 에러 메시지 |
|---|---|
| `LOCAL_LLM_MAX_IMAGES=0` 상태에서 이미지 전달 | `image input is disabled (LOCAL_LLM_MAX_IMAGES=0)` |
| 이미지 수 초과 | `image count N exceeds limit M` |
| 파일 경로 읽기 실패 | `read image "/path": open ...: no such file or directory` |
| LLM 서버가 vision 미지원 | 서버 HTTP 에러 메시지 그대로 전달 |

## Files to Modify

| File | Changes |
|---|---|
| `internal/llm/client.go` | `contentPart`, `imageURL` 타입 추가; `chatMessage.Content` → `interface{}`; `Input.Images` 추가; `Client.MaxImages` 추가; `buildImageContent()` 추가; `Call()` 수정; `NewClient()` 시그니처 변경 |
| `cmd/mcp-local-llm/main.go` | `LOCAL_LLM_MAX_IMAGES` 파싱; `NewClient()` 호출 인자 추가 |
| `README.md` | Configuration 테이블에 `LOCAL_LLM_MAX_IMAGES` 추가; `call_local_llm` 파라미터 테이블에 `images` 추가 |
