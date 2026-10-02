package questions

import (
	"strings"
	"unicode"

	"github.com/luizfreitas27/aja/internal/textmatch"
)

// MatchAnswerToOption returns the index of the option that honestly carries
// answer, or -1. Whole words in both directions; a Yes never lands on a negated
// option and vice versa; an option that IS the phrase beats one that merely
// contains it; a decline answer goes to the option that itself declines.
// -1 means "no honest match": the caller leaves the control alone, never picks
// the first option.
func MatchAnswerToOption(answer string, options []string) int {
	if answer == "" {
		return -1
	}
	var phrases []string
	wantNegative := 0 // 0 = no polarity, 1 = wants a negated option, -1 = wants an affirmative one
	switch {
	case textmatch.Has(answer, declineTerms...):
		for i, option := range options {
			if textmatch.Has(option, declineTerms...) {
				return i
			}
		}
		phrases = []string{"Decline", "not wish", "don't wish", "Prefer not", "not want", "Prefiro não", "Prefiro nao", "Não desejo", "Não informar"}
	case textmatch.Has(answer, "yes", "sim"):
		phrases = []string{"Yes", "Agree", "I do", "I have", "Sim", "Concordo", "Tenho", "Possuo"}
		wantNegative = -1
	case textmatch.Has(answer, "no", "não", "nao"):
		phrases = []string{"No", "Disagree", "I don't", "I do not", "Não", "Nao", "Discordo", "Não tenho", "Não possuo"}
		wantNegative = 1
	default:
		phrases = []string{answer, strings.ToLower(answer), strings.ToUpper(answer), alnumOnly(answer)}
	}
	for _, phrase := range phrases {
		for i, option := range options {
			if strings.EqualFold(strings.TrimSpace(option), strings.TrimSpace(phrase)) {
				return i
			}
		}
	}
	for _, phrase := range phrases {
		for i, option := range options {
			if !textmatch.Has(option, phrase) && !textmatch.Has(phrase, option) {
				continue
			}
			if wantNegative != 0 {
				negated := textmatch.Has(option, negationTerms...)
				if (wantNegative == 1) != negated {
					continue
				}
			}
			return i
		}
	}
	return -1
}

// Choices are the options a person can pick: placeholders ("Select an option")
// left out. nil when there are none.
func Choices(options []string) []string {
	var out []string
	for _, o := range options {
		if !IsPlaceholder(o) {
			out = append(out, o)
		}
	}
	return out
}

func alnumOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// indexOfNormalised is indexOf with whitespace collapsed on both sides, the
// XPath normalize-space() the Python radio branch used.
func indexOfNormalised(options []string, text string) int {
	want := strings.Join(strings.Fields(text), " ")
	for i, o := range options {
		if strings.Join(strings.Fields(o), " ") == want {
			return i
		}
	}
	return -1
}

// indexOf returns the index of an option equal to text, or -1.
func indexOf(options []string, text string) int {
	for i, o := range options {
		if o == text {
			return i
		}
	}
	return -1
}

// PickOption is the option a person typed: the option itself, or the
// beginning of exactly one option, ignoring case and spacing. -1 when the text
// names none or several: what the user typed is refused rather than guessed
// (a Telegram reply, an answer typed in the TUI).
func PickOption(text string, options []string) int {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	want := norm(text)
	if want == "" {
		return -1
	}
	for i, o := range options {
		if norm(o) == want {
			return i
		}
	}
	found := -1
	for i, o := range options {
		if strings.HasPrefix(norm(o), want) {
			if found >= 0 {
				return -1
			}
			found = i
		}
	}
	return found
}
