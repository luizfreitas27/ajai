package questions

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/textmatch"
)

// Kind is a form control kind.
type Kind string

// The control kinds, the same strings the answer memory uses.
const (
	Text     Kind = "text"
	Textarea Kind = "textarea"
	Select   Kind = "select"
	Radio    Kind = "radio"
	Checkbox Kind = "checkbox"
)

// UnknownTitle is what a control with no readable title is called.
const UnknownTitle = "Unknown"

// Question is one form control as the browser layer read it.
type Question struct {
	Label      string   // the visible title; for a checkbox, the box's own visible text
	GroupTitle string   // checkbox only: the hidden group title, "" when none
	Kind       Kind     //
	Options    []string // select and radio: the option texts in order
	Current    string   // select: selected text; radio: selected label; text/textarea: value; checkbox: "checked" or ""
}

// Job is what the current job contributes to answering.
type Job struct {
	Link         string
	WorkLocation string // the job's own location, for "where are you located" when no city is configured
	Description  string
}

// Action is what the browser layer should do with a control.
type Action int

// Actions.
const (
	Leave   Action = iota // nothing to do: prefilled, or not ours to touch
	Fill                  // type Value into a text or textarea control
	Choose                // pick Options[Option] in a select or radio
	Tick                  // tick the checkbox
	Blocked               // no honest answer: leave it, report it; the job may be skipped
)

// Decision is the outcome for one question.
type Decision struct {
	Action    Action
	Value     string // Fill: the text; Choose: the option text; Blocked/Leave: "" or the kept value
	Option    int    // Choose: index into Question.Options
	Typeahead bool   // Fill: the control is a typeahead that needs ARROW_DOWN + ENTER after typing
	Source    string // "config", "memory", "ai", "prefilled", ""
	Reason    string // Blocked: why, in words
}

// Memory is the slice of the answer memory the answerer uses.
type Memory interface {
	Lookup(label, kind string, approximate bool) (*memory.Remembered, bool, error)
	Remember(label, kind, answer, source, jobLink string) (*memory.Remembered, error)
	RecordUse(id, jobLink string) error
}

// AI answers an unrecognised question. With options it must name one verbatim
// or return ""; it is never asked a sensitive question.
type AI interface {
	Answer(question string, kind Kind, options []string, jobDescription string) (string, error)
}

// Answerer holds the configured answers and the fallbacks.
type Answerer struct {
	Cfg    config.Config
	Market markets.Resolved
	Memory Memory // nil: no memory
	AI     AI     // nil: AI off
	Logger *slog.Logger
}

// New builds an answerer; the market is resolved from the config.
func New(cfg config.Config, mem Memory, ai AI, logger *slog.Logger) *Answerer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Answerer{Cfg: cfg, Market: markets.Resolve(cfg), Memory: mem, AI: ai, Logger: logger}
}

// ------------------------------------------------------------------ classification

// WorkAuthorizationAnswer is the configured answer for a visa / authorization /
// citizenship question, else "". Three distinct questions: sponsorship first
// ("sponsor a new work visa or work authorization" is a sponsorship question),
// authorization before citizenship ("citizen or otherwise legally authorized"
// is a Yes/No question).
func (a *Answerer) WorkAuthorizationAnswer(label string) string {
	switch {
	case textmatch.Has(label, visaTerms...):
		return a.Cfg.Questions.RequireVisa
	case textmatch.Has(label, authorizationTerms...):
		return a.Cfg.Questions.LegallyAuthorized
	case textmatch.Has(label, citizenshipTerms...):
		return a.Cfg.Questions.Citizenship
	}
	return ""
}

// IsSensitive is true for a work-authorization, visa, citizenship, clearance,
// salary, disability or veteran question. Whole words.
func IsSensitive(label string) bool { return textmatch.Has(label, sensitiveTerms...) }

// AttestationTerm is the attestation/consent word a checkbox label carries, or "".
func AttestationTerm(label string) string { return textmatch.FindWord(label, attestationTerms) }

