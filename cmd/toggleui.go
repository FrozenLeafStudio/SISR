package cmd

import (
	"context"
	"log/slog"

	"github.com/Alia5/SISR/sdl"
	"github.com/Alia5/SISR/sdl/extras"
	"github.com/Alia5/SISR/webview"
)

// uiIsVisible reports whether the UI should be treated as on screen.
//
// wv.SetVisible is applied from a queued dispatch, so the webview flag can lag
// behind it; the ex-style behind hitTest is applied straight away and decides
// the toggle while it does. hitTest only tracks the UI while kbm emulation is
// disabled, since with it enabled the overlay hit-tests from startup and is
// never cleared.
//
// Caveat: the Steam overlay callback also sets hitTest true while its overlay
// is open, so fullscreen + kbm off + an open overlay reads as UI-visible and
// a toggle then hides input for the overlay instead of the SISR UI.
func uiIsVisible(windowHidden, webviewVisible, hitTest, kbmEnabled bool) bool {
	return !windowHidden && (webviewVisible || (hitTest && !kbmEnabled))
}

// ToggleUI shows or hides the SISR UI, deciding which way to go from
// uiIsVisible. It blocks until the window dispatcher has processed the
// toggle, so callers on the dispatcher's own goroutine must not call it
// directly.
func ToggleUI(ctx context.Context, c *SISRContext) {
	_, err := ScheduleWindowDispatch(ctx, c.WindowDispatcher, func(w *sdl.Window, wv webview.WebView) bool {
		c.Config.Lock()
		fullscreen := c.Config.Fullscreen
		kbmEnabled := c.Config.KeyboardMouseEmulation
		c.Config.Unlock()

		windowHidden := w.GetWindowFlags()&sdl.WindowFlagHidden != 0
		webviewVisible := wv.Visible()
		hitTest := extras.GetWindowHitTest(w)

		uiVisible := uiIsVisible(windowHidden, webviewVisible, hitTest, kbmEnabled)

		if uiVisible {
			if !kbmEnabled {
				err := extras.SetCursorHitTest(w, false)
				if err != nil {
					slog.Error("Failed setting window cursor hittest", "error", err)
				}
			}
			if !fullscreen {
				w.HideWindow()
			}
			wv.SetVisible(false)
			return false
		} else {
			w.ShowWindow()
			wv.Eval("window.invalidateAll();")
			_ = c.WindowDispatcher.Schedule(func(w *sdl.Window, wv webview.WebView) any {
				// A hide can land on the dispatcher before this queued show
				// runs; skip it then instead of leaving a fullscreen UI
				// painted with the mouse still passing through to it.
				if !kbmEnabled && !extras.GetWindowHitTest(w) {
					return nil
				}
				wv.SetVisible(true)
				return nil
			})
			err := extras.SetCursorHitTest(w, true)
			if err != nil {
				slog.Error("Failed setting window cursor hittest", "error", err)
			}
			return true
		}
	})
	if err != nil {
		slog.Error("Failed to toggle UI visibility", "error", err)
	}
}
