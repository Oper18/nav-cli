package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const goFixture = `package fixture

const (
	StatusPending = iota
	StatusActive
	StatusDone
)

func Greet(name string) string {
	return "hello " + name
}
`

func TestExtractSymbolsGo(t *testing.T) {
	dir := t.TempDir()
	rel := "fixture.go"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(goFixture), 0644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	symbols, err := ExtractSymbols(context.Background(), dir, rel, "main")
	if err != nil {
		t.Fatalf("ExtractSymbols: %v", err)
	}

	var fn, cst *Symbol
	for i := range symbols {
		switch symbols[i].Type {
		case "function":
			fn = &symbols[i]
		case "const":
			cst = &symbols[i]
		}
	}

	if fn == nil {
		t.Fatal("expected a function symbol for Greet")
	}
	if fn.Symbol != "Greet" {
		t.Errorf("function symbol = %q, want %q", fn.Symbol, "Greet")
	}
	if fn.StartLine != 9 {
		t.Errorf("function StartLine = %d, want 9", fn.StartLine)
	}

	if cst == nil {
		t.Fatal("expected a const symbol for the StatusPending block")
	}
	if cst.Symbol != "StatusPending" {
		t.Errorf("const symbol = %q, want %q (first spec in the block)", cst.Symbol, "StatusPending")
	}
	if cst.StartLine != 3 {
		t.Errorf("const StartLine = %d, want 3", cst.StartLine)
	}
}
