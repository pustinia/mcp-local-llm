# §1 Debounce Save + §5 Stat-First Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove synchronous I/O from the LLM hot path (§1 debounce) and avoid reading large files before size-limit rejection (§5 stat-first).

**Architecture:** §5 replaces `buildPromptWithFiles`'s single read-then-check loop with a 2-pass stat-first approach. §1 adds a `dirty` flag to `usageStats`, a `flushIfDirty` method, a `startSaver` ticker goroutine, and graceful shutdown via `signal.Notify` in `main()`. Both changes are independent and committed separately.

**Tech Stack:** Go stdlib — `os`, `sync`, `time`, `os/signal`, `syscall`

---

## File Map

| File | Change |
|------|--------|
| `internal/llm/client.go:177-218` | Replace `buildPromptWithFiles` with 2-pass implementation |
| `internal/llm/client_test.go` | Add stat-first tests |
| `cmd/mcp-local-llm/main.go` | Add `dirty` field, `flushIfDirty`, `startSaver`, update `main()` |
| `cmd/mcp-local-llm/main_test.go` | New — unit tests for `flushIfDirty` and `startSaver` |

---

### Task 1: GitHub issue + feature branch

**Files:** none

- [ ] **Step 1: Create GitHub issue**

```bash
gh issue create \
  --title "feat: debounce usage.json save and stat-first file size check" \
  --body "$(cat <<'EOF'
## §1 — Debounce usage.json save

`stats.save()` is called synchronously on every LLM call with error ignored. Add a 30s debounce goroutine, dirty flag, and SIGINT/SIGTERM graceful flush instead.

## §5 — stat-first in buildPromptWithFiles

`buildPromptWithFiles` reads every file fully before checking the size limit. Add an os.Stat pass first — reject immediately if the total size exceeds the limit, then read only on pass.
EOF
)"
```

Note the issue number (e.g. `#22`).

- [ ] **Step 2: Create feature branch**

Replace `{N}` with the actual issue number:

```bash
git checkout develop
git pull origin develop
git checkout -b feat/issue-{N}-debounce-and-stat-first
```

---

### Task 2: §5 failing tests

**Files:**
- Modify: `internal/llm/client_test.go`

- [ ] **Step 1: Add failing tests for stat-first behavior**

Append to `internal/llm/client_test.go`:

```go
func TestBuildPromptWithFiles_StatFirstRejectsBeforeRead(t *testing.T) {
	dir := t.TempDir()

	// Write a file large enough to exceed limit on its own
	large := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(large, []byte(strings.Repeat("x", 200)), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := buildPromptWithFiles("prompt", []string{large}, 50)
	if err == nil {
		t.Fatal("expected error when single file exceeds limit")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("expected 'exceeds limit', got: %v", err)
	}
}

func TestBuildPromptWithFiles_StatFirstSumExceedsLimit(t *testing.T) {
	dir := t.TempDir()

	f1 := filepath.Join(dir, "a.txt")
	f2 := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(f1, []byte(strings.Repeat("a", 60)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte(strings.Repeat("b", 60)), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := buildPromptWithFiles("prompt", []string{f1, f2}, 100)
	if err == nil {
		t.Fatal("expected error when combined size exceeds limit")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("expected 'exceeds limit', got: %v", err)
	}
}
```

