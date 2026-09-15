package platform

// DefaultWindowSize returns the default window size as a fraction of the
// primary screen: 3/4 width, 4/5 height. When the live screen size cannot
// be determined (GDK not initialised yet — this runs before wails.Run),
// falls back to a sensible 1536x1024 (assumes a 2048x1280 display, which
// is the common case). Callers should treat the result as a hint; the OS
// window manager may still clamp it.
func DefaultWindowSize() (int, int) {
	w, h := screenSize()
	if w <= 0 || h <= 0 {
		return 1536, 1024
	}
	return w * 3 / 4, h * 4 / 5
}
