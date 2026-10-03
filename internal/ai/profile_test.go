package ai

import (
	"strings"
	"testing"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/markets"
)

// fullConfig mirrors the Python test fixture: every field set, including the
// private ones that must stay out of the profile.
func fullConfig() config.Config {
	c := config.Defaults()
	c.Personals = config.Personals{FirstName: "Jane", MiddleName: "Q", LastName: "Applicant", Phone: "9876543210",
		City: "Lisbon", Street: "123 Main Street", State: "Lisboa", Zip: "1000-001", Country: "Portugal",
		Ethnicity: "Decline", Gender: "Decline", Disability: "Decline", Veteran: "Decline"}
	q := &c.Questions
	q.YearsOfExperience, q.RequireVisa, q.Website, q.LinkedIn = "6", "No", "https://jane.dev", "https://linkedin.com/in/jane"
	q.LegallyAuthorized, q.Citizenship, q.DesiredSalary, q.CurrentSalary = "Yes", "Other", 90000, 70000
	q.NoticePeriod, q.Headline, q.Summary, q.CoverLetter = 30, "Backend engineer", "I build APIs.", "Dear hiring manager"
	q.RecentEmployer, q.ConfidenceLevel = "Acme", "8"
	return c
}

func profileOf(c config.Config) string { return CandidateProfile(c, markets.Resolve(c)) }

func TestTheProfileCarriesTheConfiguredFacts(t *testing.T) {
	p := profileOf(fullConfig())
	for _, want := range []string{
		"Name: Jane Q Applicant", "Location: Lisbon, Lisboa, Portugal", "Years of professional experience: 6",
		"Most recent employer: Acme", "Headline: Backend engineer", "Desired annual salary: 90000 USD",
		"Notice period: 30 days", "Legally authorized to work in the country of the job: Yes",
		"Will require visa sponsorship: No", "Summary:\nI build APIs.",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile lacks %q:\n%s", want, p)
		}
	}
}

func TestTheBrazilianMarketStatesTheMonthlySalaryInReais(t *testing.T) {
	c := fullConfig()
	c.Markets.Active = "brasil"
	c.Markets.Brasil.DesiredSalary = 8000
	p := profileOf(c)
	if !strings.Contains(p, "Desired monthly salary: 8000 BRL") || strings.Contains(strings.ToLower(p), "annual") {
		t.Errorf("%s", p)
	}
}

func TestPrivateFactsNeverReachTheProfile(t *testing.T) {
	c := fullConfig()
	c.Personals.Gender, c.Personals.Ethnicity, c.Personals.Disability, c.Personals.Veteran = "Female", "Asian", "No", "No"
	p := profileOf(c)
	for _, absent := range []string{
		"9876543210", "123 Main Street", "1000-001", // contact details
		"Other", "Citizenship", "70000", "Current", // citizenship status, current salary
		"https://jane.dev", "linkedin.com/in/jane", // links
		"Female", "Asian", "Decline", // protected characteristics
		"Dear hiring manager", // cover letter
	} {
		if strings.Contains(p, absent) {
			t.Errorf("profile leaks %q:\n%s", absent, p)
		}
	}
	if strings.Contains(strings.ToLower(p), "mail") {
		t.Errorf("profile mentions email:\n%s", p)
	}
}

func TestBlankSettingsAreSkippedAndTheFreeTextIsAppendedVerbatim(t *testing.T) {
	c := fullConfig()
	c.Questions.RecentEmployer, c.Questions.Summary = "", ""
	c.Personals.MiddleName, c.Personals.City = "", ""
	c.Questions.About = "Key skills: Python, Go, Kubernetes."
	p := profileOf(c)
	if !strings.Contains(p, "Name: Jane Applicant") || !strings.Contains(p, "Location: Lisboa, Portugal") ||
		strings.Contains(p, "Most recent employer") || strings.Contains(p, "Summary") || strings.Contains(p, ": \n") {
		t.Errorf("%s", p)
	}
	if !strings.HasSuffix(p, "Additional information:\nKey skills: Python, Go, Kubernetes.") {
		t.Errorf("%s", p)
	}
	empty := config.Defaults()
	empty.Personals, empty.Questions = config.Personals{FirstName: "Jane"}, config.Questions{}
	if got := profileOf(empty); got != "Name: Jane" {
		t.Errorf("%q", got)
	}
}
