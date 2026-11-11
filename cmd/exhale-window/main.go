//go:build desktop

// Command exhale-window shows Exhale's web UI in a window of its own on
// Linux (ADR 0030). The packages install it next to exhale, whose `open`
// runs it once the server answers; it is built separately, with cgo and
// GTK 4, so that exhale itself needs no graphical library.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/akamachinecode/exhale/internal/desktop/window"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8484", "Address of the web UI.")
	flag.Parse()
	if err := window.Run(window.Options{URL: *url}); err != nil {
		fmt.Fprintln(os.Stderr, "exhale-window:", err)
		os.Exit(1)
	}
}
