package chrome

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cookieProfile writes a profile whose cookie database holds rows of (host, name, expires).
func cookieProfile(t *testing.T, sub string, rows [][3]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, sub)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO cookies (host_key, name, value, encrypted_value, expires_utc) VALUES (?, ?, '', x'00', ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestChromeTimeCountsMicrosecondsSince1601(t *testing.T) {
	// 2026-10-02T12:00:00Z, as Chrome writes it.
	if got := chromeTime(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)); got != 13435416000000000 {
		t.Errorf("%d", got)
	}
}

func TestALinkedInSessionIsAnUnexpiredLiAtCookie(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	future := chromeTime(now.Add(365 * 24 * time.Hour))
	past := chromeTime(now.Add(-time.Hour))
	network := filepath.Join("Default", "Network", "Cookies")
	old := filepath.Join("Default", "Cookies")
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"signed in", cookieProfile(t, network, [][3]any{{".www.linkedin.com", "li_at", future}}), true},
		{"older Chrome layout", cookieProfile(t, old, [][3]any{{".linkedin.com", "li_at", future}}), true},
		{"session cookie", cookieProfile(t, network, [][3]any{{".www.linkedin.com", "li_at", 0}}), true},
		{"expired", cookieProfile(t, network, [][3]any{{".www.linkedin.com", "li_at", past}}), false},
		{"other linkedin cookies only", cookieProfile(t, network, [][3]any{{".linkedin.com", "bcookie", future}}), false},
		{"li_at of another site", cookieProfile(t, network, [][3]any{{".notlinkedin.com", "li_at", future}}), false},
		{"a profile never used", t.TempDir(), false},
	}
	for _, tc := range cases {
		got, err := HasLinkedInSession(tc.dir, now)
		if err != nil || got != tc.want {
			t.Errorf("%s: %v %v, want %v", tc.name, got, err, tc.want)
		}
	}
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, "Default", "Network"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, network), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HasLinkedInSession(bad, now); err == nil {
		t.Error("a corrupt database is an error, not a missing session")
	}
}

func TestTheEverydayProfileFollowsTheBrowserFound(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/u/.config")
	cases := map[string]string{
		"/usr/bin/chromium":               "/home/u/.config/chromium",
		"/usr/bin/chromium-browser":       "/home/u/.config/chromium",
		"/usr/bin/google-chrome-stable":   "/home/u/.config/google-chrome",
		"/usr/bin/google-chrome":          "/home/u/.config/google-chrome",
		"/usr/bin/google-chrome-beta":     "/home/u/.config/google-chrome-beta",
		"/usr/bin/google-chrome-unstable": "/home/u/.config/google-chrome-unstable",
		"":                                "/home/u/.config/google-chrome", // Chrome not found
	}
	for bin, want := range cases {
		if got := EverydayProfileDir(bin); got != want {
			t.Errorf("%q: %q, want %q", bin, got, want)
		}
	}
}
