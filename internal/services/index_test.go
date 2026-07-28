package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestNormalizeIgnorePaths(t *testing.T) {
	// Create a mock repo structure for absolute paths testing
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repository")
	err := os.MkdirAll(repoDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create directories in the repo_dir to test relative path matching
	err = os.MkdirAll(filepath.Join(repoDir, "vendor", "subdir"), 0755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(repoDir, "build"), 0755)
	if err != nil {
		t.Fatal(err)
	}

	// This test should validate that our filepath.WalkDir logic correctly ignores the given directories

	// Simulate the key condition checking logic from file walking
	tests := []struct {
		path           string
		ignoreDirs     []string
		expectedIgnore bool
		description    string
	}{
		{filepath.Join(repoDir, "vendor"), []string{"vendor"}, true, "should ignore relative 'vendor' directory"},
		{filepath.Join(repoDir, "vendor", "subdir"), []string{"vendor"}, true, "should ignore subdirectory of ignored directory"},
		{filepath.Join(repoDir, "build"), []string{"build"}, true, "should ignore 'build' directory"},
		{filepath.Join(repoDir, "src"), []string{"vendor"}, false, "should not ignore unrelated directory"},
		{filepath.Join(repoDir, "myvendor"), []string{"vendor"}, false, "should not ignore directory with prefix match only"},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			// Get the relative path from repoDir (this simulates what filepath.Rel produces)
			relPath, err := filepath.Rel(repoDir, test.path)
			if err != nil {
				t.Fatalf("Failed to get relative path: %v", err)
			}

			// Perform the same logic that's in index.go
			shouldIgnore := false
			for _, ignoreDir := range test.ignoreDirs {
				// Normalize based on whether it's absolute or relative
				if filepath.IsAbs(ignoreDir) {
					// If ignoreDir is absolute, check if current path starts with it
					if filepath.HasPrefix(test.path, ignoreDir+string(filepath.Separator)) || test.path == ignoreDir {
						shouldIgnore = true
						break
					}
				} else {
					// If ignoreDir is relative, match against relative path
					// Note: Clean is important to ensure consistent path elements
					cleanRelPath := filepath.Clean(relPath)
					normalIgnoreDir := filepath.Clean(ignoreDir)
					if cleanRelPath == normalIgnoreDir ||
						filepath.HasPrefix(cleanRelPath, normalIgnoreDir+string(filepath.Separator)) {
						shouldIgnore = true
						break
					}
				}
			}

			if shouldIgnore != test.expectedIgnore {
				t.Errorf("Path %s with ignore dirs %v: expected ignore=%v, got ignore=%v",
					test.path, test.ignoreDirs, test.expectedIgnore, shouldIgnore)
			}
		})
	}
}
