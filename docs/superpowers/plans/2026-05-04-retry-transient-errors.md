# Retry on Transient Errors — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add automatic retry (max 2 attempts, 200ms/1s backoff) for 5xx and transient network errors in `Call()`.

**Architecture:** Extract `isRetryable(err, statusCode)` helper, then wrap only the HTTP dispatch section of `Call()` in a retry loop. All upstream logic (semaphore, validation, prompt building) stays outside the loop. Request body is re-created each attempt from the already-marshaled `body` byte slice.

**Tech Stack:** Go standard library — `net`, `errors`, `time`

---

### Task 1: GitHub issue + feature branch

**Files:**
- No code files

- [ ] **Step 1: Create GitHub issue**

```bash
gh issue create \
  --title "feat: retry on transient errors in Call()" \
  --body "$(cat <<'EOF'
## Motivation

`Call()` returns immediately on 5xx or temporary network errors (connection refused, temporary net.Error). When vllm-mlx is momentarily unstable, the MCP caller sees a hard failure with no retry opportunity.

## Implementation

- Add `isRetryable(err error, statusCode int) bool` helper
- Wrap HTTP dispatch in `Call()` with a retry loop: max 2 retries, delays [200ms, 1s]
- Retry on: HTTP 5xx, `net.Error.Temporary()==true`, `"connection refused"` in error string
- Non-retryable: 4xx, context cancellation, other errors
- No new env vars — constants only
EOF
)"
```

Note the issue number printed (e.g. `#21`).

- [ ] **Step 2: Create feature branch**

Replace `{N}` with the actual issue number:

```bash
git checkout develop
git pull origin develop
git checkout -b feat/issue-{N}-retry-transient-errors
```

---

### Task 2: `isRetryable` — failing tests first

**Files:**
- Modify: `internal/llm/client_test.go`

- [ ] **Step 1: Add failing tests for `isRetryable`**

Append to `internal/llm/client_test.go`:

```go
func TestIsRetryable_5xx(t *testing.T) {
	for _, code := range []int{500, 502, 503, 504} {
		if !isRetryable(nil, code) {
			t.Errorf("expected 5xx %d to be retryable", code)
		}
	}
}

func TestIsRetryable_NonRetryableStatus(t *testing.T) {
	for _, code := range []int{0, 200, 400, 404, 422} {
		if isRetryable(nil, code) {
			t.Errorf("expected status %d to be non-retryable", code)
		}
	}
}

func TestIsRetryable_ConnectionRefused(t *testing.T) {
	err := fmt.Errorf("dial tcp: connection refused")
	if !isRetryable(err, 0) {
		t.Error("expected connection refused error to be retryable")
	}
}

func TestIsRetryable_NilError_ZeroStatus(t *testing.T) {
	if isRetryable(nil, 0) {
		t.Error("nil error with zero status should not be retryable")
	}
}

func TestIsRetryable_NonNetworkError(t *testing.T) {
	err := fmt.Errorf("some other error")
	if isRetryable(err, 0) {
		t.Error("generic non-network error should not be retryable")
	}
}
```

- [ ] **Step 2: Run tests — confirm they fail**

```bash
cd /Volumes/dev-disk/dev-workspace/mcp-local-llm
go test ./internal/llm/ -run TestIsRetryable -v
```

Expected: `FAIL` — `isRetryable` undefined.

---

### Task 3: Implement `isRetryable`

**Files:**
- Modify: `internal/llm/client.go`

- [ ] **Step 1: Add imports `"errors"` and `"net"` to the import block**

