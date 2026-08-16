//go:build windows

package extras

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Alia5/SISR/sdl"
	"github.com/Alia5/SISR/windows"
)

// GetWindowHitTest returns whether cursor hit-testing is currently enabled.
func GetWindowHitTest(window *sdl.Window) bool {
	hwnd := window.GetPointerProperty(sdl.WindowPointerPropertyWin32HWND)
	if hwnd == 0 {
		return true
	}

	hasTransparent, err := windows.HasWindowExStyleBits(hwnd, windows.WSExTransparent)
	if err != nil {
		return true
	}

	return !hasTransparent
}

// SetCursorHitTest controls whether the SDL window receives mouse hit-testing.
// With it enabled the window and its children lose WS_EX_TRANSPARENT and
// consume mouse input, so the caller must turn it back off when the UI leaves
// the screen.
func SetCursorHitTest(window *sdl.Window, hittest bool) (err error) {
	hwnd := window.GetPointerProperty(sdl.WindowPointerPropertyWin32HWND)
	if hwnd == 0 {
		slog.Debug("Skipping cursor hittest, window has no native handle", "hittest", hittest)
		return nil
	}
	before, err := windows.GetWindowExStyle(hwnd)
	if err != nil {
		return err
	}
	hasLayered := before&windows.WSExLayered == windows.WSExLayered
	hasTransparent := before&windows.WSExTransparent == windows.WSExTransparent

	// Named return so this can log the actual outcome, including error
	// returns from below; the after-fetch is itself a syscall, so skip it
	// when debug logging would just discard the result.
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		defer func() {
			after, afterErr := windows.GetWindowExStyle(hwnd)
			if afterErr != nil {
				slog.Debug("Set cursor hittest", "hittest", hittest, "error", err, "exStyleBefore", exStyle(before), "exStyleAfter", "unreadable")
				return
			}
			slog.Debug("Set cursor hittest",
				"hittest", hittest,
				"error", err,
				"exStyleBefore", exStyle(before),
				"exStyleAfter", exStyle(after),
				"clickThrough", after&windows.WSExTransparent == windows.WSExTransparent,
			)
		}()
	}

	if hittest {
		if hasTransparent {
			err = windows.UpdateWindowExStyleBits(hwnd, 0, windows.WSExTransparent|windows.WSExLayered)
			if err != nil {
				return err
			}
			err = windows.UpdateChildWindowsExStyleBits(hwnd, 0, windows.WSExTransparent)
			if err != nil {
				slog.Error("Failed to clear transparent style on child windows", "error", err)
			}
		}
		return nil
	}
	setBits := uintptr(windows.WSExTransparent)
	if !hasLayered {
		setBits |= windows.WSExLayered
	}
	if !hasTransparent || !hasLayered {
		err = windows.UpdateWindowExStyleBits(hwnd, setBits, 0)
		if err != nil {
			return err
		}
		err = windows.UpdateChildWindowsExStyleBits(hwnd, windows.WSExTransparent, 0)
		if err != nil {
			slog.Error("Failed to set transparent style on child windows", "error", err)
		}
	}
	return nil
}

func exStyle(v uintptr) string {
	return fmt.Sprintf("0x%08X", v)
}

// HandleCursorHitTestWindowEvent non linux stub
func HandleCursorHitTestWindowEvent(window *sdl.Window, event sdl.Event) error {
	_ = window
	_ = event
	return nil
}
