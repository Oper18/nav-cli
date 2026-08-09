package parser

import "testing"

func TestShouldSkipNestedDirPatterns(t *testing.T) {
	patterns := []string{
		"vendor/**",
		"node_modules/**",
		"dist/**",
		"**/site-packages/**",
		"**/__pycache__/**",
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"top-level node_modules is skipped", "node_modules/foo/index.js", true},
		{"nested node_modules is skipped", ".opencode/node_modules/effect/src/foo.ts", true},
		{"deeply nested node_modules is skipped", "tools/plugin/node_modules/pkg/dist/index.js", true},
		{"nested vendor is skipped", "some-tool/vendor/github.com/pkg/errors/errors.go", true},
		{"nested dist is skipped", "web/app/dist/bundle.js", true},
		{"double-star anywhere pattern matches nested site-packages", "envs/py311/lib/site-packages/pkg/mod.py", true},
		{"double-star anywhere pattern matches nested __pycache__", "a/b/__pycache__/mod.cpython-311.pyc", true},
		{"real project file is not skipped", "internal/services/hooksearch.go", false},
		{"a file that merely contains the substring is not skipped", "internal/services/vendorstuff.go", false},
		{"a file inside a differently-named dir is not skipped", "internal/vendors/registry.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldSkip(tt.path, patterns); got != tt.want {
				t.Errorf("ShouldSkip(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestShouldSkipStandardGlobStillWorks(t *testing.T) {
	patterns := []string{"**/*.pb.go", "*.generated.go"}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"pb.go matched at any depth", "internal/proto/foo.pb.go", true},
		{"pb.go matched at repo root", "foo.pb.go", true},
		{"root-only pattern matches base name anywhere", "internal/api/foo.generated.go", true},
		{"unrelated file is kept", "internal/api/foo.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldSkip(tt.path, patterns); got != tt.want {
				t.Errorf("ShouldSkip(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestShouldSkipGoTestFiles(t *testing.T) {
	if !ShouldSkip("internal/services/hooksearch_test.go", nil) {
		t.Error("ShouldSkip should always skip Go _test.go files, even with no patterns")
	}
	if ShouldSkip("internal/services/hooksearch.go", nil) {
		t.Error("ShouldSkip should not skip a plain Go file with no patterns")
	}
}
