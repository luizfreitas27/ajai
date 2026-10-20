// Package messages holds the pure parts of the recruiter messages: rendering a
// market's template for a job and hirer, and the digests that tie an approval to
// the exact text the user read.
package messages

import (
	"fmt"
	"hash/fnv"
)

// Digest is the short fingerprint of a text an approval quotes back: FNV-1a 32
// in eight hex digits. It is not a security check, only a check that the text
// has not changed since it was shown.
func Digest(text string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(text))
	return fmt.Sprintf("%08x", h.Sum32())
}
