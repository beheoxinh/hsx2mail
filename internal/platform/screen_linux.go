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
	GdkMonitor *monitor = gdk_display_get_primary_monitor(display);
	if (monitor == NULL && gdk_display_get_n_monitors(display) > 0) {
		monitor = gdk_display_get_monitor(display, 0);
	}
	if (monitor != NULL) {
		GdkRectangle geom;
		gdk_monitor_get_geometry(monitor, &geom);
		*w = geom.width;
		*h = geom.height;
	}
	g_object_unref(display);
}
*/
import "C"

func screenSize() (int, int) {
	var w, h C.int
	C.screen_size(&w, &h)
	return int(w), int(h)
}
