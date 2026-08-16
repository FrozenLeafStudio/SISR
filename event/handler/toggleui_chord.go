package handler

import (
	"context"
	"log/slog"

	"github.com/Alia5/SISR/cmd"
	"github.com/Alia5/SISR/sdl"
)

var (
	shortcutCtrlDown  bool
	shortcutShiftDown bool
	shortcutAltDown   bool

	shortcutGamepadChordState = map[sdl.GamepadID]uint8{}
)

const (
	shortcutGamepadLB uint8 = 1 << iota
	shortcutGamepadRB
	shortcutGamepadBack
)

const shortcutGamepadMask = shortcutGamepadLB | shortcutGamepadRB | shortcutGamepadBack

func ToggleUIKeyboardDown(c *cmd.SISRContext) Operation[*sdl.KeyboardEvent] {
	return Operation[*sdl.KeyboardEvent]{
		Event: sdl.EventTypeKeyDown,
		Handler: HandleFunc(func(ctx context.Context, ev *sdl.KeyboardEvent) error {
			setShortcutModifierState(ev.Key, true)

			if ev.Repeat || ev.Key != sdl.KeyCodeS {
				return nil
			}

			if !shortcutCtrlDown || !shortcutShiftDown || !shortcutAltDown {
				return nil
			}

			slog.Debug("UI toggle keyboard shortcut detected")
			toggleUI(ctx, c)

			return nil
		}),
	}
}

func ToggleUIKeyboardUp() Operation[*sdl.KeyboardEvent] {
	return Operation[*sdl.KeyboardEvent]{
		Event: sdl.EventTypeKeyUp,
		Handler: HandleFunc(func(_ context.Context, ev *sdl.KeyboardEvent) error {
			setShortcutModifierState(ev.Key, false)
			return nil
		}),
	}
}

func ToggleUIGamepadButtonDown(c *cmd.SISRContext) Operation[*sdl.GamepadButtonEvent] {
	return Operation[*sdl.GamepadButtonEvent]{
		Event: sdl.EventTypeGamepadButtonDown,
		Handler: HandleFunc(func(ctx context.Context, ev *sdl.GamepadButtonEvent) error {
			gpID := sdl.GamepadID(ev.Which)
			button := sdl.GamepadButton(ev.Button)

			dev, ok := c.DeviceStore.DeviceForID(gpID)
			if !ok {
				return nil
			}
			dev.Lock()
			defer dev.Unlock()

			if dev.SteamVirtualGamepad == nil || dev.SteamVirtualGamepad.ID() != gpID {
				return nil
			}

			if button == sdl.GamepadButtonLeftShoulder ||
				button == sdl.GamepadButtonRightShoulder ||
				button == sdl.GamepadButtonBack {
				setChordButtonDown(gpID, button)
				return nil
			}

			if button != sdl.GamepadButtonSouth {
				return nil
			}

			if !isChordPressed(gpID) {
				return nil
			}

			slog.Debug("UI toggle controller chord detected (A/South pressed last)", "gamepadId", gpID)
			toggleUI(ctx, c)

			return nil
		}),
	}
}

func ToggleUIGamepadButtonUp() Operation[*sdl.GamepadButtonEvent] {
	return Operation[*sdl.GamepadButtonEvent]{
		Event: sdl.EventTypeGamepadButtonUp,
		Handler: HandleFunc(func(_ context.Context, ev *sdl.GamepadButtonEvent) error {
			gpID := sdl.GamepadID(ev.Which)
			button := sdl.GamepadButton(ev.Button)

			if button != sdl.GamepadButtonLeftShoulder &&
				button != sdl.GamepadButtonRightShoulder &&
				button != sdl.GamepadButtonBack {
				return nil
			}

			setChordButtonUp(gpID, button)
			return nil
		}),
	}
}

func setChordButtonDown(gpID sdl.GamepadID, button sdl.GamepadButton) {
	state := shortcutGamepadChordState[gpID]
	state |= chordBit(button)
	shortcutGamepadChordState[gpID] = state
}

func setChordButtonUp(gpID sdl.GamepadID, button sdl.GamepadButton) {
	state := shortcutGamepadChordState[gpID]
	state &^= chordBit(button)
	if state == 0 {
		delete(shortcutGamepadChordState, gpID)
		return
	}

	shortcutGamepadChordState[gpID] = state
}

func isChordPressed(gpID sdl.GamepadID) bool {
	return shortcutGamepadChordState[gpID]&shortcutGamepadMask == shortcutGamepadMask
}

func chordBit(button sdl.GamepadButton) uint8 {
	switch button {
	case sdl.GamepadButtonLeftShoulder:
		return shortcutGamepadLB
	case sdl.GamepadButtonRightShoulder:
		return shortcutGamepadRB
	case sdl.GamepadButtonBack:
		return shortcutGamepadBack
	default:
		return 0
	}
}

func setShortcutModifierState(key sdl.KeyCode, pressed bool) {
	switch key {
	case sdl.KeyCodeLCtrl, sdl.KeyCodeRCtrl:
		shortcutCtrlDown = pressed
	case sdl.KeyCodeLShift, sdl.KeyCodeRShift:
		shortcutShiftDown = pressed
	case sdl.KeyCodeLAlt, sdl.KeyCodeRAlt:
		shortcutAltDown = pressed
	}
}

// toggleUI runs cmd.ToggleUI on its own goroutine: this is called from the
// same goroutine that drives the window dispatcher's event loop, and
// cmd.ToggleUI blocks until the dispatcher processes it.
func toggleUI(ctx context.Context, c *cmd.SISRContext) {
	go cmd.ToggleUI(ctx, c)
}
