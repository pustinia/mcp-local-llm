package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultTimeout = 5 * time.Minute

type Input struct {
	Prompt    string `json:"prompt"               jsonschema:"LLM에 전달할 사용자 메시지,required"`
	System    string `json:"system,omitempty"     jsonschema:"시스템 프롬프트 (선택사항)"`
	Model     string `json:"model,omitempty"      jsonschema:"사용할 모델 이름 (기본값: gemma-4-26b-a4b-it-4bit)"`
	MaxTokens int    `json:"max_tokens,omitempty" jsonschema:"최대 출력 토큰 수 (기본값: 32768)"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
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
	httpClient       *http.Client
	sem              chan struct{} // nil = 무제한
}

func NewClient(baseURL, model string, maxTokens int, timeout time.Duration, maxConcurrent int) *Client {
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
		httpClient:       &http.Client{Timeout: timeout},
		sem:              sem,
	}
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
	messages = append(messages, chatMessage{Role: "user", Content: in.Prompt})

	model := in.Model
	if model == "" {
		model = c.DefaultModel
	}
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.DefaultMaxTokens
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
	return result.Choices[0].Message.Content, result.Usage, nil
}
