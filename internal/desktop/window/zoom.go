//go:build desktop

package window

// titlebarAction is what a double click on the title bar does.
type titlebarAction uint8

const (
	titlebarZoom     titlebarAction = iota // fill the screen, or back to the size it had
	titlebarMinimise                       // to the Dock
	titlebarNothing
)

// macTitlebarAction reads the Mac's "Double-click a window's title bar to"
// setting (AppleActionOnDoubleClick): "Fill" (the default since macOS 15),
// "Maximize" (Zoom), "Minimize", or "None" (Do Nothing). Wails 3.1 treats
// "Fill" as nothing, so the window reads the setting itself; on a Mac with
// no window tiling, Fill and Zoom look the same: the window takes the
// screen, the menu bar and the Dock stay.
func macTitlebarAction(pref string) titlebarAction {
	switch pref {
	case "Minimize":
		return titlebarMinimise
	case "None":
		return titlebarNothing
	default:
		return titlebarZoom
	}
}
