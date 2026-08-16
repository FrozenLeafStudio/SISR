package cmd

import "testing"

func TestUIIsVisible(t *testing.T) {
	cases := []struct {
		name           string
		windowHidden   bool
		webviewVisible bool
		hitTest        bool
		kbmEnabled     bool
		want           bool
	}{
		{
			name:         "fullscreen overlay at startup is not the UI",
			windowHidden: false,
			hitTest:      false,
			want:         false,
		},
		{
			name:           "fullscreen overlay with the UI up",
			windowHidden:   false,
			webviewVisible: true,
			hitTest:        true,
			want:           true,
		},
		{
			// A toggle arriving before the queued wv.SetVisible ran used to
			// read the UI as hidden and show it again.
			name:           "fullscreen overlay while the webview flag lags behind",
			windowHidden:   false,
			webviewVisible: false,
			hitTest:        true,
			want:           true,
		},
		{
			name:         "windowed and hidden at startup",
			windowHidden: true,
			hitTest:      true,
			want:         false,
		},
		{
			name:           "windowed and hidden after the UI was closed",
			windowHidden:   true,
			webviewVisible: true,
			hitTest:        true,
			want:           false,
		},
		{
			name:           "windowed with the UI up",
			windowHidden:   false,
			webviewVisible: true,
			hitTest:        true,
			want:           true,
		},
		{
			// With kbm emulation on, the overlay hit-tests from startup and the
			// hide branch never clears it, so hitTest is stuck true. Treating
			// that as "UI is up" would send every toggle down the hide branch
			// and the UI could never be opened.
			name:           "fullscreen overlay with kbm emulation and the UI down",
			windowHidden:   false,
			webviewVisible: false,
			hitTest:        true,
			kbmEnabled:     true,
			want:           false,
		},
		{
			name:           "fullscreen overlay with kbm emulation and the UI up",
			windowHidden:   false,
			webviewVisible: true,
			hitTest:        true,
			kbmEnabled:     true,
			want:           true,
		},
		{
			// The Steam overlay callback also sets hitTest true while its
			// overlay is open (api/handler/steam/cef/overlaychange.go), which
			// this predicate cannot tell apart from the SISR UI being up. A
			// toggle here hides hit-testing for the open Steam overlay.
			name:           "fullscreen overlay with the Steam overlay open and kbm emulation off",
			windowHidden:   false,
			webviewVisible: false,
			hitTest:        true,
			kbmEnabled:     false,
			want:           true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uiIsVisible(tc.windowHidden, tc.webviewVisible, tc.hitTest, tc.kbmEnabled)
			if got != tc.want {
				t.Fatalf("uiIsVisible(windowHidden=%v, webviewVisible=%v, hitTest=%v, kbmEnabled=%v) = %v, want %v",
					tc.windowHidden, tc.webviewVisible, tc.hitTest, tc.kbmEnabled, got, tc.want)
			}
		})
	}
}
