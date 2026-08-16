package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// LevelTrace defines a custom slog level below Debug for very verbose output.
const LevelTrace slog.Level = -8

func ParseLevel(s string) slog.Level {
	switch s {
	case "trace":
		return LevelTrace
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetupLogger builds a slog.Logger with console and optional file handlers.
func SetupLogger(logLevel, logFile string) (*slog.Logger, []io.Closer, error) {
	level := ParseLevel(logLevel)
	var handlers []slog.Handler

	stdoutHandler := &colorHandler{w: os.Stdout, level: level, color: useColor(os.Stdout)}
	handlers = append(handlers, LevelFilter{pass: func(l slog.Level) bool { return l < slog.LevelError }, h: stdoutHandler})
	stderrHandler := &colorHandler{w: os.Stderr, level: slog.LevelError, color: useColor(os.Stderr)}
	handlers = append(handlers, LevelFilter{pass: func(l slog.Level) bool { return l >= slog.LevelError }, h: stderrHandler})
	var closeFiles []io.Closer
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, err
		}
		closeFiles = append(closeFiles, f)
		handlers = append(handlers, slog.NewTextHandler(f, &slog.HandlerOptions{Level: level}))
	}
	logger := slog.New(MultiHandler{hs: handlers})
	slog.SetDefault(logger)
	return logger, closeFiles, nil
}

// MultiHandler fans out records to multiple handlers.
type MultiHandler struct{ hs []slog.Handler }

func (m MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.hs {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}
func (m MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.hs {
		_ = h.Handle(ctx, r)
	}
	return nil
}
func (m MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithAttrs(attrs)
	}
	return MultiHandler{hs: out}
}
func (m MultiHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithGroup(name)
	}
	return MultiHandler{hs: out}
}

// LevelFilter delegates to an underlying handler but filters which levels are
// passed to it using the provided predicate.
type LevelFilter struct {
	pass func(slog.Level) bool
	h    slog.Handler
}

func (f LevelFilter) Enabled(ctx context.Context, level slog.Level) bool {
	if !f.pass(level) {
		return false
	}
	return f.h.Enabled(ctx, level)
}

func (f LevelFilter) Handle(ctx context.Context, r slog.Record) error {
	if !f.pass(r.Level) {
		return nil
	}
	return f.h.Handle(ctx, r)
}

func (f LevelFilter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return LevelFilter{pass: f.pass, h: f.h.WithAttrs(attrs)}
}
func (f LevelFilter) WithGroup(name string) slog.Handler {
	return LevelFilter{pass: f.pass, h: f.h.WithGroup(name)}
}

const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[90m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiGreen  = "\033[32m"
	ansiBlue   = "\033[34m"
	ansiPurple = "\033[35m"
)

// useColor reports whether ANSI escapes should be written to f. Color is
// suppressed when f is not a character device (a redirected file or a pipe)
// and when NO_COLOR is set.
func useColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

type colorHandler struct {
	w     io.Writer
	level slog.Leveler
	color bool
}

func (h *colorHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// paint returns code only when this handler writes color.
func (h *colorHandler) paint(code string) string {
	if !h.color {
		return ""
	}
	return code
}

func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return ansiRed
	case l >= slog.LevelWarn:
		return ansiYellow
	case l >= slog.LevelInfo:
		return ansiGreen
	case l >= slog.LevelDebug:
		return ansiBlue
	case l >= LevelTrace:
		return ansiPurple
	default:
		return ansiReset
	}
}

func (h *colorHandler) Handle(_ context.Context, r slog.Record) error {
	buf := strings.Builder{}

	buf.WriteString(h.paint(ansiDim))
	buf.WriteString(r.Time.Format("2006-01-02T15:04:05.000000Z07:00"))
	buf.WriteString(h.paint(ansiReset))
	buf.WriteString(" ")

	buf.WriteString(h.paint(levelColor(r.Level)))
	fmt.Fprintf(&buf, "%5s", r.Level.String())
	buf.WriteString(h.paint(ansiReset))

	buf.WriteString(" ")
	buf.WriteString(r.Message)

	r.Attrs(func(a slog.Attr) bool {
		buf.WriteString(" ")
		buf.WriteString(h.paint(ansiDim))
		buf.WriteString(a.Key)
		buf.WriteString("=")
		buf.WriteString(h.paint(ansiReset))
		buf.WriteString(a.Value.String())
		return true
	})

	buf.WriteString("\n")
	_, err := io.WriteString(h.w, buf.String())
	return err
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *colorHandler) WithGroup(name string) slog.Handler {
	return h
}
