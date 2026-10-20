package messages

import "testing"

// The digest is FNV-1a 32 in eight hex digits. The empty string is the FNV-1a
// offset basis, 0x811c9dc5; "a" is the published FNV-1a test vector 0xe40c292c.
func TestDigestIsFNV1a32InEightHexDigits(t *testing.T) {
	for text, want := range map[string]string{
		"":  "811c9dc5",
		"a": "e40c292c",
	} {
		if got := Digest(text); got != want {
			t.Errorf("Digest(%q) = %q, want %q", text, got, want)
		}
	}
}
