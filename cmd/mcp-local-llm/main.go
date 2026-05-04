package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"mcp-local-llm/internal/llm"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

const (
	defaultBaseURL = "http://localhost:8000"
	defaultModel   = "mlx-community/gemma-4-26b-a4b-it-4bit"
	saverInterval  = 30 * time.Second
)

type llmUsage = llm.Usage

type usageStats struct {
	mu               sync.Mutex
	dirty            bool
	TotalCalls       int `json:"total_calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func usageFilePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "usage.json"), nil
}

func loadUsage(path string) *usageStats {
	s := &usageStats{}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	return s
}

func (s *usageStats) add(u llmUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalCalls++
	s.PromptTokens += u.PromptTokens
	s.CompletionTokens += u.CompletionTokens
	s.TotalTokens += u.TotalTokens
	s.dirty = true
}

func (s *usageStats) save(path string) error {
	s.mu.Lock()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write usage tmp: %w", err)
	}
	return os.Rename(tmp, path)
}

func (s *usageStats) flushIfDirty(path string) {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return
	}
	s.dirty = false
	s.mu.Unlock()
	if err := s.save(path); err != nil {
		log.Printf("usage save: %v", err)
	}
}

func (s *usageStats) snapshot() (calls, prompt, completion, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.TotalCalls, s.PromptTokens, s.CompletionTokens, s.TotalTokens
}

func startSaver(stats *usageStats, path string, interval time.Duration) (stop func()) {
	quit := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				stats.flushIfDirty(path)
			case <-quit:
				stats.flushIfDirty(path)
				return
			}
		}
	}()
	return func() { once.Do(func() { close(quit) }) }
}

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

	maxTokens := 0
	if v := os.Getenv("LOCAL_LLM_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTokens = n
		}
	}

	var timeout time.Duration
	if v := os.Getenv("LOCAL_LLM_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}

	maxConcurrent := 0
	if v := os.Getenv("LOCAL_LLM_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrent = n
		}
	}

	filterThinking := envOr("LOCAL_LLM_FILTER_THINKING", "true") != "false"

	maxImages := 5
	if v := os.Getenv("LOCAL_LLM_MAX_IMAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxImages = n
		}
	}

	usagePath, err := usageFilePath()
	if err != nil {
		log.Fatalf("resolve usage file path: %v", err)
	}
	stats := loadUsage(usagePath)

	maxInputBytes := 98304
	if v := os.Getenv("LOCAL_LLM_MAX_INPUT_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxInputBytes = n
		}
	}

	workDir := filepath.Dir(usagePath)
	if v := os.Getenv("LOCAL_LLM_WORK_DIR"); v != "" {
		workDir = v
	}

	client := llm.NewClient(baseURL, model, maxTokens, timeout, maxConcurrent, filterThinking, maxImages, maxInputBytes, workDir)

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "local-llm",
		Version: version,
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "call_local_llm",
		Description: "ALWAYS use this instead of generating text yourself for: summaries, " +
			"translations, PR/commit/issue drafts, release notes, documentation, " +
			"and any response over ~70 tokens. Cheaper than Claude. " +
			"Supports images (file path, https:// URL, data:image/ URI).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in *llm.Input) (*mcp.CallToolResult, any, error) {
		if in.Prompt == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "prompt is required"}},
			}, nil, nil
		}

		text, usage, err := client.Call(ctx, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil, nil
		}
		stats.add(usage)
		result := text + fmt.Sprintf("\n\n[usage: prompt=%d, completion=%d, total=%d]",
			usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_llm_usage",
		Description: "로컬 LLM 누적 사용량을 반환합니다 (총 호출 수, 프롬프트/완성/전체 토큰).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ *struct{}) (*mcp.CallToolResult, any, error) {
		calls, prompt, completion, total := stats.snapshot()
		text := fmt.Sprintf("total_calls=%d, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d",
			calls, prompt, completion, total)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})

	stop := startSaver(stats, usagePath, saverInterval)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ctx := context.Background()
	session, err := s.Connect(ctx, &mcp.StdioTransport{}, nil)
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- session.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			log.Printf("session error: %v", err)
		}
	case <-sigCh:
	}
	stop()
}
