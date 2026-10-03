// Package screening holds the skip rules: whether a job is worth applying to,
// decided from its card and its description text before any form is touched.
// Ported from the Python get_job_main_details, check_blacklist and
// get_job_description. Every text test is whole-word (textmatch).
package screening

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/textmatch"
)

// Job is what the screener reads about one posting.
type Job struct {
	ID             string
	Title          string
	Company        string
	Description    string // "" when it could not be read
	DescriptionOK  bool   // false when the description element was missing or unreadable
	AboutCompany   string
	AlreadyApplied bool // LinkedIn's own "Applied" badge on the card
}

// Verdict is the screener's decision.
type Verdict struct {
	Skip               bool
	Reason             string // short, for the jobs table
	Detail             string // the matched phrase or figure, for the log
	ExperienceRequired int    // years found in the description, 0 when none
	BlacklistCompany   bool   // the company itself should be remembered as blacklisted
}

// Rules are the settings the screener needs; built from the config once per run.
type Rules struct {
	Search      config.Search
	RequireVisa string // questions.require_visa: the sponsorship filter is inert unless "Yes"
}

// FromConfig extracts the rules from a config.
func FromConfig(c config.Config) Rules {
	return Rules{Search: c.Search, RequireVisa: c.Questions.RequireVisa}
}

var (
	// Patterns like "10+ years", "5 years", "3-5 years", "(2) years".
	experienceRe   = regexp.MustCompile(`(?i)[(]?\s*(\d+)\s*[)]?\s*[-to]*\s*\d*[+]*\s*years?`)
	mastersTerms   = []string{"master", "masters", "master's", "ms", "m.s.", "msc", "m.sc.", "graduate degree"}
	clearanceTerms = []string{"polygraph", "clearance", "secret", "top secret", "ts/sci", "sci"}
)

// ExtractYearsOfExperience returns the largest plausible (<= 12) years figure in text, or 0.
func ExtractYearsOfExperience(text string) int {
	best := 0
	for _, m := range experienceRe.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[1])
		if err == nil && n <= 12 && n > best {
			best = n
		}
	}
	return best
}

// Card screens what the job card alone tells: blacklisted company, previously
// rejected id, LinkedIn's "Applied" badge, and our own applied history.
func (r Rules) Card(job Job, blacklisted, rejected, applied map[string]bool) Verdict {
	switch {
	case blacklisted[job.Company]:
		return Verdict{Skip: true, Reason: "blacklisted company", Detail: job.Company}
	case rejected[job.ID]:
		return Verdict{Skip: true, Reason: "previously rejected"}
	case job.AlreadyApplied || applied[job.ID]:
		return Verdict{Skip: true, Reason: "already applied"}
	}
	return Verdict{}
}

// Company screens the "About the company" text: a good word waives the check,
// a bad word skips the job and blacklists the company for the rest of the run.
func (r Rules) Company(job Job) Verdict {
	about := strings.ToLower(job.AboutCompany)
	for _, good := range r.Search.AboutCompanyGoodWords {
		if good != "" && strings.Contains(about, strings.ToLower(good)) {
			return Verdict{}
		}
	}
	if bad := textmatch.FindWord(job.AboutCompany, r.Search.AboutCompanyBadWords); bad != "" {
		return Verdict{Skip: true, Reason: "blacklisted word in About the company", Detail: bad, BlacklistCompany: true}
	}
	return Verdict{}
}

// Description screens the job description: unreadable, bad words, sponsorship
// (offer wins, silence applies unless strict), security clearance, experience.
func (r Rules) Description(job Job) Verdict {
	if !job.DescriptionOK {
		// None of the filters could run: never apply to a job we did not read.
		return Verdict{Skip: true, Reason: "unable to read the job description"}
	}
	s := r.Search
	text := job.Description
	if bad := textmatch.FindWord(text, s.BadWords); bad != "" {
		return Verdict{Skip: true, Reason: "bad word in the job description", Detail: bad}
	}
	// Sponsorship, tier 1: only what THIS posting says. Offers first; an offer always wins.
	if r.RequireVisa == "Yes" && s.SkipNonSponsoringJobs && textmatch.FindWord(text, s.SponsorshipOfferedPhrases) == "" {
		if refusal := textmatch.FindWord(text, s.SponsorshipUnavailablePhrases); refusal != "" {
			return Verdict{Skip: true, Reason: fmt.Sprintf("job description excludes visa sponsorship (%q)", refusal), Detail: refusal}
		}
		if s.SkipJobsWithoutSponsorship {
			return Verdict{Skip: true, Reason: "job description is silent on visa sponsorship (not a refusal)"}
		}
	}
	if !s.SecurityClearance {
		if term := textmatch.FindWord(text, clearanceTerms); term != "" {
			return Verdict{Skip: true, Reason: "asks for a security clearance", Detail: term}
		}
	}
	v := Verdict{ExperienceRequired: ExtractYearsOfExperience(text)}
	if s.CurrentExperience > -1 {
		allowance := s.CurrentExperience
		if s.DidMasters && textmatch.FindWord(text, mastersTerms) != "" {
			allowance += 2
		}
		if v.ExperienceRequired > allowance {
			v.Skip, v.Reason = true, "required experience is high"
			v.Detail = fmt.Sprintf("%d > %d", v.ExperienceRequired, allowance)
		}
	}
	return v
}