Current import block (lines 3–16):
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
	"path/filepath"
	"regexp"
	"strings"
	"time"
)
```

Replace with:
```go
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
```

- [ ] **Step 2: Add constants and `isRetryable` after the `thinkingRe` line (line 18)**

After `var thinkingRe = ...` add:

```go
const maxRetries = 2

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
```

- [ ] **Step 3: Run `isRetryable` tests — confirm they pass**

```bash
go test ./internal/llm/ -run TestIsRetryable -v
```

Expected: all `PASS`.

- [ ] **Step 4: Commit**

```bash
git add internal/llm/client.go internal/llm/client_test.go
git commit -m "feat: add isRetryable helper with tests"
```

---

### Task 4: Retry loop in `Call()` — failing tests first

**Files:**
- Modify: `internal/llm/client_test.go`

- [ ] **Step 1: Add failing integration tests for retry behavior**

Append to `internal/llm/client_test.go`. These require adding `"net/http"`, `"net/http/httptest"`, `"context"`, and `"sync/atomic"` imports — add them to the import block at the top of the test file:

```go
import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)
```

Then append these test functions:

```go
func TestCall_RetriesOn503ThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-model", 0, 0, 0, false, 5, 0, t.TempDir())
	// Zero out retry delays for fast test
	retryDelays = [maxRetries]time.Duration{0, 0}

	text, _, err := c.Call(context.Background(), &Input{Prompt: "hello"})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if text != "ok" {
		t.Errorf("expected 'ok', got %q", text)
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestCall_ExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-model", 0, 0, 0, false, 5, 0, t.TempDir())
	retryDelays = [maxRetries]time.Duration{0, 0}

	_, _, err := c.Call(context.Background(), &Input{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected 503 in error, got: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts (1 + 2 retries), got %d", attempts.Load())
	}
}

func TestCall_NoRetryOn4xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-model", 0, 0, 0, false, 5, 0, t.TempDir())
	retryDelays = [maxRetries]time.Duration{0, 0}

	_, _, err := c.Call(context.Background(), &Input{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if attempts.Load() != 1 {
		t.Errorf("expected exactly 1 attempt for 4xx, got %d", attempts.Load())
	}
}

func TestCall_ContextCancelledDuringRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-model", 0, 0, 0, false, 5, 0, t.TempDir())
	// Use real delays so context cancellation can fire
	retryDelays = [maxRetries]time.Duration{500 * time.Millisecond, 1 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := c.Call(ctx, &Input{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	// Either context error or the 503 from the first attempt
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "503") {
		t.Errorf("expected context or 503 error, got: %v", err)
	}
}
```

- [ ] **Step 2: Restore retryDelays default in test teardown**

Note: The tests mutate the package-level `retryDelays`. Because tests in `package llm` share the same package state, each test that mutates `retryDelays` must restore it. Add a `t.Cleanup` call to each test that changes `retryDelays`:

In `TestCall_RetriesOn503ThenSucceeds`, after setting delays:
```go
orig := retryDelays
retryDelays = [maxRetries]time.Duration{0, 0}
t.Cleanup(func() { retryDelays = orig })
```

Apply the same pattern to `TestCall_ExhaustsRetries` and `TestCall_NoRetryOn4xx`. (The context cancellation test uses real delays so no override needed beyond its own restore.)

- [ ] **Step 3: Run new tests — confirm they fail**

```bash
go test ./internal/llm/ -run TestCall_ -v
```

Expected: `FAIL` — retry behavior does not exist yet.

---

### Task 5: Implement retry loop in `Call()`

**Files:**
- Modify: `internal/llm/client.go`

- [ ] **Step 1: Replace the HTTP dispatch block in `Call()`**

Current block to replace (lines 309–328):

```go
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
```

Replace with:

```go
	var (
		resp    *http.Response
		raw     []byte
		doErr   error
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
```

- [ ] **Step 2: Run all tests**

```bash
go test ./internal/llm/ -v
```

Expected: all `PASS`.

- [ ] **Step 3: Build to verify compilation**

```bash
make build
```

Expected: `bin/mcp-local-llm` created with no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/llm/client.go internal/llm/client_test.go
git commit -m "feat: retry on transient errors in Call() — max 2 retries, 200ms/1s backoff"
```

---

### Task 6: PR

**Files:**
- No code files

- [ ] **Step 1: Push branch and open PR**

Replace `{N}` with the actual issue number:

```bash
git push -u origin feat/issue-{N}-retry-transient-errors
gh pr create \
  --title "feat: retry on transient errors (issue #{N})" \
  --body "$(cat <<'EOF'
## Summary

- Add `isRetryable(err, statusCode)` helper in `internal/llm/client.go`
- Wrap HTTP dispatch in `Call()` with retry loop: max 2 retries, delays [200ms, 1s]
- Retry on HTTP 5xx, `net.Error.Temporary()`, and `"connection refused"` errors
- Non-retryable: 4xx, context cancellation, read errors
- No new configuration surface (hardcoded constants)

Closes #{N}

## Test plan

- [ ] `TestIsRetryable_*` — unit tests for retry condition helper
- [ ] `TestCall_RetriesOn503ThenSucceeds` — 2× 503 then 200 → success
- [ ] `TestCall_ExhaustsRetries` — 3× 503 → error with 503 in message
- [ ] `TestCall_NoRetryOn4xx` — 400 → exactly 1 attempt
- [ ] `TestCall_ContextCancelledDuringRetry` — context timeout during backoff sleep
EOF
)"
```
