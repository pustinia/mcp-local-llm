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

type Input struct {
	Prompt         string   `json:"prompt"                    jsonschema:"LLM에 전달할 사용자 메시지,required"`
	System         string   `json:"system,omitempty"          jsonschema:"시스템 프롬프트 (선택사항)"`
	Model          string   `json:"model,omitempty"           jsonschema:"사용할 모델 이름 (기본값: gemma-4-26b-a4b-it-4bit)"`
	MaxTokens      int      `json:"max_tokens,omitempty"      jsonschema:"최대 출력 토큰 수 (생략 시 서버 기본값 사용)"`
	FilterThinking *bool    `json:"filter_thinking,omitempty" jsonschema:"thinking 블록 필터링 여부 (true=필터, false=유지). 생략 시 서버 기본값(LOCAL_LLM_FILTER_THINKING) 적용"`
	Images         []string `json:"images,omitempty"          jsonschema:"이미지 목록 (파일 경로·URL·base64 data URI 혼합 가능)"`
	InputFiles     []string `json:"input_files,omitempty"     jsonschema:"읽어서 프롬프트에 포함할 파일 경로 목록. MCP 서버가 직접 읽어 Claude 컨텍스트 절약"`
	OutputFile     string   `json:"output_file,omitempty"     jsonschema:"결과를 저장할 파일 경로. 지정 시 저장 완료 메시지만 반환 (Claude 컨텍스트 절약)"`
	Append         bool     `json:"append,omitempty"          jsonschema:"true이면 output_file에 이어쓰기. 기본값 false (덮어쓰기)"`
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
	type fileData struct {
		path    string
		content []byte
	}
	var total int
	loaded := make([]fileData, 0, len(files))
	for i, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("input_files[%d]: file not found: %s", i, path)
			}
			if os.IsPermission(err) {
				return "", fmt.Errorf("input_files[%d]: permission denied: %s", i, path)
			}
			return "", fmt.Errorf("input_files[%d]: read failed: %w", i, err)
		}
		total += len(data)
		if maxBytes > 0 && total > maxBytes {
			return "", fmt.Errorf("input_files total size (%d bytes) exceeds limit (%d bytes). "+
				"Reduce file count or set LOCAL_LLM_MAX_INPUT_BYTES to increase limit.", total, maxBytes)
		}
		loaded = append(loaded, fileData{path: path, content: data})
	}
	var sb strings.Builder
	sb.WriteString(prompt)
	for _, f := range loaded {
		sb.WriteString("\n\n[file: ")
		sb.WriteString(f.path)
		sb.WriteString("]\n")
		sb.Write(f.content)
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

	// Validate and resolve input file paths against work directory
	inputFiles := make([]string, len(in.InputFiles))
	for i, f := range in.InputFiles {
		p, err := validatePath(f, c.WorkDir)
		if err != nil {
			return "", Usage{}, fmt.Errorf("input_files[%d]: %w", i, err)
		}
		inputFiles[i] = p
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

	prompt := in.Prompt
	if len(inputFiles) > 0 {
		var err error
		prompt, err = buildPromptWithFiles(in.Prompt, inputFiles, c.MaxInputBytes)
		if err != nil {
			return "", Usage{}, err
		}
	}

	messages := []chatMessage{}
	if in.System != "" {
		messages = append(messages, chatMessage{Role: "system", Content: in.System})
	}
	if len(in.Images) > 0 {
		parts, err := buildImageContent(prompt, in.Images, c.MaxImages)
		if err != nil {
			return "", Usage{}, err
		}
		messages = append(messages, chatMessage{Role: "user", Content: parts})
	} else {
		messages = append(messages, chatMessage{Role: "user", Content: prompt})
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

	if outputFile != "" {
		summary, err := writeOutput(outputFile, content, in.Append)
		if err != nil {
			return "", Usage{}, err
		}
		return summary, result.Usage, nil
	}

	return content, result.Usage, nil
}
