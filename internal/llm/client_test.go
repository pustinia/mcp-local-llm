package llm

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildImageContent_URL(t *testing.T) {
	parts, err := buildImageContent("describe this", []string{"https://example.com/photo.jpg"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "describe this" {
		t.Errorf("text part: got %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "https://example.com/photo.jpg" {
		t.Errorf("image part: got %+v", parts[1])
	}
}

func TestBuildImageContent_DataURI(t *testing.T) {
	dataURI := "data:image/jpeg;base64,/9j/4AAQSkZJRgAB"
	parts, err := buildImageContent("hello", []string{dataURI}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != dataURI {
		t.Errorf("image part: got %+v", parts[1])
	}
}

func TestBuildImageContent_FilePath(t *testing.T) {
	f, err := os.CreateTemp("", "test-image-*.png")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if _, err := f.Write(pngHeader); err != nil {
		t.Fatal(err)
	}
	f.Close()

	parts, err := buildImageContent("hello", []string{f.Name()}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader)
	if parts[1].ImageURL.URL != expected {
		t.Errorf("expected %s, got %s", expected, parts[1].ImageURL.URL)
	}
}

func TestBuildImageContent_TextPartIsFirst(t *testing.T) {
	parts, err := buildImageContent("my prompt", []string{"https://example.com/img.jpg"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts[0].Type != "text" || parts[0].Text != "my prompt" {
		t.Errorf("first part should be text with prompt, got %+v", parts[0])
	}
}

func TestBuildImageContent_MultipleImages(t *testing.T) {
	images := []string{
		"https://example.com/1.jpg",
		"https://example.com/2.jpg",
	}
	parts, err := buildImageContent("compare", images, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 3 {
		t.Errorf("expected 3 parts (1 text + 2 images), got %d", len(parts))
	}
	if parts[1].ImageURL.URL != images[0] || parts[2].ImageURL.URL != images[1] {
		t.Errorf("image order wrong: got %+v %+v", parts[1], parts[2])
	}
}

func TestBuildImageContent_ExactLimit(t *testing.T) {
	images := []string{
		"https://example.com/1.jpg",
		"https://example.com/2.jpg",
	}
	parts, err := buildImageContent("hello", images, 2)
	if err != nil {
		t.Fatalf("unexpected error at exact limit: %v", err)
	}
	if len(parts) != 3 {
		t.Errorf("expected 3 parts, got %d", len(parts))
	}
}

func TestBuildImageContent_ExceedsLimit(t *testing.T) {
	images := []string{
		"https://example.com/1.jpg",
		"https://example.com/2.jpg",
	}
	_, err := buildImageContent("hello", images, 1)
	if err == nil {
		t.Fatal("expected error for exceeding limit")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildImageContent_DisabledWithZero(t *testing.T) {
	_, err := buildImageContent("hello", []string{"https://example.com/img.jpg"}, 0)
	if err == nil {
		t.Fatal("expected error when images disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildImageContent_NegativeMaxImages(t *testing.T) {
	parts, err := buildImageContent("hello", []string{"https://example.com/img.jpg"}, -1)
	if err != nil {
		t.Fatalf("negative maxImages should be treated as unlimited, got error: %v", err)
	}
	if len(parts) != 2 {
		t.Errorf("expected 2 parts, got %d", len(parts))
	}
}

func TestBuildImageContent_FileNotFound(t *testing.T) {
	_, err := buildImageContent("hello", []string{"/nonexistent/path/image.png"}, 5)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidatePath_RelativeResolved(t *testing.T) {
	dir := t.TempDir()
	result, err := validatePath("output.txt", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(dir, "output.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestValidatePath_AbsWithinDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "file.txt")
	result, err := validatePath(path, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != path {
		t.Errorf("expected %q, got %q", path, result)
	}
}

func TestValidatePath_AbsOutsideDir(t *testing.T) {
	dir := t.TempDir()
	_, err := validatePath("/etc/passwd", dir)
	if err == nil {
		t.Fatal("expected error for path outside work dir")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("expected error to mention 'outside', got: %v", err)
	}
}

func TestValidatePath_DotDotEscape(t *testing.T) {
	dir := t.TempDir()
	_, err := validatePath("../escape.txt", dir)
	if err == nil {
		t.Fatal("expected error for dot-dot escape")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("expected error to mention 'outside', got: %v", err)
	}
}

func TestValidatePath_DotDotAbsolute(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/../escape.txt"
	_, err := validatePath(path, dir)
	if err == nil {
		t.Fatal("expected error for absolute path that escapes via ../")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Errorf("expected error to mention 'outside', got: %v", err)
	}
}

func TestValidatePath_ExactWorkDir(t *testing.T) {
	dir := t.TempDir()
	result, err := validatePath(dir, dir)
	if err != nil {
		t.Fatalf("work dir root should be valid: %v", err)
	}
	if result != filepath.Clean(dir) {
		t.Errorf("expected %q, got %q", filepath.Clean(dir), result)
	}
}

func TestBuildPromptWithFiles_Single(t *testing.T) {
	f, err := os.CreateTemp("", "llm-test-*.go")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	content := "package main\n\nfunc main() {}"
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	result, err := buildPromptWithFiles("summarize this", []string{f.Name()}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "summarize this\n\n[file: " + f.Name() + "]\n" + content + "\n"
	if result != expected {
		t.Errorf("expected:\n%q\ngot:\n%q", expected, result)
	}
}

func TestBuildPromptWithFiles_Multiple(t *testing.T) {
	f1, err := os.CreateTemp("", "llm-test-a-*.go")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f1.Name())
	f2, err := os.CreateTemp("", "llm-test-b-*.md")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f2.Name())
	if _, err := f1.WriteString("content A"); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.WriteString("content B"); err != nil {
		t.Fatal(err)
	}
	f1.Close()
	f2.Close()

	result, err := buildPromptWithFiles("my prompt", []string{f1.Name(), f2.Name()}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[file: "+f1.Name()+"]") {
		t.Errorf("missing header for file1 in: %s", result)
	}
	if !strings.Contains(result, "[file: "+f2.Name()+"]") {
		t.Errorf("missing header for file2 in: %s", result)
	}
	idx1 := strings.Index(result, "[file: "+f1.Name()+"]")
	idx2 := strings.Index(result, "[file: "+f2.Name()+"]")
	if idx1 < 0 || idx2 < 0 || idx1 > idx2 {
		t.Errorf("file1 header should appear before file2 header in result")
	}
}

func TestBuildPromptWithFiles_FileNotFound(t *testing.T) {
	_, err := buildPromptWithFiles("prompt", []string{"/nonexistent/path/missing.go"}, 0)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "input_files[0]: file not found:") {
		t.Errorf("expected error to contain 'input_files[0]: file not found:', got: %v", err)
	}
}

func TestBuildPromptWithFiles_SizeExceeds(t *testing.T) {
	f, err := os.CreateTemp("", "llm-test-large-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(strings.Repeat("x", 100)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = buildPromptWithFiles("prompt", []string{f.Name()}, 50)
	if err == nil {
		t.Fatal("expected error when total size exceeds limit")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("expected error to mention 'exceeds limit', got: %v", err)
	}
}

func TestBuildPromptWithFiles_Empty(t *testing.T) {
	result, err := buildPromptWithFiles("my prompt", []string{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "my prompt" {
		t.Errorf("expected prompt unchanged, got: %q", result)
	}
}

func TestWriteOutput_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")

	summary, err := writeOutput(path, "hello world", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", string(got))
	}
	if !strings.Contains(summary, "saved 11 bytes") {
		t.Errorf("expected summary to contain 'saved 11 bytes', got: %s", summary)
	}
}

func TestWriteOutput_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(path, []byte("old content"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := writeOutput(path, "new content", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new content" {
		t.Errorf("expected overwrite, got %q", string(got))
	}
}

func TestWriteOutput_Append(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := writeOutput(path, "second\n", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "first\nsecond\n" {
		t.Errorf("expected appended content, got %q", string(got))
	}
}

func TestWriteOutput_DirNotFound(t *testing.T) {
	_, err := writeOutput("/nonexistent/dir/output.txt", "content", false)
	if err == nil {
		t.Fatal("expected error when parent directory missing")
	}
	if !strings.Contains(err.Error(), "output_file") {
		t.Errorf("expected error to contain 'output_file', got: %v", err)
	}
}
