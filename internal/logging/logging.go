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
	"sync"
	"time"
)

// FileName is the log file for a given day.
func FileName(dir string, day time.Time) string {
	return filepath.Join(dir, "aja-"+day.Format("2006-01-02")+".log")
}

// DaemonFileName is the daemon's log file for a given day.
func DaemonFileName(dir string, day time.Time) string {
	return filepath.Join(dir, "daemon-"+day.Format("2006-01-02")+".log")
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

// OpenDaily is Open for a process that lives for days (the daemon): it writes
// to name(dir, day) and moves to the next day's file after midnight, so each
// day's file holds that day.
func OpenDaily(dir string, name func(dir string, day time.Time) string, level slog.Level) (*slog.Logger, io.Closer, error) {
	d := &dailyFile{dir: dir, name: name, now: time.Now}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("log directory: %w", err)
	}
	if _, err := d.file(); err != nil {
		return nil, nil, err
	}
	return slog.New(slog.NewJSONHandler(d, &slog.HandlerOptions{Level: level})), d, nil
}

type dailyFile struct {
	mu   sync.Mutex
	dir  string
	name func(string, time.Time) string
	now  func() time.Time
	path string
	f    *os.File
}

// file returns today's file, opening it (and closing yesterday's) when the day changed.
func (d *dailyFile) file() (*os.File, error) {
	path := d.name(d.dir, d.now())
	if d.f != nil && path == d.path {
		return d.f, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if d.f != nil {
		_ = d.f.Close()
	}
	d.f, d.path = f, path
	return f, nil
}

func (d *dailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, err := d.file()
	if err != nil {
		return 0, err
	}
	return f.Write(p)
}

func (d *dailyFile) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return nil
	}
	err := d.f.Close()
	d.f = nil
	return err
}
