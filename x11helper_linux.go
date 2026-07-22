package main

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/Xatom.h>
#include <stdlib.h>
#include <string.h>

static int xerror_flag = 0;
static int catch_xerror(Display *d, XErrorEvent *e) {
	xerror_flag = 1;
	return 0;
}

static int isWindowAlive(unsigned long wid) {
	Display *d = XOpenDisplay(NULL);
	if (!d) return 0;
	XErrorHandler old = XSetErrorHandler(catch_xerror);
	xerror_flag = 0;
	XWindowAttributes attr;
	XGetWindowAttributes(d, (Window)wid, &attr);
	XSync(d, False);
	XSetErrorHandler(old);
	XCloseDisplay(d);
	return !xerror_flag;
}

static void setWMClass(unsigned long wid, const char *name, const char *class) {
	Display *d = XOpenDisplay(NULL);
	if (!d) return;
	Window w = (Window)wid;
	XClassHint ch;
	ch.res_name = (char*)name;
	ch.res_class = (char*)class;
	XSetClassHint(d, w, &ch);
	XFlush(d);
	XCloseDisplay(d);
}

static int activateWindow(unsigned long wid) {
	Display *d = XOpenDisplay(NULL);
	if (!d) return 0;
	Window w = (Window)wid;

	XErrorHandler old = XSetErrorHandler(catch_xerror);
	xerror_flag = 0;

	XWindowAttributes attr;
	if (XGetWindowAttributes(d, w, &attr) == 0) {
		XSetErrorHandler(old);
		XCloseDisplay(d);
		return 0;
	}

	Atom net_wm_state = XInternAtom(d, "_NET_WM_STATE", False);
	Atom net_wm_state_hidden = XInternAtom(d, "_NET_WM_STATE_HIDDEN", False);
	Atom net_active_window = XInternAtom(d, "_NET_ACTIVE_WINDOW", False);
	Atom wm_state_atom = XInternAtom(d, "WM_STATE", False);
	Window root = DefaultRootWindow(d);

	// Find top-level ancestor for mapping
	Window top = w;
	for (int i = 0; i < 10; i++) {
		Window r, p, *c; unsigned int n;
		if (XQueryTree(d, top, &r, &p, &c, &n) == 0) break;
		if (c) XFree(c);
		if (p == r) break;
		top = p;
	}

	// Step 1: Map top-level ancestor and target window
	XMapRaised(d, top);
	XMapRaised(d, w);
	XFlush(d);

	// Step 2: Set WM_STATE = NormalState
	long wm_state_data[2] = { 1, None };
	XChangeProperty(d, w, wm_state_atom, wm_state_atom, 32, PropModeReplace,
	                (unsigned char*)wm_state_data, 2);

	// Step 3: Remove _NET_WM_STATE_HIDDEN via ClientMessage (source=pager)
	XEvent ev1 = {0};
	ev1.xclient.type = ClientMessage;
	ev1.xclient.window = w;
	ev1.xclient.message_type = net_wm_state;
	ev1.xclient.format = 32;
	ev1.xclient.data.l[0] = 0; // _NET_WM_STATE_REMOVE
	ev1.xclient.data.l[1] = (long)net_wm_state_hidden;
	ev1.xclient.data.l[2] = 0;
	ev1.xclient.data.l[3] = 2; // source: pager
	ev1.xclient.data.l[4] = 0;
	XSendEvent(d, root, False,
	           SubstructureRedirectMask | SubstructureNotifyMask, &ev1);

	// Step 4: Directly rewrite _NET_WM_STATE, removing HIDDEN
	Atom actual_type; int actual_format;
	unsigned long nitems, bytes_after;
	unsigned char *prop_data = NULL;
	Atom new_states[64]; int new_count = 0;
	if (XGetWindowProperty(d, w, net_wm_state, 0, 64, False, XA_ATOM,
	                       &actual_type, &actual_format, &nitems, &bytes_after,
	                       &prop_data) == Success && prop_data) {
		Atom *old_states = (Atom*)prop_data;
		for (unsigned long i = 0; i < nitems && new_count < 64; i++) {
			if (old_states[i] != net_wm_state_hidden) {
				new_states[new_count++] = old_states[i];
			}
		}
		XFree(prop_data);
	}
	XChangeProperty(d, w, net_wm_state, XA_ATOM, 32, PropModeReplace,
	                (unsigned char*)new_states, new_count);

	// Step 5: Also activate the Chrome main window if this is a Chrome app child
	// This is needed because Mutter manages _NET_ACTIVE_WINDOW per top-level
	Window chromeMain = 0;
	Window r2, *c2; unsigned int n2;
	if (XQueryTree(d, root, &r2, &chromeMain, &c2, &n2) != 0 && c2) {
		// We need to scan root children for Chrome main window
		XFree(c2);
	}
	// Find Chrome main window from root children
	Window root2 = DefaultRootWindow(d);
	Window parent2, *children2;
	unsigned int nchildren2;
	chromeMain = 0;
	if (XQueryTree(d, root2, &root2, &parent2, &children2, &nchildren2) != 0) {
		for (unsigned int i = 0; i < nchildren2; i++) {
			XClassHint ch2;
			if (XGetClassHint(d, children2[i], &ch2) != 0) {
				if (strcmp(ch2.res_class, "Google-chrome") == 0 &&
				    strcmp(ch2.res_name, "google-chrome") == 0) {
					chromeMain = children2[i];
				}
				XFree(ch2.res_name);
				XFree(ch2.res_class);
				if (chromeMain) break;
			}
		}
		if (children2) XFree(children2);
	}
	if (chromeMain) {
		XMapRaised(d, chromeMain);
		XEvent ev3 = {0};
		ev3.xclient.type = ClientMessage;
		ev3.xclient.window = chromeMain;
		ev3.xclient.message_type = net_active_window;
		ev3.xclient.format = 32;
		ev3.xclient.data.l[0] = 2; // pager
		ev3.xclient.data.l[1] = CurrentTime;
		ev3.xclient.data.l[2] = 0;
		ev3.xclient.data.l[3] = 0;
		ev3.xclient.data.l[4] = 0;
		XSendEvent(d, root, False,
		           SubstructureRedirectMask | SubstructureNotifyMask, &ev3);
		XRaiseWindow(d, chromeMain);
	}

	// Step 6: Send _NET_ACTIVE_WINDOW to target (source=pager)
	XEvent ev2 = {0};
	ev2.xclient.type = ClientMessage;
	ev2.xclient.window = w;
	ev2.xclient.message_type = net_active_window;
	ev2.xclient.format = 32;
	ev2.xclient.data.l[0] = 2; // source: pager
	ev2.xclient.data.l[1] = CurrentTime;
	ev2.xclient.data.l[2] = 0;
	ev2.xclient.data.l[3] = 0;
	ev2.xclient.data.l[4] = 0;
	XSendEvent(d, root, False,
	           SubstructureRedirectMask | SubstructureNotifyMask, &ev2);

	// Step 7: Direct raise + focus
	XRaiseWindow(d, top);
	XRaiseWindow(d, w);
	XSetInputFocus(d, w, RevertToPointerRoot, CurrentTime);

	XSync(d, False);
	XSetErrorHandler(old);
	XCloseDisplay(d);
	return !xerror_flag;
}

