package chrome

import (
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// HasLinkedInSession reports whether any profile in a Chrome user-data directory
// holds an unexpired LinkedIn session cookie (li_at), without starting Chrome.
// Chrome opens the profile used last, which is not always "Default", so every
// profile counts. The cookie databases are opened read-only in place, never
// copied, and only cookie names and expiry are read, never values.
func HasLinkedInSession(userDataDir string, now time.Time) (bool, error) {
	var files []string
	for _, pattern := range []string{
		filepath.Join(userDataDir, "*", "Network", "Cookies"), // since Chrome 96
		filepath.Join(userDataDir, "*", "Cookies"),            // before
	} {
		found, _ := filepath.Glob(pattern)
		files = append(files, found...)
	}
	var firstErr error
	for _, f := range files {
		ok, err := hasLiAt(f, now)
		if ok {
			return true, nil
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return false, firstErr
}

// hasLiAt looks for li_at in one cookie database. A Chrome running on the
// profile may hold it locked; then the file is read as it stands on disk.
func hasLiAt(path string, now time.Time) (bool, error) {
	n, err := countLiAt(path, "mode=ro", now)
	if err != nil {
		n, err = countLiAt(path, "mode=ro&immutable=1", now)
	}
	if err != nil {
		return false, errors.New("unreadable cookie database " + path + ": " + err.Error())
	}
	return n > 0, nil
}

func countLiAt(path, query string, now time.Time) (int, error) {
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String())
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM cookies WHERE name = 'li_at' AND (host_key = 'linkedin.com' OR host_key LIKE '%.linkedin.com')
		AND (expires_utc = 0 OR expires_utc > ?)`, chromeTime(now)).Scan(&n)
	return n, err
}

// chromeTime is t as Chrome stores cookie times: microseconds since 1601-01-01
// UTC. (time.Sub cannot do it: a Duration spans only 292 years.)
func chromeTime(t time.Time) int64 {
	const unixFrom1601 = 11644473600 // seconds from 1601-01-01 to 1970-01-01
	return (t.Unix()+unixFrom1601)*1_000_000 + int64(t.Nanosecond()/1000)
}
