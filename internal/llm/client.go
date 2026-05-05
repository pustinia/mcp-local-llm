package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var thinkingRe = regexp.MustCompile(`(?s)<\|channel>thought.*?<channel\|>`)

const (
	defaultTimeout = 5 * time.Minute
	maxRetries     = 2
)

var retryDelays = [maxRetries]time.Duration{
	200 * time.Millisecond,
	1 * time.Second,
}

func isRetryable(err error, statusCode int) bool {
	if statusCode >= 500 {
		return true
	}
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Temporary() { //nolint:staticcheck
		return true
	}
	return strings.Contains(err.Error(), "connection refused")
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Input struct {
	Prompt         string            `json:"prompt"                    jsonschema:"LLM에 전달할 사용자 메시지,required"`
	System         string            `json:"system,omitempty"          jsonschema:"시스템 프롬프트 (선택사항)"`
	Model          string            `json:"model,omitempty"           jsonschema:"사용할 모델 이름 (기본값: gemma-4-26b-a4b-it-4bit)"`
	MaxTokens      int               `json:"max_tokens,omitempty"      jsonschema:"최대 출력 토큰 수 (생략 시 서버 기본값 사용)"`
	FilterThinking *bool             `json:"filter_thinking,omitempty" jsonschema:"thinking 블록 필터링 여부 (true=필터, false=유지). 생략 시 서버 기본값(LOCAL_LLM_FILTER_THINKING) 적용"`
	Images         []string          `json:"images,omitempty"          jsonschema:"이미지 목록 (파일 경로·URL·base64 data URI 혼합 가능)"`
	InputFiles     []string          `json:"input_files,omitempty"     jsonschema:"읽어서 프롬프트에 포함할 파일 경로 목록. MCP 서버가 직접 읽어 Claude 컨텍스트 절약"`
	OutputFile     string            `json:"output_file,omitempty"     jsonschema:"결과를 저장할 파일 경로. 지정 시 저장 완료 메시지만 반환 (Claude 컨텍스트 절약)"`
	Append         bool              `json:"append,omitempty"          jsonschema:"true이면 output_file에 이어쓰기. 기본값 false (덮어쓰기)"`
	Tools      string `json:"tools,omitempty"       jsonschema:"로컬 LLM에 제공할 툴 스키마 배열 (JSON 문자열). 예: [{\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"description\":\"...\",\"parameters\":{...}}}]. 응답에 tool_calls가 있으면 JSON 배열 문자열로 반환됨"`
	ToolChoice string `json:"tool_choice,omitempty" jsonschema:"툴 선택 방식: \"auto\", \"none\", 또는 {\"type\":\"function\",\"function\":{\"name\":\"toolName\"}}"`
	Messages   string `json:"messages,omitempty"    jsonschema:"멀티턴 메시지 배열 (JSON 문자열). OpenAI messages format. 제공 시 prompt/system/images/input_files 무시. tool_calls 응답 후 재호출 시 전체 대화 히스토리 전달"`
}

type chatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type chatRequest struct {
	Model      string            `json:"model"`
	Messages   interface{}       `json:"messages"`
	MaxTokens  *int              `json:"max_tokens,omitempty"`
	Tools      []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice interface{}       `json:"tool_choice,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []toolCall `json:"tool_calls,omitempty"`
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
	MaxInputBytes    int
	WorkDir          string
	httpClient       *http.Client
	sem              chan struct{} // nil = 무제한
}

func NewClient(baseURL, model string, maxTokens int, timeout time.Duration, maxConcurrent int, filterThinking bool, maxImages int, maxInputBytes int, workDir string) *Client {
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
		MaxInputBytes:    maxInputBytes,
		WorkDir:          workDir,
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

func buildPromptWithFiles(prompt string, files []string, maxBytes int) (string, error) {
	if len(files) == 0 {
		return prompt, nil
	}
	// Pass 1: stat only — reject before reading if total size exceeds limit
	var total int64
	for i, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("input_files[%d]: file not found: %s", i, path)
			}
			return "", fmt.Errorf("input_files[%d]: stat failed: %w", i, err)
		}
		total += info.Size()
		if maxBytes > 0 && total > int64(maxBytes) {
			return "", fmt.Errorf("input_files total size (%d bytes) exceeds limit (%d bytes). "+
				"Reduce file count or set LOCAL_LLM_MAX_INPUT_BYTES to increase limit.", total, maxBytes)
		}
	}
	// Pass 2: read all files (size already validated)
	var sb strings.Builder
	sb.WriteString(prompt)
	for i, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsPermission(err) {
				return "", fmt.Errorf("input_files[%d]: permission denied: %s", i, path)
			}
			return "", fmt.Errorf("input_files[%d]: read failed: %w", i, err)
		}
		sb.WriteString("\n\n[file: ")
		sb.WriteString(path)
		sb.WriteString("]\n")
		sb.Write(data)
	}
	result := sb.String()
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result += "\n"
	}
	return result, nil
}

func writeOutput(path, text string, appendMode bool) (string, error) {
	if appendMode {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return "", fmt.Errorf("output_file: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString(text); err != nil {
			return "", fmt.Errorf("output_file: write failed: %w", err)
		}
	} else {
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			return "", fmt.Errorf("output_file: %w", err)
		}
	}
	return fmt.Sprintf("saved %d bytes to %s", len(text), path), nil
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

	// Validate and resolve output file path against work directory
	outputFile := in.OutputFile
	if in.OutputFile != "" {
		p, err := validatePath(in.OutputFile, c.WorkDir)
		if err != nil {
			return "", Usage{}, fmt.Errorf("output_file: %w", err)
		}
		outputFile = p
	}

	// Parse tools JSON string → typed slice for chatRequest
	var tools []map[string]interface{}
	if in.Tools != "" {
		if err := json.Unmarshal([]byte(in.Tools), &tools); err != nil {
			return "", Usage{}, fmt.Errorf("tools: invalid JSON: %w", err)
		}
	}

	// Parse tool_choice: try as JSON object first, fall back to plain string
	var toolChoice interface{}
	if in.ToolChoice != "" {
		var obj interface{}
		if err := json.Unmarshal([]byte(in.ToolChoice), &obj); err == nil {
			toolChoice = obj
		} else {
			toolChoice = in.ToolChoice
		}
	}

	// Build messages: parse JSON string when provided, else construct from prompt/system/images/input_files
	var messages interface{}
	if in.Messages != "" {
		var parsed []map[string]interface{}
		if err := json.Unmarshal([]byte(in.Messages), &parsed); err != nil {
			return "", Usage{}, fmt.Errorf("messages: invalid JSON: %w", err)
		}
		messages = parsed
	} else {
		inputFiles := make([]string, len(in.InputFiles))
		for i, f := range in.InputFiles {
			p, err := validatePath(f, c.WorkDir)
			if err != nil {
				return "", Usage{}, fmt.Errorf("input_files[%d]: %w", i, err)
			}
			inputFiles[i] = p
		}

		prompt := in.Prompt
		if len(inputFiles) > 0 {
			var err error
			prompt, err = buildPromptWithFiles(in.Prompt, inputFiles, c.MaxInputBytes)
			if err != nil {
				return "", Usage{}, err
			}
		}

		chatMsgs := []chatMessage{}
		if in.System != "" {
			chatMsgs = append(chatMsgs, chatMessage{Role: "system", Content: in.System})
		}
		if len(in.Images) > 0 {
			parts, err := buildImageContent(prompt, in.Images, c.MaxImages)
			if err != nil {
				return "", Usage{}, err
			}
			chatMsgs = append(chatMsgs, chatMessage{Role: "user", Content: parts})
		} else {
			chatMsgs = append(chatMsgs, chatMessage{Role: "user", Content: prompt})
		}
		messages = chatMsgs
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

	body, err := json.Marshal(chatRequest{
		Model:      model,
		Messages:   messages,
		MaxTokens:  maxTokens,
		Tools:      tools,
		ToolChoice: toolChoice,
	})
	if err != nil {
		return "", Usage{}, fmt.Errorf("marshal failed: %w", err)
	}

	var (
		resp  *http.Response
		raw   []byte
		doErr error
	)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(retryDelays[attempt-1]):
			case <-ctx.Done():
				return "", Usage{}, ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", Usage{}, fmt.Errorf("create request failed: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, doErr = c.httpClient.Do(req)
		if doErr != nil {
			if !isRetryable(doErr, 0) {
				return "", Usage{}, fmt.Errorf("request failed: %w", doErr)
			}
			continue
		}

		raw, doErr = io.ReadAll(resp.Body)
		resp.Body.Close()
		if doErr != nil {
			return "", Usage{}, fmt.Errorf("read response failed: %w", doErr)
		}
		doErr = nil

		if !isRetryable(nil, resp.StatusCode) {
			break
		}
	}
	if doErr != nil {
		return "", Usage{}, fmt.Errorf("request failed: %w", doErr)
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
	// tool_calls 응답이면 JSON 문자열로 반환 — Claude가 파싱 후 실제 툴 실행 및 재호출
	if len(result.Choices[0].Message.ToolCalls) > 0 {
		toolCallsJSON, err := json.Marshal(result.Choices[0].Message.ToolCalls)
		if err != nil {
			return "", Usage{}, fmt.Errorf("marshal tool_calls: %w", err)
		}
		return string(toolCallsJSON), result.Usage, nil
	}

	content := result.Choices[0].Message.Content
	filterThinking := c.FilterThinking
	if in.FilterThinking != nil {
		filterThinking = *in.FilterThinking
	}
	if filterThinking {
		content = strings.TrimSpace(thinkingRe.ReplaceAllString(content, ""))
	}

	if outputFile != "" {
		summary, err := writeOutput(outputFile, content, in.Append)
		if err != nil {
			return "", Usage{}, err
		}
		return summary, result.Usage, nil
	}

	return content, result.Usage, nil
}
