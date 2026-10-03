package store

import (
	"strings"
	"time"
)

// Timestamp is the one format every timestamp column uses: RFC 3339 with the
// local offset, which sorts correctly as text.
func Timestamp(t time.Time) string { return t.Format(time.RFC3339) }

// pythonLayouts are the formats the Python bot wrote: datetime.now() with and
// without microseconds, dates alone, and the memory's second-precision ISO form.
var pythonLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05.999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseTime reads any timestamp the Python bot or ajai wrote, in local time when
// no offset is present. ok is false for blanks and words such as "Pending".
func ParseTime(text string) (t time.Time, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return time.Time{}, false
	}
	for _, layout := range pythonLayouts {
		if parsed, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// NormalizeTimestamp rewrites a Python timestamp as Timestamp; unparseable text
// comes back unchanged so nothing is lost.
func NormalizeTimestamp(text string) string {
	if t, ok := ParseTime(text); ok {
		return Timestamp(t)
	}
	return text
}
