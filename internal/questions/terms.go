// Package questions decides what to put in each Easy Apply form control from a
// question's label, its kind and its options: the configured answers first,
// then the answer memory, then the AI, never a guess. Every label test is
// whole-word (textmatch). Ported from the Python answer_questions and its
// helpers; the term lists below are copied verbatim, English and Portuguese.
package questions

import "strings"

// Work-authorization wording overlaps the location questions ("United States"
// contains "state") and the two families collide in every branch, so it is
// classified first.
var (
	visaTerms = []string{"sponsor", "sponsors", "sponsorship", "visa", "visas", "work permit", "h-1b", "h1b",
		"visto", "vistos", "patrocínio", "patrocinio", "patrocínio de visto"}
	authorizationTerms = []string{"employment eligibility", "legally authorized", "legally authorised",
		"authorized to work", "authorised to work", "work authorization", "work authorisation", "right to work",
		"autorizado a trabalhar", "autorizada a trabalhar", "autorização de trabalho", "autorizacao de trabalho",
		"permissão de trabalho", "permissao de trabalho", "direito de trabalhar", "legalmente autorizado", "legalmente autorizada"}
	citizenshipTerms = []string{"citizen", "citizens", "citizenship", "cidadania", "cidadão", "cidadao", "cidadã"}

	// Answer -> option mapping. Whole words: "no" lives inside "Non-citizen", "not", "none" and "know".
	declineTerms = []string{"decline", "prefer not", "not wish", "don't wish", "not want", "rather not",
		"prefiro não", "prefiro nao", "não desejo", "nao desejo", "não informar", "nao informar"}
	negationTerms = []string{"no", "not", "never", "don't", "doesn't", "won't", "can't", "cannot",
		"disagree", "decline", "não", "nao", "nunca", "discordo", "recuso"}

	clearanceTerms = []string{"polygraph", "clearance", "secret", "top secret", "ts/sci", "sci"}
	// Checkbox labels that carry legal weight. None is ever auto-ticked; the list names WHY a box was left alone.
	attestationTerms = []string{"certify", "certifies", "certification", "attest", "attestation",
		"consent", "consents", "agree", "agreement", "terms", "conditions", "policy", "acknowledge",
		"acknowledgement", "acknowledgment", "authorize", "authorise", "authorization", "authorisation",
		"background check", "drug test", "drug screen", "drug screening", "accurate", "accuracy",
		"true and complete", "eligible", "eligibility", "citizen", "citizenship", "clearance", "i understand",
		"i confirm", "declaro", "concordo", "autorizo", "aceito", "ciente", "termos", "política", "politica",
		"verdadeiras", "lgpd", "consentimento", "cidadania"}

	// Sensitive questions: configured answers only, never the AI, never the memory.
	// No bare "pay": "Do you pay attention to detail?" is not a salary question.
	salaryTerms = []string{"salary", "salaries", "compensation", "ctc", "remuneration", "wage", "wages", "pay rate", "pay range",
		"salário", "salario", "salarial", "pretensão", "pretensao", "remuneração", "remuneracao"}
	disabilityTerms = []string{"disability", "disabilities", "disabled", "handicap", "handicapped", "deficiência", "deficiencia", "pcd"}
	veteranTerms    = []string{"veteran", "veterans", "military"}
	sensitiveTerms  = concat(visaTerms, authorizationTerms, citizenshipTerms, clearanceTerms, salaryTerms, disabilityTerms, veteranTerms)

	// Period words a salary or notice-period question may carry.
	monthTerms  = []string{"month", "months", "monthly", "mês", "mes", "meses", "mensal", "mensais"}
	weekTerms   = []string{"week", "weeks", "weekly", "semana", "semanas", "semanal"}
	annualTerms = []string{"year", "yearly", "annual", "annually", "per annum", "ano", "anual", "anuais", "por ano"}

	// Gender and race/ethnicity questions are answered from the configured
	// personals.gender / personals.ethnicity ("Decline" picks the decline option).
	genderTerms = []string{"gender", "sex", "sexual orientation", "gênero", "genero", "sexo"}
	// A bare "race" is only a race question when it is the whole label ("Race *");
	// "race conditions" or "a race against deadlines" are not (isEthnicity).
	ethnicityTerms = []string{"ethnicity", "ethnic", "racial", "your race", "race/ethnicity", "race or ethnicity", "raça", "raca", "etnia", "cor ou raça", "cor/raça"}

	countryTerms = []string{"country", "país", "pais"}
	stateTerms   = []string{"state", "province", "estado", "uf"}
	cityTerms    = []string{"city", "cidade"}

	// years_of_experience is a TOTAL, so it only answers a question that asks for the total...
	totalExperienceTerms = []string{"anos de experiência", "anos de experiencia", "tempo de experiência", "tempo de experiencia",
		"years of experience", "years experience", "work experience", "working experience", "professional experience",
		"total experience", "overall experience", "industry experience", "years of work", "technical experience",
		"years of technical", "engineering experience"}
	// ...and not when narrowed to one skill: "years of experience IN Kubernetes", "experience WITH Python".
	skillQualifierTerms = []string{"in", "with", "using", "on", "em", "com"}
	// "...experience in years" names a unit, not a skill: stripped before looking for a qualifier.
	periodUnitPhrases = []string{"in years", "in months", "em anos", "em meses"}
)

// OptionPlaceholders are dropdown texts that mean "nothing chosen yet": never
// offered to the AI, and a dropdown sitting on one counts as unanswered. An
// employer's own questions come in the language of the job, so LinkedIn's
// Portuguese placeholders are here too.
var OptionPlaceholders = []string{"Select an option", "Select", "Please select", "Choose an option",
	"Selecionar opção", "Selecione uma opção", "Selecione", "Escolha uma opção", ""}

// IsPlaceholder reports whether an option text is one of OptionPlaceholders,
// ignoring case and the surrounding whitespace.
func IsPlaceholder(text string) bool {
	text = strings.TrimSpace(text)
	for _, p := range OptionPlaceholders {
		if strings.EqualFold(text, p) {
			return true
		}
	}
	return false
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
