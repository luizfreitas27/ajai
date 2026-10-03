// Package textmatch is the whole-word matching every label and option test in
// ajai uses, ported from the Python find_bad_word. A plain substring scan made
// "java" skip every JavaScript role, matched "state" inside "United States" and
// "no" inside "Non-citizen"; boundaries fix all three, but only on the
// alphanumeric edges of the phrase, so ".NET" still matches "ASP.NET" while not
// matching ".NETWORK".
package textmatch

import (
	"strings"
	"unicode"
)

// isWordRune mirrors Python's \w: letters (any script), digits and underscore.
func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// FindWord returns the first of words that occurs in text as a whole phrase,
// case-insensitively, or "" when none does. The returned value is the word as
// given (trimmed), so callers can log which term fired.
func FindWord(text string, words []string) string {
	lowered := []rune(strings.ToLower(text))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if containsPhrase(lowered, []rune(strings.ToLower(word))) {
			return word
		}
	}
	return ""
}

// Has reports whether any of words occurs in text as a whole phrase.
func Has(text string, words ...string) bool { return FindWord(text, words) != "" }

func containsPhrase(text, phrase []rune) bool {
	needLeft := isWordRune(phrase[0])
	needRight := isWordRune(phrase[len(phrase)-1])
	for start := 0; start+len(phrase) <= len(text); start++ {
		if !equalRunes(text[start:start+len(phrase)], phrase) {
			continue
		}
		if needLeft && start > 0 && isWordRune(text[start-1]) {
			continue
		}
		end := start + len(phrase)
		if needRight && end < len(text) && isWordRune(text[end]) {
			continue
		}
		return true
	}
	return false
}

func equalRunes(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Normalise is the answer memory's key function, the Python memory's normalise:
// lowercase, everything but ASCII letters and digits becomes a space, spaces
// collapsed. Accented letters become spaces too, exactly as the Python regex did.
func Normalise(label string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Ratio is difflib.SequenceMatcher(None, a, b).ratio(): twice the number of
// matching characters found by recursively taking the longest common substring,
// over the total length. 1.0 for identical strings. Like difflib's "autojunk",
// when b has 200 characters or more, characters that make up more than 1% of it
// are ignored as anchors, so two long consent texts do not look alike just
// because they share the same common letters.
func Ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	total := len(ra) + len(rb)
	if total == 0 {
		return 1
	}
	return 2 * float64(matches(ra, rb, popular(rb))) / float64(total)
}

// AutojunkMinLength is difflib's threshold for the popular-character rule.
const AutojunkMinLength = 200

func popular(b []rune) map[rune]bool {
	if len(b) < AutojunkMinLength {
		return nil
	}
	counts := map[rune]int{}
	for _, r := range b {
		counts[r]++
	}
	limit := len(b)/100 + 1
	junk := map[rune]bool{}
	for r, n := range counts {
		if n > limit {
			junk[r] = true
		}
	}
	return junk
}

func matches(a, b []rune, junk map[rune]bool) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	ai, bj, size := longestCommon(a, b, junk)
	if size == 0 {
		return 0
	}
	return size + matches(a[:ai], b[:bj], junk) + matches(a[ai+size:], b[bj+size:], junk)
}

// longestCommon is the earliest longest common substring, like difflib's
// find_longest_match; junk characters in b never start or extend a block.
func longestCommon(a, b []rune, junk map[rune]bool) (bestA, bestB, bestSize int) {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] && !junk[b[j-1]] {
				cur[j] = prev[j-1] + 1
				if cur[j] > bestSize {
					bestSize = cur[j]
					bestA, bestB = i-cur[j], j-cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
		for j := range cur {
			cur[j] = 0
		}
	}
	return bestA, bestB, bestSize
}
