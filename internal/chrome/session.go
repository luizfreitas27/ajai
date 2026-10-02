package chrome

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// cookieFiles are where Chrome keeps the default profile's cookies: Network/ since
// Chrome 96, the profile root before.
var cookieFiles = []string{filepath.Join("Default", "Network", "Cookies"), filepath.Join("Default", "Cookies")}

// chromeTime is t as Chrome stores cookie times: microseconds since 1601-01-01
// UTC. (time.Sub cannot do it: a Duration spans only 292 years.)
func chromeTime(t time.Time) int64 {
	const unixFrom1601 = 11644473600 // seconds from 1601-01-01 to 1970-01-01
	return (t.Unix()+unixFrom1601)*1_000_000 + int64(t.Nanosecond()/1000)
}

// HasLinkedInSession reports whether a profile holds an unexpired LinkedIn
// session cookie (li_at), without starting Chrome. It reads a copy of the cookie
// database, so a Chrome running on the profile does not get in the way. Only
// names and expiry are read; cookie values are never touched.
func HasLinkedInSession(profileDir string, now time.Time) (bool, error) {
	var src string
	for _, f := range cookieFiles {
		if _, err := os.Stat(filepath.Join(profileDir, f)); err == nil {
			src = filepath.Join(profileDir, f)
			break
		}
	}
	if src == "" {
		return false, nil // a profile never used: no cookies at all
	}
	tmp, err := os.CreateTemp("", "aja-cookies-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	in, err := os.Open(src)
	if err != nil {
		_ = tmp.Close()
		return false, err
	}
	_, err = io.Copy(tmp, in)
	_ = in.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	db, err := sql.Open("sqlite", "file:"+tmp.Name()+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM cookies WHERE name = 'li_at' AND (host_key = 'linkedin.com' OR host_key LIKE '%.linkedin.com')
		AND (expires_utc = 0 OR expires_utc > ?)`, chromeTime(now)).Scan(&n)
	if err != nil {
		return false, errors.New("unreadable cookie database: " + err.Error())
	}
	return n > 0, nil
}
