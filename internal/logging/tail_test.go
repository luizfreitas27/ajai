package logging

import (
	"os"
	"testing"
	"time"
)

func TestTailFollowsAppendsTruncationAndMidnight(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 2, 23, 0, 0, 0, time.Local)
	path := FileName(dir, day)
	tail := NewTail(FileName, dir, day, 0)
	read := func(now time.Time) string {
		t.Helper()
		b, err := tail.Read(now)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read(day); got != "" {
		t.Errorf("a missing file reads as nothing: %q", got)
	}
	appendTo := func(p, text string) {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(text)
		_ = f.Close()
	}
	appendTo(path, "one\n")
	appendTo(path, "two\n")
	if got := read(day); got != "one\ntwo\n" {
		t.Errorf("%q", got)
	}
	appendTo(path, "three\n")
	if got := read(day); got != "three\n" {
		t.Errorf("only what was appended: %q", got)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil { // truncated and rewritten
		t.Fatal(err)
	}
	if got := read(day); got != "new\n" {
		t.Errorf("after truncation the file is read from the start: %q", got)
	}
	appendTo(path, "half")
	if got := read(day); got != "half" {
		t.Errorf("%q", got)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil { // truncated mid-line
		t.Fatal(err)
	}
	if got := read(day); got != "\nx\n" {
		t.Errorf("a half-read line is ended before the file is read again: %q", got)
	}

	appendTo(path, "just before midnight\nhalf")
	tomorrow := day.Add(2 * time.Hour)
	appendTo(FileName(dir, tomorrow), "saturday\n")
	if got := read(tomorrow); got != "just before midnight\nhalf\nsaturday\n" {
		t.Errorf("after midnight: the rest of the day's file, its half line ended, then the next day's: %q", got)
	}

	skip := NewTail(FileName, dir, tomorrow, int64(len("saturday\n")))
	if b, _ := skip.Read(tomorrow); len(b) != 0 {
		t.Errorf("an offset skips what is already there: %q", b)
	}
}
