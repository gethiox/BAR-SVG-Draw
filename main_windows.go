//go:build windows

// bardraw draws SVG images on the map in Beyond All Reason by emulating the
// map-drawing input (hold the draw key, drag with the left mouse button).
// See README.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	rl "github.com/gen2brain/raylib-go/raylib"

	"bardraw/internal/winput"
)

func init() {
	// raylib/OpenGL must stay on the main OS thread
	runtime.LockOSThread()
}

func main() {
	cfgFlag := flag.String("config", "", "path to bardraw.json (default: next to the executable)")
	dry := flag.Bool("dry", false, "simulate: show the drawing progress without sending input")
	flag.Parse()

	baseDir, _ := os.Getwd()
	if exe, err := os.Executable(); err == nil {
		baseDir = filepath.Dir(exe)
	}
	cfgPath := *cfgFlag
	if cfgPath == "" {
		cfgPath = filepath.Join(baseDir, "bardraw.json")
	}

	winput.EnableDPIAwareness()
	winput.HighResTimer(true)
	defer winput.HighResTimer(false)

	app, err := newApp(cfgPath, *dry)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bardraw:", err)
		winput.MessageBox("bardraw", err.Error())
		os.Exit(1)
	}
	defer app.shutdown()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; app.requestQuit() }()

	app.initWindow()
	defer rl.CloseWindow()
	app.loop()
}
