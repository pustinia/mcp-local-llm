# Design Spec: `call_local_llm` File I/O Enhancements

**Date:** 2026-05-02
**Status:** Draft

## Background

When Claude passes large file contents via prompt, the file content enters Claude's context window and consumes tokens twice — once when reading, once when the LLM result is returned. For a 10KB file, this means ~5,000 tokens of overhead per call.

Context limit testing on the local MLX-LM server confirmed:
- ≤ 24,000 tokens input: reliable
- ≥ 32,000 tokens input: server crash (no error response)

## Goal

Reduce Claude context token usage by having the MCP server handle file I/O directly. File contents and LLM outputs never pass through Claude's context window.

**Token savings example:**
```
Before: Claude reads file (5,000 tokens) → passes to LLM → result back to Claude (1,000 tokens) = 6,000 tokens
After:  Claude calls tool with file path → MCP server handles I/O → "saved 843 bytes" = ~20 tokens
```

## Design

### New Parameters

Three fields added to `Input` struct in `internal/llm/client.go`:

```go
InputFiles []string `json:"input_files,omitempty" jsonschema:"파일 경로 목록. MCP 서버가 읽어서 프롬프트에 포함 (Claude 컨텍스트 절약)"`
OutputFile string   `json:"output_file,omitempty" jsonschema:"결과를 저장할 파일 경로. 지정 시 저장 완료 메시지만 반환"`
Append     bool     `json:"append,omitempty"      jsonschema:"true이면 output_file에 이어쓰기, false(기본값)이면 덮어쓰기"`
```

### File Reading (input_files)

The MCP server reads each file and prepends content to the prompt using header format:

```
[user prompt]

[file: path/to/a.go]
[file contents...]

[file: path/to/b.md]
[file contents...]
```

Files are inserted in the order provided. The combined text is passed as the user message to the LLM.

### File Writing (output_file)

When `output_file` is set:
- LLM result is written to the specified path
- `append=false` (default): overwrite with `os.WriteFile`
- `append=true`: append with `os.OpenFile(O_APPEND|O_WRONLY|O_CREATE)`
- Return value to Claude: `"summary.md에 843 bytes 저장 완료 [usage: prompt=1240, completion=210, total=1450]"`

When `output_file` is not set: existing behavior unchanged (full text returned).

**No automatic directory creation** — parent directory must exist. Prevents accidental file creation at unexpected paths.

### Pre-validation (crash prevention)

Before calling the LLM, validate total input size:

```go
// Environment variable: LOCAL_LLM_MAX_INPUT_BYTES
// Default: 98304 (96KB ≈ 24,000 tokens — confirmed safe limit)
// If exceeded, return error without calling LLM:
// "input_files total size (120,450 bytes) exceeds limit (98,304 bytes).
//  Reduce file count or set LOCAL_LLM_MAX_INPUT_BYTES to increase limit."
```

### Path Security

The MCP server runs as the OS user and can read/write any path that user can access. To prevent accidental or malicious path traversal, all `input_files` and `output_file` paths are validated against a **work directory** before use.

**Work directory:** defaults to the directory containing the MCP binary (same directory as `usage.json`). Override with `LOCAL_LLM_WORK_DIR`.

**Validation rules (applied to every path before I/O):**
1. Relative paths are resolved against the work directory: `"output.txt"` → `"/work/dir/output.txt"`
2. The resolved path is normalized with `filepath.Clean()` to collapse `../` sequences
3. The normalized path must be equal to or start with `workDir + "/"` — any path that escapes the work directory is rejected

```
"output.txt"                → /work/dir/output.txt          ✅
"subdir/notes.md"           → /work/dir/subdir/notes.md     ✅
"../escape.txt"             → /escape.txt                   ❌ outside work dir
"/etc/passwd"               → /etc/passwd                   ❌ outside work dir
"/work/dir/../etc/passwd"   → /etc/passwd                   ❌ outside work dir (after Clean)
```

This is implemented in a standalone `validatePath(path, workDir string) (string, error)` function in `internal/llm/client.go`. `Call()` validates all paths before passing them to `buildPromptWithFiles` or `writeOutput`.

### Error Handling

| Situation | Error Message |
|-----------|---------------|
| Path outside work dir (input) | `input_files[N]: path "/etc/passwd" is outside work directory "/work/dir"` |
| Path outside work dir (output) | `output_file: path "/etc/passwd" is outside work directory "/work/dir"` |
| File not found | `input_files[N]: file not found: path/to/file` |
| Permission denied (read) | `input_files[N]: permission denied: path/to/file` |
| Total size exceeded | `input_files total size (X bytes) exceeds limit (Y bytes)` |
| Output dir not found | `output_file: open ...: no such file or directory` |
| Permission denied (write) | `output_file: open ...: permission denied` |

All errors returned as `CallToolResult{IsError: true}` per project convention.

## Testing

14 unit tests in `internal/llm/client_test.go`:

**validatePath (5 tests):**
- `TestValidatePath_RelativeResolved` — relative path resolved to workDir
- `TestValidatePath_AbsWithinDir` — absolute path within workDir is allowed
- `TestValidatePath_AbsOutsideDir` — absolute path outside workDir is rejected
- `TestValidatePath_DotDotEscape` — `../` relative escape is rejected
- `TestValidatePath_DotDotAbsolute` — `/../` absolute escape rejected after Clean

**input_files (5 tests):**
- `TestBuildPromptWithFiles_Single` — single file, header format verified
- `TestBuildPromptWithFiles_Multiple` — multiple files, order preserved
- `TestBuildPromptWithFiles_FileNotFound` — error on missing file
- `TestBuildPromptWithFiles_SizeExceeds` — error when total exceeds limit
- `TestBuildPromptWithFiles_Empty` — empty slice behaves same as no input_files

**output_file (4 tests):**
- `TestWriteOutput_NewFile` — new file created with correct content
- `TestWriteOutput_Overwrite` — existing file overwritten (append=false)
- `TestWriteOutput_Append` — content appended to existing file (append=true)
- `TestWriteOutput_DirNotFound` — error when parent directory missing

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LOCAL_LLM_MAX_INPUT_BYTES` | `98304` | Maximum combined size of all input_files in bytes (96KB ≈ 24,000 tokens). Set to 0 for no limit (not recommended — server may crash). |
| `LOCAL_LLM_WORK_DIR` | binary directory | Base directory for all `input_files` and `output_file` paths. All paths must resolve within this directory. Relative paths are resolved against it. |
