package questions

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/textmatch"
)

// Skill years (questions.skill_years) answer questions about one technology or
// role: how long the user has worked with it, and Yes/No questions about
// experience or knowledge with it. Only the sentence a label asks counts, its
// links taken out, so a skill named in context (an intro, a link, a note after
// the question) is not taken for its subject. A listed name matches whole,
// whatever its case and accents ("react" is not "React-Native"). A question
// naming no listed skill is left to the memory, the AI and the review queue.

var (
	links = regexp.MustCompile(`https?://\S+`)
	// innerHyphen joins a hyphenated word, so a name never matches half of it.
	innerHyphen = regexp.MustCompile(`([\pL\pN])-([\pL\pN])`)
	// yearsAsked is a number of years a question names ("5+ years", "3 anos").
	yearsAsked = regexp.MustCompile(`(\d+)\s*\+?\s*(?:years|yrs|anos)`)
	digits     = regexp.MustCompile(`\d`)
)

// skillText is text as skill names are looked for in it: folded, hyphenated words kept whole.
func skillText(text string) string {
	text = textmatch.Fold(text)
	for innerHyphen.MatchString(text) {
		text = innerHyphen.ReplaceAllString(text, "${1}_${2}")
	}
	return text
}

// askedText is what a label asks: its sentences ending in a question mark (all
// of it when none does), without links.
func askedText(label string) string {
	runes := []rune(links.ReplaceAllString(label, " "))
	var asked []string
	start := 0
	for i, r := range runes {
		if (r == '.' || r == '!' || r == '?') && (i == len(runes)-1 || unicode.IsSpace(runes[i+1])) {
			if r == '?' {
				asked = append(asked, string(runes[start:i+1]))
			}
			start = i + 1
		}
	}
	if len(asked) == 0 {
		return string(runes)
	}
	return strings.Join(asked, " ")
}

// listedSkills parses questions.skill_years, already validated: an entry it
// cannot read is left out.
func listedSkills(entries []string) []config.Skill {
	var skills []config.Skill
	for _, entry := range entries {
		if s, ok := config.ParseSkill(entry); ok {
			skills = append(skills, s)
		}
	}
	return skills
}

// skillsAsked are the listed skills an asked text names, and the fewest years among them.
func (a *Answerer) skillsAsked(asked string) (names []string, fewest int) {
	text := skillText(asked)
	for _, s := range a.skills {
		if textmatch.Has(text, skillText(s.Name)) {
			if len(names) == 0 || s.Years < fewest {
				fewest = s.Years
			}
			names = append(names, s.Name)
		}
	}
	return names, fewest
}

// yearsFromSkills answers a text question asking how long the user has used
// or worked with listed skills: the fewest years among those it names, or ""
// (always for a sensitive question, which only its own configured answer
// ever answers).
func (a *Answerer) yearsFromSkills(label string) string {
	asked := askedText(label)
	if IsSensitive(label) || !textmatch.HasFolded(asked, skillDurationTerms...) ||
		!textmatch.HasFolded(asked, skillUseTerms...) || textmatch.HasFolded(asked, skillDurationExclusions...) {
		return ""
	}
	names, fewest := a.skillsAsked(asked)
	if len(names) == 0 {
		return ""
	}
	a.Logger.Info("answered from skill years", "question", label, "skills", names, "years", fewest)
	return strconv.Itoa(fewest)
}

// yesFromSkills reports whether a Yes/No question asks about experience or
// knowledge with listed skills, and can be answered yes: it has an option
// meaning yes and no ranges of years, and any number of years it asks for is a
// floor the user's years reach. Never no: a skill not listed may just be
// missing from the list.
func (a *Answerer) yesFromSkills(label string, options []string) bool {
	asked := askedText(label)
	if IsSensitive(label) || !textmatch.HasFolded(asked, skillExperienceTerms...) || MatchAnswerToOption("Yes", options) < 0 {
		return false
	}
	for _, o := range options {
		if !IsPlaceholder(o) && digits.MatchString(o) { // "Sim, mais de 5 anos": ranges, not yes or no
			return false
		}
	}
	names, fewest := a.skillsAsked(asked)
	if len(names) == 0 {
		return false
	}
	if m := yearsAsked.FindStringSubmatch(textmatch.Fold(asked)); m != nil {
		floor, _ := strconv.Atoi(m[1])
		if fewest < floor || textmatch.HasFolded(asked, skillCapTerms...) {
			return false
		}
	}
	a.Logger.Info("answered yes from skill years", "question", label, "skills", names)
	return true
}
