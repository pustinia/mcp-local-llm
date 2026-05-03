package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var thinkingRe = regexp.MustCompile(`(?s)<\|channel>thought.*?<channel\|>`)

const defaultTimeout = 5 * time.Minute

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type Input struct {
	Prompt         string   `json:"prompt"                    jsonschema:"LLM에 전달할 사용자 메시지,required"`
	System         string   `json:"system,omitempty"          jsonschema:"시스템 프롬프트 (선택사항)"`
	Model          string   `json:"model,omitempty"           jsonschema:"사용할 모델 이름 (기본값: gemma-4-26b-a4b-it-4bit)"`
	MaxTokens      int      `json:"max_tokens,omitempty"      jsonschema:"최대 출력 토큰 수 (생략 시 서버 기본값 사용)"`
	FilterThinking *bool    `json:"filter_thinking,omitempty" jsonschema:"thinking 블록 필터링 여부 (true=필터, false=유지). 생략 시 서버 기본값(LOCAL_LLM_FILTER_THINKING) 적용"`
	Images         []string `json:"images,omitempty"          jsonschema:"이미지 목록 (파일 경로·URL·base64 data URI 혼합 가능)"`
}

type chatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens *int          `json:"max_tokens,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Usage Usage `json:"usage"`
}

type Client struct {
	BaseURL          string
	DefaultModel     string
	DefaultMaxTokens int
	FilterThinking   bool
	MaxImages        int
	httpClient       *http.Client
	sem              chan struct{} // nil = 무제한
}

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

func readImageAsDataURI(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read image %q: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat image %q: %w", path, err)
	}
	const maxImageBytes = 20 << 20 // 20 MB
	if info.Size() > maxImageBytes {
		return "", fmt.Errorf("image %q size %d exceeds %d bytes", path, info.Size(), maxImageBytes)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read image %q: %w", path, err)
	}
	mime := http.DetectContentType(data)
	if mime == "application/octet-stream" {
		return "", fmt.Errorf("image %q: unrecognized format", path)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

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
			var err error
			dataURL, err = readImageAsDataURI(img)
			if err != nil {
				return nil, err
			}
		}
		parts = append(parts, contentPart{
			Type:     "image_url",
			ImageURL: &imageURL{URL: dataURL},
		})
	}
	return parts, nil
}

func validatePath(path, workDir string) (string, error) {
	if workDir == "" {
		return "", fmt.Errorf("validatePath: workDir must not be empty")
	}
	workDir = filepath.Clean(workDir)
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	} else {
		path = filepath.Clean(path)
	}
	if path != workDir && !strings.HasPrefix(path, workDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside work directory %q", path, workDir)
	}
	return path, nil
}

func (c *Client) Call(ctx context.Context, in *Input) (string, Usage, error) {
	if c.sem != nil {
		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		case <-ctx.Done():
			return "", Usage{}, ctx.Err()
		}
	}
	messages := []chatMessage{}
	if in.System != "" {
		messages = append(messages, chatMessage{Role: "system", Content: in.System})
	}
	if len(in.Images) > 0 {
		parts, err := buildImageContent(in.Prompt, in.Images, c.MaxImages)
		if err != nil {
			return "", Usage{}, err
		}
		messages = append(messages, chatMessage{Role: "user", Content: parts})
	} else {
		messages = append(messages, chatMessage{Role: "user", Content: in.Prompt})
	}

	model := in.Model
	if model == "" {
		model = c.DefaultModel
	}
	var maxTokens *int
	if in.MaxTokens > 0 {
		v := in.MaxTokens
		maxTokens = &v
	} else if c.DefaultMaxTokens > 0 {
		v := c.DefaultMaxTokens
		maxTokens = &v
	}

	body, err := json.Marshal(chatRequest{Model: model, Messages: messages, MaxTokens: maxTokens})
	if err != nil {
		return "", Usage{}, fmt.Errorf("marshal failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", Usage{}, fmt.Errorf("read response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, fmt.Errorf("server returned %d: %s", resp.StatusCode, raw)
	}

	var result chatResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", Usage{}, fmt.Errorf("parse failed: %w", err)
	}
	if result.Error != nil {
		return "", Usage{}, fmt.Errorf("API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("empty response")
	}
	content := result.Choices[0].Message.Content
	filterThinking := c.FilterThinking
	if in.FilterThinking != nil {
		filterThinking = *in.FilterThinking
	}
	if filterThinking {
		content = strings.TrimSpace(thinkingRe.ReplaceAllString(content, ""))
	}
	return content, result.Usage, nil
}
