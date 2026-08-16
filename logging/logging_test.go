package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/slogtest"
	"time"
)

func newTestHandler(buf *bytes.Buffer, color bool) *colorHandler {
	return &colorHandler{w: buf, level: LevelTrace, color: color}
}

func TestHandlerKeepsAttrsFromWith(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(newTestHandler(buf, false)).With("gamepadID", 5, "appID", 4242)

	logger.Info("assigned")

	out := buf.String()
	for _, want := range []string{"assigned", "gamepadID=5", "appID=4242"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
}

func TestHandlerQualifiesGroupedAttrs(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(newTestHandler(buf, false)).WithGroup("window").With("hitTest", true)

	logger.Info("toggled", "fullscreen", false)

	out := buf.String()
	for _, want := range []string{"window.hitTest=true", "window.fullscreen=false"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
}

func TestHandlerKeepsAttrsAddedBeforeGroup(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(newTestHandler(buf, false)).With("appID", 42).WithGroup("window")

	logger.Info("toggled", "hitTest", true)

	out := buf.String()
	if !strings.Contains(out, "appID=42") {
		t.Fatalf("attr added before the group should stay unqualified: %q", out)
	}
	if !strings.Contains(out, "window.hitTest=true") {
		t.Fatalf("attr added after the group should be qualified: %q", out)
	}
}

// A logger derived twice must not have the second branch's attributes leak
// into the first.
func TestHandlerDerivedLoggersDoNotShareAttrs(t *testing.T) {
	buf := &bytes.Buffer{}
	base := slog.New(newTestHandler(buf, false)).With("appID", 42)
	base.With("gamepadID", 1)

	base.Info("assigned")

	if strings.Contains(buf.String(), "gamepadID") {
		t.Fatalf("a sibling logger's attribute leaked: %q", buf.String())
	}
}

func TestHandlerInlinesGroupValues(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(newTestHandler(buf, false))

	logger.Info("device", slog.Group("ids", "vid", "0x045e", "pid", "0x028e"))

	out := buf.String()
	for _, want := range []string{"ids.vid=0x045e", "ids.pid=0x028e"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
}

func TestHandlerOmitsAnsiWhenColorDisabled(t *testing.T) {
	buf := &bytes.Buffer{}
	slog.New(newTestHandler(buf, false)).Error("boom", "error", "eof")

	if strings.Contains(buf.String(), "\033[") {
		t.Fatalf("expected no ANSI escapes, got %q", buf.String())
	}
}

func TestHandlerWritesAnsiWhenColorEnabled(t *testing.T) {
	buf := &bytes.Buffer{}
	slog.New(newTestHandler(buf, true)).Error("boom", "error", "eof")

	if !strings.Contains(buf.String(), "\033[") {
		t.Fatalf("expected ANSI escapes, got %q", buf.String())
	}
}

// TestHandlerConformsToSlogSpec runs the standard library's handler test
// suite. Attributes are parsed back out of the rendered line, which is
// unambiguous here because the suite only logs unquoted scalar values.
func TestHandlerConformsToSlogSpec(t *testing.T) {
	buf := &bytes.Buffer{}
	h := newTestHandler(buf, false)

	results := func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
			fields := strings.Fields(line)
			// time, level, message, then key=value pairs.
			entry := map[string]any{
				slog.TimeKey:    fields[0],
				slog.LevelKey:   fields[1],
				slog.MessageKey: fields[2],
			}
			if fields[0] == (time.Time{}).Format("2006-01-02T15:04:05.000000Z07:00") {
				delete(entry, slog.TimeKey)
			}
			for _, f := range fields[3:] {
				k, v, _ := strings.Cut(f, "=")
				nest := entry
				keys := strings.Split(k, ".")
				for _, g := range keys[:len(keys)-1] {
					sub, ok := nest[g].(map[string]any)
					if !ok {
						sub = map[string]any{}
						nest[g] = sub
					}
					nest = sub
				}
				nest[keys[len(keys)-1]] = v
			}
			out = append(out, entry)
		}
		return out
	}

	if err := slogtest.TestHandler(h, results); err != nil {
		t.Fatal(err)
	}
}

func TestUseColorSkipsNonCharDevices(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "sisr.log"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close() //nolint:errcheck

	if useColor(f) {
		t.Fatal("a regular file must not be colored")
	}
}

func TestUseColorHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	if useColor(os.Stdout) {
		t.Fatal("NO_COLOR must suppress color regardless of the destination")
	}
}
