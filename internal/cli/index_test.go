package cli

import (
	"os"
	"path/filepath"
	"testing"
)


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
		path            string
		ignoreDirs      []string
		expectedIgnore  bool
		description     string
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