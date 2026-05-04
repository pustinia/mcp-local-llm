# Retry on Transient Errors — Design Spec

**Date:** 2026-05-04
**Scope:** `internal/llm/client.go` — `Call()` only

## Problem

`Call()` returns immediately on any HTTP error or non-200 status. When vllm-mlx is momentarily unstable (5xx, connection refused, temporary network error), the MCP caller sees a failure with no retry opportunity.

## Design

### Constants (hardcoded)

```go
const (
    maxRetries  = 2
    retryDelay0 = 200 * time.Millisecond
    retryDelay1 = 1 * time.Second
)

var retryDelays = [maxRetries]time.Duration{retryDelay0, retryDelay1}
```

### Retry condition

```go
func isRetryable(err error, statusCode int) bool
```

Returns true when:
- `err != nil` and (`net.Error` with `Temporary()==true` OR error string contains `"connection refused"`)
- `statusCode >= 500`

Non-retryable: 4xx client errors, context cancellation, non-network errors.

### Call() change

Only the HTTP dispatch segment is wrapped in a retry loop (semaphore acquire, path validation, and prompt building remain outside):

```
for attempt := 0; attempt <= maxRetries; attempt++:
    if attempt > 0:
        select { case <-time.After(retryDelays[attempt-1]): case <-ctx.Done(): return ctx.Err() }
    do HTTP request → parse response
    if !isRetryable(err, statusCode): break
return last error
```

### Error message

On exhausted retries, return the last error as-is (no wrapping with retry count — keeps error messages clean for MCP callers).

## Testing

- `isRetryable()` unit tests: 5xx → true, 4xx → false, `net.Error{Temporary:true}` → true, `context.Canceled` → false
- `Call()` retry integration: mock server returning 503 twice then 200 → success; mock returning 503 three times → error

## Out of scope

- Configurable retry count / delay (hardcoded constants)
- Retry on POST idempotency concerns (prompts are non-deterministic by nature; retry is acceptable)
- README / env var changes (no new configuration surface)
