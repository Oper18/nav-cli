package services

import (
	"strings"
	"testing"

	"nav/internal/db"
)

func TestQueryIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "picks out identifier-shaped tokens, drops short filler",
			query: "why does HookSearch fail on an empty query?",
			want:  []string{"why", "does", "HookSearch", "fail", "empty", "query"},
		},
		{
			name:  "dedupes, keeping first-seen order",
			query: "Handle calls helper, and Handle again",
			want:  []string{"Handle", "calls", "helper", "and", "again"},
		},
		{
			name:  "empty query yields no tokens",
			query: "",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queryIdentifiers(tt.query)
			if len(got) != len(tt.want) {
				t.Fatalf("queryIdentifiers(%q) = %v, want %v", tt.query, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("queryIdentifiers(%q)[%d] = %q, want %q", tt.query, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGraphSearchExactMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sdb, err := db.Open("test", "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	results, err := graphSearch(sdb, "why does Handle call helper", 0)
	if err != nil {
		t.Fatalf("graphSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("graphSearch returned %d results, want 2: %+v", len(results), results)
	}

	// "Handle" appears before "helper" in the query, so it must come first.
	if results[0].Symbol != "Handle" || results[1].Symbol != "helper" {
		t.Fatalf("results out of query order: got %q, %q", results[0].Symbol, results[1].Symbol)
	}

	handle := results[0]
	if handle.Score != 1 {
		t.Errorf("exact graph hit Score = %v, want 1", handle.Score)
	}
	if !strings.Contains(handle.Purpose, "Handles inbound requests.") {
		t.Errorf("Handle purpose missing summary: %q", handle.Purpose)
	}
	if !strings.Contains(handle.Purpose, "internal/api/handler.go:10") {
		t.Errorf("Handle purpose missing definition site: %q", handle.Purpose)
	}
	if !strings.Contains(handle.Code, "-calls-> sym:internal/api/handler.go#helper") {
		t.Errorf("Handle Code missing outgoing call edge: %q", handle.Code)
	}
	if !strings.Contains(handle.Code, "sym:cmd/main.go#main -calls->") {
		t.Errorf("Handle Code missing incoming call edge: %q", handle.Code)
	}
}

func TestGraphSearchNoMatchIsNotAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sdb, err := db.Open("test", "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	results, err := graphSearch(sdb, "totally unrelated prompt text", 0)
	if err != nil {
		t.Fatalf("graphSearch: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("graphSearch returned %d results for a query with no matching symbols, want 0: %+v", len(results), results)
	}
}

func TestGraphSearchRespectsLimit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sdb, err := db.Open("test", "main")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sdb.Close()

	seedTinyGraph(t, sdb)

	results, err := graphSearch(sdb, "Handle and helper and main all match", 1)
	if err != nil {
		t.Fatalf("graphSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("graphSearch returned %d results, want 1 (limit): %+v", len(results), results)
	}
	if results[0].Symbol != "Handle" {
		t.Errorf("graphSearch with limit 1 = %q, want the first-matching token %q", results[0].Symbol, "Handle")
	}
}
