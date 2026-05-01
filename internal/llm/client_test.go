package llm

import (
	"encoding/base64"
	"os"
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
