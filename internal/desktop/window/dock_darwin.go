//go:build desktop && darwin && !purego

package window

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void exhaleSetDock(int shown) {
	if (shown) {
		[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
		[NSApp activateIgnoringOtherApps:YES];
	} else {
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	}
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// setDock shows the Dock icon while the window is shown, and brings the app
// to the front; with the window hidden, only the menu bar icon is left.
func setDock(shown bool) {
	v := C.int(0)
	if shown {
		v = 1
	}
	application.InvokeSync(func() { C.exhaleSetDock(v) })
}
