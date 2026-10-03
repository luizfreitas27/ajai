// Package ai answers unrecognised form questions with a language model through
// one OpenAI-compatible client (the official openai-go SDK) for every provider:
// OpenAI, DeepSeek, Gemini's OpenAI-compatible endpoint, Ollama, LM Studio, vLLM
// or any compatible server. Ported from the Python modules/ai.
package ai

import (
	"strconv"
	"strings"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/markets"
)

// CandidateProfile is the description of the applicant the model is given:
// one "Label: value" line per configured fact, then the summary and the free
// text from questions.about. Deliberately absent: phone, email, street, zip,
// citizenship status, current salary, links, protected characteristics and the
// cover letter. The prompt tells the model to use these facts "whenever
// relevant", so anything in here can surface in a free-text answer.
func CandidateProfile(c config.Config, m markets.Resolved) string {
	p, q := c.Personals, c.Questions
	salary := ""
	if v := m.DesiredAsEntered(); v > 0 {
		salary = strings.TrimSpace(markets.FormatMoney(v) + " " + m.Currency)
	}
	notice := ""
	if q.NoticePeriod > 0 {
		notice = strconv.Itoa(q.NoticePeriod) + " days"
	}
	rows := [][2]string{
		{"Name", join(" ", p.FirstName, p.MiddleName, p.LastName)},
		{"Location", join(", ", p.City, p.State, p.Country)},
		{"Years of professional experience", q.YearsOfExperience},
		{"Most recent employer", q.RecentEmployer},
		{"Headline", q.Headline},
		{"Desired " + string(m.SalaryPeriod) + " salary", salary},
		{"Notice period", notice},
		{"Legally authorized to work in the country of the job", q.LegallyAuthorized},
		{"Will require visa sponsorship", q.RequireVisa},
	}
	var lines []string
	for _, r := range rows {
		if v := strings.TrimSpace(r[1]); v != "" {
			lines = append(lines, r[0]+": "+v)
		}
	}
	if s := strings.TrimSpace(q.Summary); s != "" {
		lines = append(lines, "Summary:\n"+s)
	}
	if extra := strings.TrimSpace(q.About); extra != "" {
		lines = append(lines, "Additional information:\n"+extra)
	}
	return strings.Join(lines, "\n")
}

func join(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
