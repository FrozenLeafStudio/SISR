package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestHandler(buf *bytes.Buffer, color bool) *colorHandler {
	return &colorHandler{w: buf, level: LevelTrace, color: color}
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
