//go:build desktop && darwin && purego

package window

import (
	"github.com/ebitengine/purego/objc"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// NSApplicationActivationPolicy values.
const (
	policyRegular   = 0
	policyAccessory = 1
)

// setDock is dock_darwin.go's, for Wails's backend without cgo.
func setDock(shown bool) {
	application.InvokeSync(func() {
		app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
		if !shown {
			app.Send(objc.RegisterName("setActivationPolicy:"), policyAccessory)
			return
		}
		app.Send(objc.RegisterName("setActivationPolicy:"), policyRegular)
		app.Send(objc.RegisterName("activateIgnoringOtherApps:"), true)
	})
}
