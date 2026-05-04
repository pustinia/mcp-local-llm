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