- [ ] **Step 2: Run tests — confirm they pass (stat-first doesn't change observable behavior for these cases)**

```bash
cd /Volumes/dev-disk/dev-workspace/mcp-local-llm
go test ./internal/llm/ -run TestBuildPromptWithFiles_Stat -v
```

Expected: PASS — these tests verify the *result*, not the mechanism. They pass with the current code too and will keep passing after the refactor.

---

### Task 3: §5 implementation — stat-first in buildPromptWithFiles

**Files:**
- Modify: `internal/llm/client.go:177-218`

- [ ] **Step 1: Replace buildPromptWithFiles with 2-pass version**

Current function at lines 177–218:

```go
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
```

Replace with:

```go
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
```

- [ ] **Step 2: Run all client tests**

```bash
go test ./internal/llm/ -v
```

Expected: all PASS (error message text preserved — existing tests check `"file not found"`, `"exceeds limit"` which are unchanged).

- [ ] **Step 3: Commit**

```bash
git add internal/llm/client.go internal/llm/client_test.go
git commit -m "feat: stat-first size check in buildPromptWithFiles"
```

---

### Task 4: §1 failing tests — flushIfDirty and startSaver

**Files:**
- Create: `cmd/mcp-local-llm/main_test.go`

- [ ] **Step 1: Create test file**

Create `cmd/mcp-local-llm/main_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFlushIfDirty_WritesWhenDirty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	s := &usageStats{}
	s.add(llmUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})

	s.flushIfDirty(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file to be written: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty usage file")
	}
}

func TestFlushIfDirty_SkipsWhenClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	s := &usageStats{}
	// do NOT call add() — dirty stays false

	s.flushIfDirty(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no file written when not dirty")
	}
}

func TestFlushIfDirty_ClearsDirtyAfterWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	s := &usageStats{}
	s.add(llmUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2})

	s.flushIfDirty(path)
	// dirty should now be false — second flush should not write again
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.flushIfDirty(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no second write after dirty cleared")
	}
}

func TestStartSaver_FlushesOnStop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	s := &usageStats{}
	s.add(llmUsage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5})

	stop := startSaver(s, path, 10*time.Minute) // long interval — flush only on stop
	stop()
	time.Sleep(20 * time.Millisecond) // allow goroutine to complete

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file written on stop: %v", err)
	}
}

func TestStartSaver_FlushesOnTick(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	s := &usageStats{}
	s.add(llmUsage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10})

	stop := startSaver(s, path, 10*time.Millisecond)
	defer stop()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return // file written — test passes
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("expected file to be written within 500ms on tick")
}

func TestStartSaver_StopIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	s := &usageStats{}
	stop := startSaver(s, path, time.Minute)

	// calling stop twice must not panic
	stop()
	stop()
}
```

Note: `llmUsage` is a type alias for `llm.Usage` that we'll define in Task 5. For now the tests won't compile.

- [ ] **Step 2: Run tests — confirm they fail to compile**

```bash
go test ./cmd/mcp-local-llm/ -v 2>&1 | head -20
```

Expected: build failure — `flushIfDirty`, `startSaver`, `llmUsage` undefined.

---

### Task 5: §1 implementation — dirty flag + flushIfDirty

**Files:**
- Modify: `cmd/mcp-local-llm/main.go`

- [ ] **Step 1: Add `dirty` field to `usageStats` and define `llmUsage` type alias**

Current `usageStats` struct (lines 29–35):

```go
type usageStats struct {
	mu               sync.Mutex
	TotalCalls       int `json:"total_calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
```

Replace with:

```go
type llmUsage = llm.Usage

type usageStats struct {
	mu               sync.Mutex
	dirty            bool
	TotalCalls       int `json:"total_calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
```

- [ ] **Step 2: Update `add()` to set dirty=true**

Current `add()` method (lines 55–62):

```go
func (s *usageStats) add(u llm.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalCalls++
	s.PromptTokens += u.PromptTokens
	s.CompletionTokens += u.CompletionTokens
	s.TotalTokens += u.TotalTokens
}
```

Replace with:

```go
func (s *usageStats) add(u llmUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalCalls++
	s.PromptTokens += u.PromptTokens
	s.CompletionTokens += u.CompletionTokens
	s.TotalTokens += u.TotalTokens
	s.dirty = true
}
```

- [ ] **Step 3: Add `flushIfDirty` method after `save()`**

After the existing `save()` method (currently ending around line 76), add:

```go
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
```

- [ ] **Step 4: Run tests — flushIfDirty tests should pass, startSaver still fails**

```bash
go test ./cmd/mcp-local-llm/ -run "TestFlushIfDirty" -v
```

Expected: all 3 `TestFlushIfDirty_*` tests PASS.

---

### Task 6: §1 implementation — startSaver

**Files:**
- Modify: `cmd/mcp-local-llm/main.go`

- [ ] **Step 1: Add `saverInterval` constant and `startSaver` function**

Add `"sync"` is already imported. Add `"os/signal"` and `"syscall"` to the import block:

Current imports:
```go
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"mcp-local-llm/internal/llm"
)
```

Replace with:

```go
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
```

- [ ] **Step 2: Add `saverInterval` constant after the existing constants block**

After `const ( defaultBaseURL = ... defaultModel = ... )`, add:

```go
const saverInterval = 30 * time.Second
```

- [ ] **Step 3: Add `startSaver` function**

Add after `flushIfDirty`:

```go
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
```

- [ ] **Step 4: Run startSaver tests**

```bash
go test ./cmd/mcp-local-llm/ -run "TestStartSaver" -v
```

Expected: all 3 `TestStartSaver_*` tests PASS.

---

### Task 7: §1 implementation — update main()

**Files:**
- Modify: `cmd/mcp-local-llm/main.go`

- [ ] **Step 1: Remove hot-path save and add startSaver + signal handling**

Current end of `main()` (lines 172–208, the tool handler and session block):

In the `call_local_llm` tool handler, find and remove:
```go
		stats.add(usage)
		_ = stats.save(usagePath)
```

Replace with:
```go
		stats.add(usage)
```

- [ ] **Step 2: Replace session.Wait() block with select-based shutdown**

Current session block (last ~8 lines of main):

```go
	ctx := context.Background()
	session, err := s.Connect(ctx, &mcp.StdioTransport{}, nil)
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}
	if err := session.Wait(); err != nil {
		log.Fatalf("session error: %v", err)
	}
```

Replace with:

```go
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
```

- [ ] **Step 3: Build and run all tests**

```bash
go test ./... -v 2>&1 | tail -20
make build
```

Expected: all tests PASS, binary builds cleanly.

- [ ] **Step 4: Commit**

```bash
git add cmd/mcp-local-llm/main.go cmd/mcp-local-llm/main_test.go
git commit -m "feat: debounce usage.json save with 30s ticker and graceful shutdown"
```

---

### Task 8: PR

**Files:** none

- [ ] **Step 1: Push branch and open PR**

Replace `{N}` with the actual issue number:

```bash
git push -u origin feat/issue-{N}-debounce-and-stat-first
gh pr create \
  --title "feat: debounce usage save and stat-first file size check (issue #{N})" \
  --body "$(cat <<'EOF'
## Summary

### §5 — stat-first in buildPromptWithFiles
- Replace single read-then-check loop with 2-pass: `os.Stat` size sum first, `os.ReadFile` only if within limit
- Avoids reading large files that will be rejected anyway
- Error messages unchanged (existing tests unaffected)

### §1 — Debounce usage.json save
- Add `dirty` flag to `usageStats`; `add()` sets it, `flushIfDirty()` clears after write
- `startSaver` ticker goroutine flushes every 30s when dirty; `sync.Once`-protected stop
- `main()` registers `SIGINT`/`SIGTERM`; either signal or session end triggers `stop()` → final flush
- Remove synchronous `stats.save()` from LLM call hot path

Closes #{N}

## Test plan
- [x] `TestBuildPromptWithFiles_StatFirst*` — stat-first size rejection
- [x] `TestFlushIfDirty_*` — dirty flag write/skip/clear behavior
- [x] `TestStartSaver_FlushesOnStop` — final flush on stop()
- [x] `TestStartSaver_FlushesOnTick` — periodic save on ticker
- [x] `TestStartSaver_StopIdempotent` — double stop() no panic
EOF
)"
```