// Recursive helper: find Chrome --app windows in the full window tree
static void findAppWindowsRecursive(Display *d, Window w,
                                     unsigned long *out, int *count, int maxOut) {
	Window parent, *children;
	unsigned int nchildren;

	if (XQueryTree(d, w, &w, &parent, &children, &nchildren) == 0 || !children)
		return;

	for (unsigned int i = 0; i < nchildren && *count < maxOut; i++) {
		XClassHint ch;
		if (XGetClassHint(d, children[i], &ch) != 0) {
			int isAppWindow = (strcmp(ch.res_class, "Google-chrome") == 0 &&
			                   strcmp(ch.res_name, "google-chrome") != 0) ||
			              (strcmp(ch.res_class, "Chromium") == 0 &&
			               strcmp(ch.res_name, "chromium") != 0 &&
			               strcmp(ch.res_name, "chromium-browser") != 0) ||
			              strcmp(ch.res_class, "Webdesk") == 0;
			XFree(ch.res_name);
			XFree(ch.res_class);
			if (isAppWindow && *count < maxOut) {
				out[(*count)++] = (unsigned long)children[i];
			}
		}
		// Recurse into children
		findAppWindowsRecursive(d, children[i], out, count, maxOut);
	}
	XFree(children);
}

static unsigned long findChromeMainWindow() {
	Display *d = XOpenDisplay(NULL);
	if (!d) return 0;
	Window root = DefaultRootWindow(d);
	Window parent, *children;
	unsigned int nchildren;
	unsigned long result = 0;

	if (XQueryTree(d, root, &root, &parent, &children, &nchildren) == 0) {
		XCloseDisplay(d);
		return 0;
	}

	for (unsigned int i = 0; i < nchildren; i++) {
		XClassHint ch;
		if (XGetClassHint(d, children[i], &ch) != 0) {
			if (strcmp(ch.res_class, "Google-chrome") == 0 &&
			    strcmp(ch.res_name, "google-chrome") == 0) {
				result = (unsigned long)children[i];
			}
			XFree(ch.res_name);
			XFree(ch.res_class);
			if (result) break;
		}
	}

	if (children) XFree(children);
	XCloseDisplay(d);
	return result;
}

