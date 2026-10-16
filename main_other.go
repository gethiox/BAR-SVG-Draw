//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The overlay needs Windows input hooks and SendInput. On other platforms
// use cmd/svgplan to plan drawings and render previews.
func main() {
	fmt.Fprintln(os.Stderr, "bardraw's overlay currently runs on Windows only; try: go run ./cmd/svgplan -svg file.svg -o preview.png")
	os.Exit(1)
}
