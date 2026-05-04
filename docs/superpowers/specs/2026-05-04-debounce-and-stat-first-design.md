# §1 Debounce Save + §5 Stat-First — Design Spec

**Date:** 2026-05-04
**Scope:** `cmd/mcp-local-llm/main.go` (§1), `internal/llm/client.go` (§5)

---

## §5 — buildPromptWithFiles: stat-first (simpler, covered first)

### Problem
`buildPromptWithFiles` calls `os.ReadFile` for every file before checking the total size limit. A single large file is fully read into memory before being rejected.

### Design

Replace the current single-pass read-then-check with a 2-pass approach:

**Pass 1 — stat only:**
```go
for i, path := range files {
    info, err := os.Stat(path)
    if err != nil {
        if os.IsNotExist(err) { return "", fmt.Errorf("input_files[%d]: file not found: %s", i, path) }
        return "", fmt.Errorf("input_files[%d]: stat failed: %w", i, err)
    }
    total += info.Size()
    if maxBytes > 0 && total > int64(maxBytes) {
        return "", fmt.Errorf("input_files total size (%d bytes) exceeds limit (%d bytes). "+
            "Reduce file count or set LOCAL_LLM_MAX_INPUT_BYTES to increase limit.", total, maxBytes)
    }
}
```

**Pass 2 — read all (only if pass 1 passes):**
```go
for i, path := range files {
    data, err := os.ReadFile(path)
    // permission check, etc.
}
```

### Error message compatibility
- `"file not found"` error text preserved (existing tests pass unchanged)
- `"exceeds limit"` error text preserved (existing tests pass unchanged)
- Adds `"stat failed"` for new stat error path (not currently tested)

---

## §1 — usage.json Debounced Save + Graceful Shutdown

### Problem
`stats.save(usagePath)` is called synchronously on every LLM call with error ignored (`_ =`). Under concurrent calls, the mutex is released before file I/O, risking a race. Even without concurrency, blocking the response path on disk I/O is unnecessary.

### Design

#### `usageStats` changes
Add a `dirty` flag (mutex-protected):

```go
type usageStats struct {
    mu               sync.Mutex
    dirty            bool
    TotalCalls       int `json:"total_calls"`
    PromptTokens     int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    TotalTokens      int `json:"total_tokens"`
}
```

`add()` sets `dirty = true`. `save()` clears `dirty = false` after successful write (inside a separate lock).

#### `startSaver` function
```go
func startSaver(stats *usageStats, path string, interval time.Duration) (stop func()) {
    quit := make(chan struct{})
    go func() {
        t := time.NewTicker(interval)
        defer t.Stop()
        for {
            select {
            case <-t.C:
                stats.flushIfDirty(path)
            case <-quit:
                stats.flushIfDirty(path) // final flush on stop
                return
            }
        }
    }()
    var once sync.Once
    return func() { once.Do(func() { close(quit) }) }
}
```

`flushIfDirty` checks `dirty` under lock and calls `save()` only when true.

#### `main()` changes
1. Call `stop := startSaver(stats, usagePath, 30*time.Second)` after loading stats
2. Register `signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)`
3. Run `session.Wait()` in a goroutine; block with select on session done or signal:
```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
done := make(chan error, 1)
go func() { done <- session.Wait() }()
select {
case err := <-done:
    if err != nil { log.Printf("session error: %v", err) }
case <-sigCh:
}
stop()
```
4. **Remove** `_ = stats.save(usagePath)` from the tool handler hot path

#### Shutdown flow
```
SIGINT/SIGTERM or session.Wait() returns
    → select exits
    → stop() called (sync.Once protected)
    → saver goroutine receives quit signal
    → flushIfDirty() runs final save
    → main() exits
```

### Constants
```go
const saverInterval = 30 * time.Second
```

### Error handling
`flushIfDirty` logs save errors via `log.Printf` (not fatal). SIGKILL cannot be caught — up to 30s of counts may be lost; acceptable given usage tracking is non-critical.

---

## Testing

### §5
- Existing tests pass unchanged (error message text preserved)
- Add: large file (>limit) fails without reading content — verified by checking that `os.ReadFile` is not called (use a file that would panic if read, or check via timing/size)
- Add: multiple files where only the sum exceeds limit → stat-phase rejection

### §1
- `startSaver` unit test: mock `flushIfDirty`, verify called on tick and on stop
- `stop()` flushes immediately: call stop, verify dirty stats were saved
- Integration: `add()` sets dirty, tick fires, file written with correct counts

## Out of scope
- SIGKILL protection (not catchable)
- Configurable save interval (hardcoded 30s constant)
- Per-call save removed entirely (no fallback)
