package questions

import (
	"strconv"
	"strings"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/textmatch"
)

// Skill years (questions.skill_years) answer questions about one technology or
// role: how many years the user has worked with it, and Yes/No questions about
// experience or knowledge with it. A listed name matches whole, whatever its
// case and accents; a question naming none is left to the memory, the AI and
// the review queue, as before.
var (
	// durationTerms: the question asks how long.
	durationTerms = []string{"years", "year", "how long", "anos", "ano", "quanto tempo", "tempo de experiencia"}
	// experienceTerms: a Yes/No question asks about experience or knowledge with something.
	experienceTerms = []string{"experience", "experienced", "familiar", "familiarity", "knowledge", "proficient", "hands-on",
		"worked with", "have you used", "experiencia", "conhecimento", "conhecimentos", "dominio", "vivencia",
		"familiaridade", "ja trabalhou", "ja utilizou", "ja usou", "ja desenvolveu"}
)

// unaccent folds the accented letters Portuguese and Spanish use, after lowering the case.
var unaccent = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ç", "c", "ñ", "n")

func fold(text string) string { return unaccent.Replace(strings.ToLower(text)) }

// hasFolded is textmatch.Has ignoring accents; the terms are written without them.
func hasFolded(label string, terms ...string) bool { return textmatch.Has(fold(label), terms...) }

// skillsNamed are the listed skills the label names.
func (a *Answerer) skillsNamed(label string) []config.SkillYears {
	var named []config.SkillYears
	for _, entry := range a.Cfg.Questions.SkillYears {
		if s, ok := config.ParseSkillYears(entry); ok && textmatch.Has(fold(label), fold(s.Name)) {
			named = append(named, s)
		}
	}
	return named
}

// skillYears answers a question asking how long the user has worked with
// listed skills: the smallest of their years, or "" (also for a sensitive
// question, which only its own configured answer ever answers).
func (a *Answerer) skillYears(label string) string {
	if IsSensitive(label) || !hasFolded(label, durationTerms...) {
		return ""
	}
	named := a.skillsNamed(label)
	if len(named) == 0 {
		return ""
	}
	least := named[0]
	for _, s := range named[1:] {
		if s.Years < least.Years {
			least = s
		}
	}
	answer := strconv.Itoa(least.Years)
	a.Logger.Info("answered from skill years", "question", label, "skill", least.Name, "answer", answer)
	return answer
}

// skillYes reports whether a Yes/No question asks about experience or
// knowledge with listed skills and has an option meaning yes: then it is
// answered yes. Never no: a skill not listed may just be missing from the list.
func (a *Answerer) skillYes(label string, options []string) bool {
	if IsSensitive(label) || !hasFolded(label, experienceTerms...) || MatchAnswerToOption("Yes", options) < 0 {
		return false
	}
	named := a.skillsNamed(label)
	if len(named) == 0 {
		return false
	}
	a.Logger.Info("answered from skill years", "question", label, "skill", named[0].Name, "answer", "Yes")
	return true
}
