package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Input struct {
	Prompt    string `json:"prompt"               jsonschema:"LLM에 전달할 사용자 메시지,required"`
	System    string `json:"system,omitempty"     jsonschema:"시스템 프롬프트 (선택사항)"`
	Model     string `json:"model,omitempty"      jsonschema:"사용할 모델 이름 (기본값: gemma-4-26b-a4b-it-4bit)"`
	MaxTokens int    `json:"max_tokens,omitempty" jsonschema:"최대 출력 토큰 수 (기본값: 1000)"`
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

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type Client struct {
	BaseURL          string
	DefaultModel     string
	DefaultMaxTokens int
}

func (c *Client) Call(in *Input) (string, error) {
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

	body, _ := json.Marshal(chatRequest{Model: model, Messages: messages, MaxTokens: maxTokens})
	resp, err := http.Post(c.BaseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result chatResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse failed: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return result.Choices[0].Message.Content, nil
}
