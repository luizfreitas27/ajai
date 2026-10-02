// Package logging writes a structured JSON log file per day in the data
// directory. Terminal output is the ui package's job; the file is for reading
// after the fact and for `aja logs -f`.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileName is the log file for a given day.
func FileName(dir string, day time.Time) string {
	return filepath.Join(dir, "aja-"+day.Format("2006-01-02")+".log")
}

// ParseLevel maps the config's log_level to slog.
func ParseLevel(text string) slog.Level {
	switch strings.ToUpper(text) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Open creates the day's log file and returns a logger writing JSON to it. The
// caller closes the returned io.Closer. extra, when non-nil, also receives the
// lines (tests).
func Open(dir string, level slog.Level, extra io.Writer) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("log directory: %w", err)
	}
	f, err := os.OpenFile(FileName(dir, time.Now()), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	var w io.Writer = f
	if extra != nil {
		w = io.MultiWriter(f, extra)
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})), f, nil
}

// Discard is a logger that drops everything, for commands that must not touch
// the data directory (version) and for tests.
var Discard = slog.New(slog.NewTextHandler(io.Discard, nil))
