//go:build desktop && !darwin

package window

// titlebarDoubleClick maximises on Windows and Linux, as their own title
// bars do.
func titlebarDoubleClick() titlebarAction { return titlebarZoom }