// CheckboxKey is the question the memory knows a checkbox by: its visible text
// prefixed by the group title when the form has one. "" when the box has no
// text at all, so it is never remembered.
func CheckboxKey(groupTitle, visibleText string) string {
	var parts []string
	for _, p := range []string{groupTitle, visibleText} {
		if p != "" && p != UnknownTitle {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// ------------------------------------------------------------------ derived values

func (a *Answerer) fullName() string {
	p := a.Cfg.Personals
	return strings.Join(nonBlank(p.FirstName, p.MiddleName, p.LastName), " ")
}

func nonBlank(items ...string) []string {
	var out []string
	for _, s := range items {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func (a *Answerer) cityOr(workLocation string) string {
	if a.Cfg.Personals.City != "" {
		return a.Cfg.Personals.City
	}
	return workLocation
}

// salaryAnswer picks the figure a salary question asks for: a named period wins,
// else the market's own period ("Pretensão salarial" in Brazil is monthly).
func (a *Answerer) salaryAnswer(label string) string {
	m := a.Market
	current := textmatch.Has(label, "current", "present", "atual")
	annual, monthly := m.DesiredAnnual, m.DesiredMonthly
	if current {
		annual, monthly = m.CurrentAnnual, m.CurrentMonthly
	}
	switch {
	case textmatch.Has(label, monthTerms...):
		return markets.FormatMoney(monthly)
	case textmatch.Has(label, "lakh", "lakhs"):
		return markets.FormatMoney(roundTo(annual/100000, 2))
	case textmatch.Has(label, annualTerms...):
		return markets.FormatMoney(annual)
	case m.SalaryPeriod == markets.Monthly:
		return markets.FormatMoney(monthly)
	default:
		return markets.FormatMoney(annual)
	}
}

func roundTo(v float64, decimals int) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', decimals, 64), 64)
	return f
}

func (a *Answerer) noticeAnswer(label string) string {
	days := a.Cfg.Questions.NoticePeriod
	switch {
	case textmatch.Has(label, monthTerms...):
		return strconv.Itoa(days / 30)
	case textmatch.Has(label, weekTerms...):
		return strconv.Itoa(days / 7)
	}
	return strconv.Itoa(days)
}

// ------------------------------------------------------------------ the fallbacks

// lookupUnlessSensitive is a memory lookup guarded by the sensitive rule.
func (a *Answerer) lookupUnlessSensitive(label, kind string, approximate bool) (*memory.Remembered, bool) {
	if a.Memory == nil || IsSensitive(label) {
		return nil, false
	}
	found, approx, err := a.Memory.Lookup(label, kind, approximate)
	if err != nil {
		a.Logger.Warn("answer memory lookup failed", "question", label, "error", err)
		return nil, false
	}
	return found, approx
}

func (a *Answerer) recordUse(entry *memory.Remembered, jobLink string) {
	if a.Memory == nil {
		return
	}
	if err := a.Memory.RecordUse(entry.ID, jobLink); err != nil {
		a.Logger.Warn("answer memory could not count a use", "id", entry.ID, "error", err)
	}
}

func (a *Answerer) remember(label, kind, answer, source, jobLink string) *memory.Remembered {
	if a.Memory == nil {
		return nil
	}
	entry, err := a.Memory.Remember(label, kind, answer, source, jobLink)
	if err != nil {
		a.Logger.Warn("answer memory could not remember", "question", label, "error", err)
		return nil
	}
	return entry
}

func (a *Answerer) askAI(label string, kind Kind, options []string, description string) string {
	if a.AI == nil {
		return ""
	}
	answer, err := a.AI.Answer(label, kind, options, description)
	if err != nil {
		a.Logger.Warn("AI answer failed", "question", label, "error", err)
		return ""
	}
	return strings.TrimSpace(answer)
}

// answerFromMemoryOrAI is the fallback for an unrecognised text or textarea
// question: the memory first, then the AI (remembered as pending). "" when
// neither has anything or the question is sensitive.
func (a *Answerer) answerFromMemoryOrAI(label string, kind Kind, job Job) (string, string) {
	if IsSensitive(label) {
		return "", ""
	}
	if found, approx := a.lookupUnlessSensitive(label, string(kind), true); found != nil {
		a.recordUse(found, job.Link)
		a.Logger.Info("memory answered", "question", label, "answer", found.Answer, "state", found.State, "approximate", approx)
		return found.Answer, "memory"
	}
	answer := a.askAI(label, kind, nil, job.Description)
	if answer == "" {
		return "", ""
	}
	a.Logger.Info("AI answered", "question", label, "answer", answer)
	if entry := a.remember(label, string(kind), answer, "ai", job.Link); entry != nil {
		a.recordUse(entry, job.Link)
	}
	return answer, "ai"
}

// optionFromMemoryOrAI picks an option for a select or radio with no configured
// answer: the memory's text snapped to THIS form's options, then the AI shown the
// non-placeholder options and accepted only verbatim. -1 when nothing fits.
func (a *Answerer) optionFromMemoryOrAI(label string, kind Kind, options []string, job Job) (int, string) {
	if IsSensitive(label) {
		return -1, ""
	}
	if found, approx := a.lookupUnlessSensitive(label, string(kind), true); found != nil {
		matched := indexOf(options, found.Answer)
		if matched < 0 {
			matched = MatchAnswerToOption(found.Answer, options)
		}
		if matched >= 0 {
			a.recordUse(found, job.Link)
			a.Logger.Info("memory answered", "question", label, "answer", options[matched], "state", found.State, "approximate", approx)
			return matched, "memory"
		}
		a.Logger.Info("memory has an answer but no option here matches it", "question", label, "answer", found.Answer)
	}
	var choices []string
	for _, o := range options {
		if !IsPlaceholder(o) {
			choices = append(choices, o)
		}
	}
	if len(choices) == 0 || a.AI == nil {
		return -1, ""
	}
	answer := a.askAI(label, kind, choices, job.Description)
	if indexOf(choices, answer) < 0 {
		a.Logger.Info("AI did not name one of the options", "question", label)
		return -1, ""
	}
	a.Logger.Info("AI answered", "question", label, "answer", answer)
	if entry := a.remember(label, string(kind), answer, "ai", job.Link); entry != nil {
		a.recordUse(entry, job.Link)
	}
	return indexOf(options, answer), "ai"
}

// ------------------------------------------------------------------ Answer

// Answer decides what to do with one question.
func (a *Answerer) Answer(q Question, job Job) Decision {
	switch q.Kind {
	case Select:
		return a.answerSelect(q, job)
	case Radio:
		return a.answerRadio(q, job)
	case Text:
		return a.answerText(q, job)
	case Textarea:
		return a.answerTextarea(q, job)
	case Checkbox:
		return a.answerCheckbox(q, job)
	}
	return Decision{Action: Blocked, Reason: fmt.Sprintf("unknown control kind %q", q.Kind)}
}

func blocked(reason string) Decision { return Decision{Action: Blocked, Option: -1, Reason: reason} }

func (a *Answerer) answerSelect(q Question, job Job) Decision {
	label := strings.ToLower(q.Label)
	if !a.Cfg.Questions.OverwritePrevious && !IsPlaceholder(q.Current) {
		return Decision{Action: Leave, Value: q.Current, Option: indexOf(q.Options, q.Current), Source: "prefilled"}
	}
	answer := ""
	switch {
	case a.WorkAuthorizationAnswer(label) != "":
		answer = a.WorkAuthorizationAnswer(label)
	case textmatch.Has(label, "email", "phone"):
		return Decision{Action: Leave, Value: q.Current, Option: indexOf(q.Options, q.Current), Source: "prefilled"}
	case textmatch.Has(label, "gender", "sex", "sexual orientation", "gênero", "genero", "sexo"):
		answer = a.Cfg.Personals.Gender
	case textmatch.Has(label, disabilityTerms...):
		answer = a.Cfg.Personals.Disability
	case textmatch.Has(label, "proficiency", "proficiência", "proficiencia", "fluência", "fluencia"):
		answer = "Professional"
	case textmatch.Has(label, concat([]string{"location", "localização", "localizacao"}, cityTerms, stateTerms, countryTerms)...) && !textmatch.Has(label, "civil"):
		switch {
		case textmatch.Has(label, countryTerms...):
			answer = a.Cfg.Personals.Country
		case textmatch.Has(label, stateTerms...):
			answer = a.Cfg.Personals.State
		case textmatch.Has(label, cityTerms...):
			answer = a.cityOr(job.WorkLocation)
		default:
			answer = job.WorkLocation
		}
	}
	return a.chooseOption(q, label, answer, job)
}

func (a *Answerer) answerRadio(q Question, job Job) Decision {
	label := strings.ToLower(q.Label)
	if !a.Cfg.Questions.OverwritePrevious && q.Current != "" {
		return Decision{Action: Leave, Value: q.Current, Option: indexOf(q.Options, q.Current), Source: "prefilled"}
	}
	answer := ""
	switch {
	case a.WorkAuthorizationAnswer(label) != "":
		answer = a.WorkAuthorizationAnswer(label)
	case textmatch.Has(label, "veteran", "protected", "veterano"):
		answer = a.Cfg.Personals.Veteran
	case textmatch.Has(label, disabilityTerms...):
		answer = a.Cfg.Personals.Disability
	}
	return a.chooseOption(q, label, answer, job)
}

// chooseOption turns a configured answer (or none) into a Choose or Blocked for a
// select/radio: exact option, else the honest mapper, else (only when nothing is
// configured) the memory and the AI. Never the first option.
func (a *Answerer) chooseOption(q Question, label, answer string, job Job) Decision {
	if answer != "" {
		if i := indexOf(q.Options, answer); i >= 0 {
			return Decision{Action: Choose, Option: i, Value: q.Options[i], Source: "config"}
		}
	}
	matched, source := MatchAnswerToOption(answer, q.Options), "config"
	if matched < 0 && answer == "" {
		matched, source = a.optionFromMemoryOrAI(q.Label, q.Kind, q.Options, job)
	}
	if matched < 0 {
		what := answer
		if what == "" {
			what = "a configured answer"
		}
		a.Logger.Info("no option matched; leaving it unanswered instead of guessing", "question", q.Label, "answer", what)
		return blocked(fmt.Sprintf("no option matched %q", what))
	}
	return Decision{Action: Choose, Option: matched, Value: q.Options[matched], Source: source}
}

func (a *Answerer) answerText(q Question, job Job) Decision {
	label := strings.ToLower(q.Label)
	if q.Current != "" && !a.Cfg.Questions.OverwritePrevious {
		return Decision{Action: Leave, Value: q.Current, Source: "prefilled"}
	}
	p, qs := a.Cfg.Personals, a.Cfg.Questions
	answer, fallbackAllowed, typeahead := "", true, false
	switch {
	case a.WorkAuthorizationAnswer(label) != "":
		answer = a.WorkAuthorizationAnswer(label)
	case textmatch.Has(label, "experience", "years", "experiência", "experiencia", "anos"):
		// Only the total. "years of Kubernetes experience" and "experience with Python" ask about ONE skill.
		withoutUnit := label
		for _, phrase := range periodUnitPhrases {
			withoutUnit = strings.ReplaceAll(withoutUnit, phrase, " ")
		}
		if textmatch.Has(label, totalExperienceTerms...) && !textmatch.Has(withoutUnit, skillQualifierTerms...) {
			answer = qs.YearsOfExperience
		}
	case textmatch.Has(label, "phone", "mobile", "telefone", "celular", "whatsapp"):
		answer = p.Phone
	case textmatch.Has(label, "street", "endereço", "endereco", "rua", "logradouro"):
		answer = p.Street
	case textmatch.Has(label, "email", "e-mail"):
		// No email value in the config, and the AI must never invent one: a made-up
		// email would be remembered and replayed on every later application.
		fallbackAllowed = false
		a.Logger.Info("no configured answer for the email question; leaving LinkedIn's own value", "question", q.Label)
	case textmatch.Has(label, "city", "location", "address", "cidade", "localização", "localizacao"):
		answer, typeahead = a.cityOr(job.WorkLocation), true
	case textmatch.Has(label, "signature", "assinatura"):
		answer = a.fullName()
	case textmatch.Has(label, "name", "surname", "nome", "sobrenome"):
		switch {
		case textmatch.Has(label, "full", "completo"):
			answer = a.fullName()
		case textmatch.Has(label, "first", "primeiro") && !textmatch.Has(label, "last", "sobrenome"):
			answer = p.FirstName
		case textmatch.Has(label, "middle", "meio") && !textmatch.Has(label, "last", "sobrenome"):
			answer = p.MiddleName
		case textmatch.Has(label, "last", "surname", "sobrenome") && !textmatch.Has(label, "first", "primeiro"):
			answer = p.LastName
		case textmatch.Has(label, "employer", "empregador", "empresa"):
			answer = qs.RecentEmployer
		default:
			answer = a.fullName()
		}
	case textmatch.Has(label, "notice", "aviso prévio", "aviso previo"):
		answer = a.noticeAnswer(label)
	case textmatch.Has(label, append([]string{"pay"}, salaryTerms...)...): // bare "pay" answers here but is not a sensitivity trigger
		answer = a.salaryAnswer(label)
	case textmatch.Has(label, "linkedin"):
		answer = qs.LinkedIn
	case textmatch.Has(label, "website", "blog", "portfolio", "link", "links", "portfólio"):
		answer = qs.Website
	case textmatch.Has(label, "scale of 1-10"):
		answer = qs.ConfidenceLevel
	case textmatch.Has(label, "headline"):
		answer = qs.Headline
	case textmatch.Has(label, "hear", "heard", "come across") && textmatch.Has(label, "this") && textmatch.Has(label, "job", "position"):
		answer = "LinkedIn"
	case textmatch.Has(label, stateTerms...) && !textmatch.Has(label, "civil"): // "Estado civil" is marital status
		answer = p.State
	case textmatch.Has(label, "zip", "zipcode", "postal", "postcode", "code", "cep"):
		answer = p.Zip
	case textmatch.Has(label, countryTerms...):
		answer = p.Country
	}
	source := "config"
	if answer == "" && fallbackAllowed {
		answer, source = a.answerFromMemoryOrAI(q.Label, Text, job)
	}
	if answer == "" {
		a.Logger.Info("no answer for the text question; leaving it empty", "question", q.Label)
		return blocked("no answer")
	}
	return Decision{Action: Fill, Value: answer, Typeahead: typeahead, Source: source}
}

func (a *Answerer) answerTextarea(q Question, job Job) Decision {
	label := strings.ToLower(q.Label)
	if q.Current != "" && !a.Cfg.Questions.OverwritePrevious {
		return Decision{Action: Leave, Value: q.Current, Source: "prefilled"}
	}
	answer, source := "", "config"
	switch {
	case textmatch.Has(label, "summary", "resumo", "sobre você", "sobre voce"):
		answer = a.Cfg.Questions.Summary
	case textmatch.Has(label, "cover", "carta", "apresentação", "apresentacao"):
		answer = a.Cfg.Questions.CoverLetter
	}
	if answer == "" {
		answer, source = a.answerFromMemoryOrAI(q.Label, Textarea, job)
	}
	if answer == "" {
		return blocked("no answer")
	}
	return Decision{Action: Fill, Value: answer, Source: source}
}

// answerCheckbox ticks only from an approved remembered answer. Every other
// unticked box is left alone and reported; a non-sensitive one is offered for
// review as a pending "checked" entry so approving it ticks it next time.
func (a *Answerer) answerCheckbox(q Question, job Job) Decision {
	if q.Current == "checked" {
		return Decision{Action: Leave, Value: "checked", Source: "prefilled"}
	}
	key := CheckboxKey(q.GroupTitle, q.Label)
	var remembered *memory.Remembered
	if key != "" {
		// Exact only: a near-identical attestation at another employer is a different legal text.
		remembered, _ = a.lookupUnlessSensitive(key, string(Checkbox), false)
	}
	if remembered != nil && remembered.State == "approved" && remembered.Answer == "checked" {
		a.recordUse(remembered, job.Link)
		a.Logger.Info("memory ticked a box approved by you", "question", key)
		return Decision{Action: Tick, Value: "checked", Source: "memory"}
	}
	reason := "it cannot be classified"
	if term := AttestationTerm(strings.TrimSpace(q.GroupTitle + " " + q.Label)); term != "" {
		reason = fmt.Sprintf("it is an attestation or consent (%q)", term)
	}
	a.Logger.Info("not ticking a box; it is not something to guess", "question", key, "why", reason)
	if remembered == nil && key != "" && !IsSensitive(key) {
		a.remember(key, string(Checkbox), "checked", "form", job.Link)
	}
	return blocked(reason)
}
