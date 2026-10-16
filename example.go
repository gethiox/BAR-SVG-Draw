package main

import (
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// exampleSVG ships inside the binary so a fresh install has something to draw.
//
//go:embed drawings/Fluttershy.svg
var exampleSVG []byte

const exampleName = "Fluttershy.svg"

// ensureDrawingsDir creates the drawings folder if it does not exist yet and
// puts the example drawing into it. An existing folder is never touched, so
// a deleted example stays deleted.
func ensureDrawingsDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, exampleName), exampleSVG, 0o644)
}
