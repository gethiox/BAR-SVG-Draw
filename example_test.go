package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"bardraw/internal/svg"
)

func TestEnsureDrawingsDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "drawings")
	if err := ensureDrawingsDir(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, exampleName))
	if err != nil || !bytes.Equal(got, exampleSVG) {
		t.Fatalf("example not written: %v", err)
	}
	// the example must be a drawable SVG
	doc, err := svg.Parse(bytes.NewReader(exampleSVG))
	if err != nil || len(doc.Shapes) == 0 {
		t.Fatalf("embedded example does not parse: %v", err)
	}
	// an existing folder is left alone: a deleted example is not restored
	if err := os.Remove(filepath.Join(dir, exampleName)); err != nil {
		t.Fatal(err)
	}
	if err := ensureDrawingsDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, exampleName)); !os.IsNotExist(err) {
		t.Fatal("example re-created in an existing folder")
	}
}
