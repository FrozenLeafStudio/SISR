//go:build windows

package windows

import (
	"errors"
	"fmt"
	"syscall"
)

var (
	user32StyleDLL            = syscall.NewLazyDLL("user32.dll")
	getWindowLongPtrStyleProc = user32StyleDLL.NewProc("GetWindowLongPtrW")
	setWindowLongPtrStyleProc = user32StyleDLL.NewProc("SetWindowLongPtrW")
	setWindowPosStyleProc     = user32StyleDLL.NewProc("SetWindowPos")
	getWindowStyleProc        = user32StyleDLL.NewProc("GetWindow")
)

const (
	GWLStyle   int32 = -16
	GWLExStyle int32 = -20

	GWChild    = 5
	GWHwndNext = 2

	WSClipChildren  = 0x02000000
	WSExLayered     = 0x00080000
	WSExTransparent = 0x00000020

	SWPNoSize       = 0x0001
	SWPNoMove       = 0x0002
	SWPNoZOrder     = 0x0004
	SWPNoActivate   = 0x0010
	SWPFrameChanged = 0x0020
)

func getWindowLongPtr(hwnd uintptr, index int32) (uintptr, error) {
	value, _, callErr := getWindowLongPtrStyleProc.Call(hwnd, uintptr(index))
	if value == 0 && callErr != syscall.Errno(0) {
		return 0, fmt.Errorf("GetWindowLongPtrW failed: %w", callErr)
	}
	return value, nil
}

func setWindowLongPtr(hwnd uintptr, index int32, value uintptr) error {
	result, _, callErr := setWindowLongPtrStyleProc.Call(hwnd, uintptr(index), value)
	if result == 0 && callErr != syscall.Errno(0) {
		return fmt.Errorf("SetWindowLongPtrW failed: %w", callErr)
	}
	return nil
}

func ApplyFrameChanged(hwnd uintptr) error {
	flags := SWPNoSize | SWPNoMove | SWPNoZOrder | SWPNoActivate | SWPFrameChanged
	result, _, callErr := setWindowPosStyleProc.Call(hwnd, 0, 0, 0, 0, 0, uintptr(flags))
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("SetWindowPos failed: %w", callErr)
		}
		return fmt.Errorf("SetWindowPos failed")
	}
	return nil
}

func ClearWindowStyleBits(hwnd uintptr, bits uintptr) error {
	style, err := getWindowLongPtr(hwnd, GWLStyle)
	if err != nil {
		return err
	}
	newStyle := style &^ bits
	if newStyle == style {
		return nil
	}
	if err := setWindowLongPtr(hwnd, GWLStyle, newStyle); err != nil {
		return err
	}
	return ApplyFrameChanged(hwnd)
}

func UpdateWindowExStyleBits(hwnd uintptr, setBits uintptr, clearBits uintptr) error {
	exStyle, err := getWindowLongPtr(hwnd, GWLExStyle)
	if err != nil {
		return err
	}
	newExStyle := (exStyle | setBits) &^ clearBits
	if newExStyle == exStyle {
		return nil
	}
	if err := setWindowLongPtr(hwnd, GWLExStyle, newExStyle); err != nil {
		return err
	}
	return ApplyFrameChanged(hwnd)
}

// GetWindowExStyle returns the raw extended window style, which is useful when
// logging why a window does or does not receive mouse input.
func GetWindowExStyle(hwnd uintptr) (uintptr, error) {
	return getWindowLongPtr(hwnd, GWLExStyle)
}

func HasWindowExStyleBits(hwnd uintptr, bits uintptr) (bool, error) {
	exStyle, err := getWindowLongPtr(hwnd, GWLExStyle)
	if err != nil {
		return false, err
	}
	return exStyle&bits == bits, nil
}

func walkChildWindows(parentHwnd uintptr, visitor func(hwnd uintptr) error) error {
	child, _, _ := getWindowStyleProc.Call(parentHwnd, uintptr(GWChild))
	for child != 0 {
		nextChild, _, _ := getWindowStyleProc.Call(child, uintptr(GWHwndNext))
		if err := visitor(child); err != nil {
			return err
		}
		if err := walkChildWindows(child, visitor); err != nil {
			return err
		}
		child = nextChild
	}
	return nil
}

func isInvalidWindowHandleError(err error) bool {
	return errors.Is(err, syscall.Errno(6))
}

func UpdateChildWindowsExStyleBits(parentHwnd uintptr, setBits uintptr, clearBits uintptr) error {
	return walkChildWindows(parentHwnd, func(hwnd uintptr) error {
		err := UpdateWindowExStyleBits(hwnd, setBits, clearBits)
		if err != nil && isInvalidWindowHandleError(err) {
			return nil
		}
		return err
	})
}

func UpdateWindowAndChildrenExStyleBits(hwnd uintptr, setBits uintptr, clearBits uintptr) error {
	if err := UpdateWindowExStyleBits(hwnd, setBits, clearBits); err != nil {
		return err
	}
	return UpdateChildWindowsExStyleBits(hwnd, setBits, clearBits)
}
