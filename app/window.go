package app

import (
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// RefreshWindowConstraints removes window max size constraints. This works
// around a Wails v2 Linux limitation where GTK geometry hints are set once at
// startup using the initial monitor's dimensions, causing the window to be
// stuck at that size when moving to a larger monitor.
// We use a large value instead of 0,0 because GTK may interpret zero as
// "use current hints" rather than "remove constraints".
func (a *App) RefreshWindowConstraints() {
	wailsRuntime.WindowSetMaxSize(a.ctx, 100000, 100000)
}

// RefreshWindowConstraints removes window max size constraints for the
// composer window. See App.RefreshWindowConstraints for details.
func (c *ComposerApp) RefreshWindowConstraints() {
	wailsRuntime.WindowSetMaxSize(c.ctx, 100000, 100000)
}
