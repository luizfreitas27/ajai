package logging

import (
	"io"
	"os"
	"time"
)

// Tail reads what is appended to a day's log file, surviving truncation (the
// offset resets) and midnight (the file name changes with the day).
type Tail struct {
	name   FileNamer
	dir    string
	path   string
	offset int64
}

// NewTail follows the log name gives in dir for day, from offset bytes in.
func NewTail(name FileNamer, dir string, day time.Time, offset int64) *Tail {
	return &Tail{name: name, dir: dir, path: name(dir, day), offset: offset}
}

// Read returns what was appended since the last call, moving to now's file
// when the day has changed. A missing file reads as nothing.
func (t *Tail) Read(now time.Time) ([]byte, error) {
	if today := t.name(t.dir, now); today != t.path {
		t.path, t.offset = today, 0
	}
	f, err := os.Open(t.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err == nil && info.Size() < t.offset {
		t.offset = 0
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	t.offset += int64(len(data))
	return data, err
}
