package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const goGraphFixture = `package fixture

import (
	"fmt"
	q "nav/internal/db/qdrant"
)

type Reader interface {
	Read() string
}

type Base struct {
	Name string
}

type Doc struct {
	Base
	*q.Client
}

func (d Doc) Read() string {
	return fmt.Sprint(d.Name)
}

func Load() *Doc {
	d := &Doc{}
	return d
}
`

func TestBuildFileNodesGoStructural(t *testing.T) {
	dir := t.TempDir()
	rel := "fixture.go"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(goGraphFixture), 0644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	symbols, err := ExtractSymbols(context.Background(), dir, rel, "main")
	if err != nil {
		t.Fatalf("ExtractSymbols: %v", err)
	}

	nodes, edges := BuildFileNodes(rel, symbols)

	byID := make(map[string]GraphNode)
	for _, n := range nodes {
		byID[n.ID] = n
	}

	pkgID := PackageNodeID(".")
	fileID := FileNodeID(rel)
	if _, ok := byID[pkgID]; !ok {
		t.Errorf("missing package node %q", pkgID)
	}
	if _, ok := byID[fileID]; !ok {
		t.Errorf("missing file node %q", fileID)
	}
	docID := SymbolNodeID(rel, "Doc")
	if n, ok := byID[docID]; !ok || n.Kind != nodeKindType {
		t.Errorf("missing/misclassified Doc node: %+v ok=%v", n, ok)
	}

	hasEdge := func(src, dst, kind string) bool {
		for _, e := range edges {
			if e.Src == src && e.Dst == dst && e.Kind == kind {
				return true
			}
		}
		return false
	}

	if !hasEdge(pkgID, fileID, edgeDefines) {
		t.Error("expected package --defines--> file edge")
	}
	if !hasEdge(fileID, docID, edgeDefines) {
		t.Error("expected file --defines--> Doc edge")
	}

	// Doc embeds Base (same-file, resolvable) and q.Client (qualified,
	// recorded against a synthetic external symbol node).
	if !hasEdge(docID, SymbolNodeID(rel, "Base"), edgeEmbeds) {
		t.Error("expected Doc --embeds--> Base edge")
	}
	if !hasEdge(docID, "sym:ext:q.Client", edgeEmbeds) {
		t.Error("expected Doc --embeds--> sym:ext:q.Client edge")
	}

	// Doc has a Read() method matching the Reader interface's sole method.
	readerID := SymbolNodeID(rel, "Reader")
	if !hasEdge(docID, readerID, edgeImplements) {
		t.Error("expected Doc --implements--> Reader edge")
	}
}

func TestExtractFileImports(t *testing.T) {
	refs := ExtractFileImports(LangGo, []byte(goGraphFixture))
	want := map[string]string{"fmt": "fmt", "q": "nav/internal/db/qdrant"}
	got := map[string]string{}
	for _, r := range refs {
		got[r.Alias] = r.Path
	}
	for alias, path := range want {
		if got[alias] != path {
			t.Errorf("import alias %q = %q, want %q (all: %+v)", alias, got[alias], path, refs)
		}
	}

	py := []byte("import os\nimport numpy as np\nfrom collections import OrderedDict, defaultdict as dd\n")
	pyRefs := ExtractFileImports(LangPython, py)
	pyGot := map[string]string{}
	for _, r := range pyRefs {
		pyGot[r.Alias] = r.Path
	}
	pyWant := map[string]string{
		"os":          "os",
		"np":          "numpy",
		"OrderedDict": "collections.OrderedDict",
		"dd":          "collections.defaultdict",
	}
	for alias, path := range pyWant {
		if pyGot[alias] != path {
			t.Errorf("python import alias %q = %q, want %q (all: %+v)", alias, pyGot[alias], path, pyRefs)
		}
	}

	ts := []byte(`import * as React from "react";
import Foo from "./foo";
import { a, b as bb } from "./bar";
`)
	tsRefs := ExtractFileImports(LangTypeScript, ts)
	tsGot := map[string]string{}
	for _, r := range tsRefs {
		tsGot[r.Alias] = r.Path
	}
	tsWant := map[string]string{"React": "react", "Foo": "./foo", "a": "./bar", "bb": "./bar"}
	for alias, path := range tsWant {
		if tsGot[alias] != path {
			t.Errorf("ts import alias %q = %q, want %q (all: %+v)", alias, tsGot[alias], path, tsRefs)
		}
	}
}
