package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbedBatchesConcurrentlyRespectsConcurrencyLimit(t *testing.T) {
	texts := make([]string, embedBatchSize*12) // 12 batches, well above embedConcurrency
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	var inFlight int32
	var peak int32
	var mu sync.Mutex

	embed := func(batch []string) ([][]float32, error) {
		n := atomic.AddInt32(&inFlight, 1)
		defer atomic.AddInt32(&inFlight, -1)

		mu.Lock()
		if n > peak {
			peak = n
		}
		mu.Unlock()

		// Give other goroutines a chance to actually overlap in time,
		// rather than racing through so fast the scheduler never runs
		// more than one at once.
		time.Sleep(10 * time.Millisecond)

		vecs := make([][]float32, len(batch))
		for i := range batch {
			vecs[i] = []float32{1}
		}
		return vecs, nil
	}

	if _, err := embedBatchesConcurrently(texts, embed, nil); err != nil {
		t.Fatalf("embedBatchesConcurrently: %v", err)
	}

	if peak > embedConcurrency {
		t.Errorf("peak concurrent batches = %d, want <= %d", peak, embedConcurrency)
	}
	if peak < 2 {
		t.Errorf("peak concurrent batches = %d, expected batches to actually run in parallel (want > 1)", peak)
	}
}

func TestEmbedBatchesConcurrentlyPreservesOrder(t *testing.T) {
	texts := make([]string, embedBatchSize*4+3) // several full batches plus a partial one
	for i := range texts {
		texts[i] = fmt.Sprintf("%d", i)
	}

	embed := func(batch []string) ([][]float32, error) {
		vecs := make([][]float32, len(batch))
		for i, txt := range batch {
			var n int
			fmt.Sscanf(txt, "%d", &n)
			vecs[i] = []float32{float32(n)}
		}
		return vecs, nil
	}

	vectors, err := embedBatchesConcurrently(texts, embed, nil)
	if err != nil {
		t.Fatalf("embedBatchesConcurrently: %v", err)
	}
	for i, v := range vectors {
		if len(v) != 1 || v[0] != float32(i) {
			t.Fatalf("vectors[%d] = %v, want [%d]", i, v, i)
		}
	}
}

func TestEmbedBatchesConcurrentlyReturnsFirstError(t *testing.T) {
	texts := make([]string, embedBatchSize*6)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	failingBatch := 3 // fail the 4th batch (index 3), not the first, to confirm
	// the error is still surfaced even when other batches succeed.
	var calls int32

	wantErr := errors.New("boom")
	embed := func(batch []string) ([][]float32, error) {
		n := atomic.AddInt32(&calls, 1)
		if int(n-1) == failingBatch {
			return nil, wantErr
		}
		vecs := make([][]float32, len(batch))
		for i := range batch {
			vecs[i] = []float32{0}
		}
		return vecs, nil
	}

	_, err := embedBatchesConcurrently(texts, embed, nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestEmbedBatchesConcurrentlyEmptyInput(t *testing.T) {
	called := false
	embed := func(batch []string) ([][]float32, error) {
		called = true
		return nil, nil
	}

	vectors, err := embedBatchesConcurrently(nil, embed, nil)
	if err != nil {
		t.Fatalf("embedBatchesConcurrently: %v", err)
	}
	if len(vectors) != 0 {
		t.Errorf("expected no vectors for empty input, got %v", vectors)
	}
	if called {
		t.Error("embed should never be called for empty input")
	}
}

func TestUnderIgnoredDir(t *testing.T) {
	repoDir := filepath.Join(string(filepath.Separator), "repository")

	tests := []struct {
		rel         string
		ignoreDirs  []string
		want        bool
		description string
	}{
		{"vendor", []string{"vendor"}, true, "should ignore relative 'vendor' directory itself"},
		{filepath.Join("vendor", "subdir"), []string{"vendor"}, true, "should ignore subdirectory of ignored directory"},
		{filepath.Join("vendor", "subdir", "file.go"), []string{"vendor"}, true, "should ignore a file nested under an ignored directory"},
		{"build", []string{"build"}, true, "should ignore 'build' directory"},
		{"src", []string{"vendor"}, false, "should not ignore unrelated directory"},
		{"myvendor", []string{"vendor"}, false, "should not ignore a directory that merely shares a prefix"},
		{filepath.Join("vendor", "file.go"), []string{filepath.Join(repoDir, "vendor")}, true, "absolute ignoreDir matches the corresponding relative rel"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			if got := underIgnoredDir(repoDir, tt.rel, tt.ignoreDirs); got != tt.want {
				t.Errorf("underIgnoredDir(%q, %q, %v) = %v, want %v", repoDir, tt.rel, tt.ignoreDirs, got, tt.want)
			}
		})
	}
}

func TestFilterIgnoredDirs(t *testing.T) {
	repoDir := filepath.Join(string(filepath.Separator), "repository")
	relPaths := []string{"main.go", filepath.Join("vendor", "pkg", "a.go"), filepath.Join("internal", "x.go")}

	got := filterIgnoredDirs(repoDir, relPaths, []string{"vendor"})
	want := []string{"main.go", filepath.Join("internal", "x.go")}

	if len(got) != len(want) {
		t.Fatalf("filterIgnoredDirs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("filterIgnoredDirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWalkRepoFilesSkipsIgnoredDirs(t *testing.T) {
	repoDir := t.TempDir()
	mustWriteFile(t, filepath.Join(repoDir, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(repoDir, "vendor", "pkg", "a.go"), "package pkg\n")
	mustWriteFile(t, filepath.Join(repoDir, "internal", "x.go"), "package internal\n")

	got, err := walkRepoFiles(repoDir, []string{"vendor"})
	if err != nil {
		t.Fatalf("walkRepoFiles: %v", err)
	}

	for _, rel := range got {
		if strings.HasPrefix(filepath.ToSlash(rel), "vendor/") {
			t.Errorf("walkRepoFiles returned an ignored path: %q (full list: %v)", rel, got)
		}
	}
	if !containsPath(got, "main.go") || !containsPath(got, filepath.Join("internal", "x.go")) {
		t.Errorf("walkRepoFiles missed a non-ignored file, got: %v", got)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
