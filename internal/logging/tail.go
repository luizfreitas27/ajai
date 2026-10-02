package logging

import (
	"io"
	"os"
	"time"
)

// Tail reads what is appended to a day's log file, surviving truncation (the
// file is read again from its start) and midnight (the rest of the day's file,
// then the next day's).
type Tail struct {
	name    FileNamer
	dir     string
	path    string
	offset  int64
	midLine bool // the last byte read did not end a line
}

// NewTail follows the log name gives in dir for day, from offset bytes in.
func NewTail(name FileNamer, dir string, day time.Time, offset int64) *Tail {
	return &Tail{name: name, dir: dir, path: name(dir, day), offset: offset}
}

// Read returns what was appended since the last call, moving to now's file
// when the day has changed. A missing file reads as nothing. A line left half
// written in a file that is left behind is ended, so it is never joined to the
// next file's first line.
func (t *Tail) Read(now time.Time) ([]byte, error) {
	var out []byte
	if today := t.name(t.dir, now); today != t.path {
		rest, err := t.read() // what the day's file gained before midnight
		if err != nil {
			return nil, err
		}
		out = t.startOver(rest)
		t.path = today
	}
	data, err := t.read()
	return append(out, data...), err
}

// read returns what the file gained since offset; a file now shorter than
// offset was truncated and is read from its start.
func (t *Tail) read() ([]byte, error) {
	f, err := os.Open(t.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []byte
	if info, err := f.Stat(); err == nil && info.Size() < t.offset {
		out = t.startOver(nil)
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	t.offset += int64(len(data))
	if len(data) > 0 {
		t.midLine = data[len(data)-1] != '\n'
	}
	return append(out, data...), err
}

// startOver goes back to the start of a file, ending a half-read line.
func (t *Tail) startOver(out []byte) []byte {
	t.offset = 0
	if t.midLine {
		t.midLine = false
		out = append(out, '\n')
	}
	return out
}