static int findAllAppWindows(unsigned long *out, int maxOut) {
	Display *d = XOpenDisplay(NULL);
	if (!d) return 0;
	int count = 0;
	findAppWindowsRecursive(d, DefaultRootWindow(d), out, &count, maxOut);
	XCloseDisplay(d);
	return count;
}

// Find by WM_CLASS res_name containing substr, recursive
static void findByTitleRecursive(Display *d, Window w, const char *substr,
                                  unsigned long *out, int *count, int maxOut) {
	Window parent, *children;
	unsigned int nchildren;

	if (XQueryTree(d, w, &w, &parent, &children, &nchildren) == 0 || !children)
		return;

	for (unsigned int i = 0; i < nchildren && *count < maxOut; i++) {
		XClassHint ch;
		if (XGetClassHint(d, children[i], &ch) != 0) {
			int isAppWindow = (strcmp(ch.res_class, "Google-chrome") == 0 &&
			                   strcmp(ch.res_name, "google-chrome") != 0) ||
			              (strcmp(ch.res_class, "Chromium") == 0 &&
			               strcmp(ch.res_name, "chromium") != 0 &&
			               strcmp(ch.res_name, "chromium-browser") != 0) ||
			              strcmp(ch.res_class, "Webdesk") == 0;
			if (isAppWindow) {
				if (strstr(ch.res_name, substr) != NULL && *count < maxOut) {
					out[(*count)++] = (unsigned long)children[i];
				}
			}
			XFree(ch.res_name);
			XFree(ch.res_class);
		}
		findByTitleRecursive(d, children[i], substr, out, count, maxOut);
	}
	XFree(children);
}

static int findAllAppWindowsByTitle(const char *substr, unsigned long *out, int maxOut) {
	Display *d = XOpenDisplay(NULL);
	if (!d) return 0;
	int count = 0;
	findByTitleRecursive(d, DefaultRootWindow(d), substr, out, &count, maxOut);
	XCloseDisplay(d);
	return count;
}
*/
import "C"
import (
	"strings"
	"unsafe"
)

func setWMClass(wid uintptr, name, class string) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	cClass := C.CString(class)
	defer C.free(unsafe.Pointer(cClass))
	C.setWMClass(C.ulong(wid), cName, cClass)
}

func activateX11Window(wid uintptr) bool {
	return C.activateWindow(C.ulong(wid)) != 0
}

func isWindowValid(wid uintptr) bool {
	return C.isWindowAlive(C.ulong(wid)) != 0
}

func findChromeAppWindowsByTitle(substr string) map[uintptr]bool {
	result := make(map[uintptr]bool)
	var buf [512]C.ulong
	cStr := C.CString(substr)
	defer C.free(unsafe.Pointer(cStr))
	count := C.findAllAppWindowsByTitle(cStr, &buf[0], 512)
	for i := C.int(0); i < count; i++ {
		result[uintptr(buf[i])] = true
	}
	return result
}

func findAllChromeAppWindows() map[uintptr]bool {
	result := make(map[uintptr]bool)
	var buf [512]C.ulong
	count := C.findAllAppWindows(&buf[0], 512)
	for i := C.int(0); i < count; i++ {
		result[uintptr(buf[i])] = true
	}
	return result
}

func extractHost(rawURL string) string {
	u := rawURL
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "www.")
	idx := strings.IndexAny(u, "/?#:")
	if idx >= 0 {
		u = u[:idx]
	}
	return u
}
