package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"mcp-local-llm/internal/llm"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

const (
	defaultBaseURL   = "http://localhost:8000"
	defaultModel     = "mlx-community/gemma-4-26b-a4b-it-4bit"
	defaultMaxTokens = 1000
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mcp-local-llm %s (built %s)\n", version, buildTime)
		os.Exit(0)
	}

	baseURL := envOr("LOCAL_LLM_BASE_URL", defaultBaseURL)
	model := envOr("LOCAL_LLM_MODEL", defaultModel)

	maxTokens := defaultMaxTokens
	if v := os.Getenv("LOCAL_LLM_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTokens = n
		}
	}

	client := &llm.Client{
		BaseURL:          baseURL,
		DefaultModel:     model,
		DefaultMaxTokens: maxTokens,
	}

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "local-llm",
		Version: version,
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "call_local_llm",
		Description: fmt.Sprintf("로컬에서 실행 중인 LLM(%s)을 호출합니다. 간단한 요약, 번역, 코드 생성, 빠른 질문 응답 등에 활용하세요.", model),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in *llm.Input) (*mcp.CallToolResult, any, error) {
		if in.Prompt == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "prompt is required"}},
			}, nil, nil
		}

		text, err := client.Call(in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})

	ctx := context.Background()
	session, err := s.Connect(ctx, &mcp.StdioTransport{}, nil)
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}
	if err := session.Wait(); err != nil {
		log.Fatalf("session error: %v", err)
	}
}
