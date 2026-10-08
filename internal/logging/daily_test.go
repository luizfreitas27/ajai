package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheDailyFileMovesToTheNextDayAfterMidnight(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 2, 23, 59, 0, 0, time.Local)
	d := &dailyFile{dir: dir, name: DaemonFileName, now: func() time.Time { return day }}
	if _, err := d.Write([]byte("friday\n")); err != nil {
		t.Fatal(err)
	}
	day = day.Add(2 * time.Minute)
	if _, err := d.Write([]byte("saturday\n")); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	for file, want := range map[string]string{"daemon-2026-10-02.log": "friday\n", "daemon-2026-10-03.log": "saturday\n"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(data) != want {
			t.Errorf("%s: %q %v", file, data, err)
		}
	}
}

func TestOpenDailyWritesJSONLines(t *testing.T) {
	dir := t.TempDir()
	logger, closer, err := OpenDaily(dir, DaemonFileName, ParseLevel("INFO"))
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("daemon started", "pid", 1)
	_ = closer.Close()
	data, _ := os.ReadFile(DaemonFileName(dir, time.Now()))
	if !strings.Contains(string(data), `"msg":"daemon started"`) {
		t.Errorf("%s", data)
	}
}

func TestTheLogDirectoryIsTheUsersAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	_, c, err := Open(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("the log directory is %o", info.Mode().Perm())
	}
}
