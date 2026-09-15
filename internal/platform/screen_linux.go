//go:build linux

package platform

/*
#cgo pkg-config: gdk-3.0
#include <gdk/gdk.h>

// screen_size opens the default display without requiring gdk_init (which
// wails.Run calls later), reads the primary monitor geometry, and returns
// 0,0 on any failure.
static void screen_size(int *w, int *h) {
	*w = 0;
	*h = 0;
	GdkDisplay *display = gdk_display_open(NULL);
	if (display == NULL) {
		return;
	}
	GdkScreen *screen = gdk_display_get_default_screen(display);
	if (screen == NULL) {
		g_object_unref(display);
		return;
	}
	*w = gdk_screen_get_width(screen);
	*h = gdk_screen_get_height(screen);
	g_object_unref(display);
}
*/
import "C"

func screenSize() (int, int) {
	var w, h C.int
	C.screen_size(&w, &h)
	return int(w), int(h)
}
