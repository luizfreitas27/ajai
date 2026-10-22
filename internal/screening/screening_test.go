package screening

import (
	"strings"
	"testing"

	"github.com/luizfreitas27/ajai/internal/config"
)

var offered = []string{"visa sponsorship available", "sponsorship available", "sponsorship is available",
	"we sponsor", "we do sponsor", "will sponsor", "open to sponsorship", "h-1b transfer", "h1b transfer", "cap-exempt", "cap exempt"}
var unavailable = []string{"will not sponsor", "do not sponsor", "does not sponsor", "cannot sponsor", "unable to sponsor",
	"not able to sponsor", "not offer sponsorship", "not provide sponsorship", "does not provide immigration", "no visa sponsorship",
	"without sponsorship", "without the need for sponsorship", "sponsorship not available", "sponsorship is not available",
	"not eligible for visa sponsorship", "must be a us citizen", "must be a u.s. citizen"}

// pinned mirrors the Python test fixture: sponsorship filter on, strict off, other filters out of the way.
func pinned() Rules {
	s := config.Defaults().Search
	s.SkipNonSponsoringJobs, s.SkipJobsWithoutSponsorship = true, false
	s.SponsorshipOfferedPhrases, s.SponsorshipUnavailablePhrases = offered, unavailable
	s.BadWords, s.SecurityClearance, s.CurrentExperience, s.DidMasters = nil, true, -1, false
	return Rules{Search: s, RequireVisa: "Yes"}
}

func desc(text string) Job { return Job{Description: text, DescriptionOK: true} }

func TestAPostingThatRefusesSponsorshipIsSkippedAndTheReasonNamesThePhrase(t *testing.T) {
	cases := map[string]string{
		"This role is not eligible for visa sponsorship.":                                                      "not eligible for visa sponsorship",
		"Applicants must be authorized to work in the United States without sponsorship now or in the future.": "without sponsorship",
		"We are unable to sponsor or take over sponsorship of an employment visa at this time.":                "unable to sponsor",
		"Sponsorship is not available for this position.":                                                      "sponsorship is not available",
		"GM DOES NOT PROVIDE IMMIGRATION-RELATED SPONSORSHIP FOR THIS ROLE.":                                   "does not provide immigration",
	}
	r := pinned()
	for text, phrase := range cases {
		v := r.Description(desc(text))
		if !v.Skip || !strings.Contains(v.Reason, phrase) {
			t.Errorf("%q: %+v", text, v)
		}
	}
}

func TestAnOfferOfSponsorshipWinsOverARefusalPhraseAndSilenceApplies(t *testing.T) {
	r := pinned()
	for _, text := range []string{
		"We do not require you to have sponsorship. We will sponsor H-1B transfers.",
		"We are unable to sponsor new cap-subject H-1B petitions at this time, however we will sponsor H-1B transfers for candidates already holding status.",
		"Visa sponsorship available for exceptional candidates.",
		"This role is cap-exempt and we sponsor H-1B transfers.",
		"We are open to sponsorship for the right person.",
		"Senior Python engineer. You will own our data pipeline and mentor two juniors.", // silence
		"We are proud to fund the sponsorship of our annual conference for the community.",
		"The marketing team produces sponsored content and partner newsletters.",
		"You will manage sponsorships across our developer events.",
	} {
		if v := r.Description(desc(text)); v.Skip {
			t.Errorf("%q should be applied to: %+v", text, v)
		}
	}
}

func TestTheSponsorshipFilterIsInertUnlessSwitchedOn(t *testing.T) {
	refusal := desc("This role is not eligible for visa sponsorship.")
	r := pinned()
	r.RequireVisa = "No"
	if v := r.Description(refusal); v.Skip {
		t.Errorf("%+v", v)
	}
	r = pinned()
	r.Search.SkipNonSponsoringJobs = false
	if v := r.Description(refusal); v.Skip {
		t.Errorf("%+v", v)
	}
}

func TestStrictModeSkipsSilenceButNotOffersAndReportsItAsSilence(t *testing.T) {
	r := pinned()
	r.Search.SkipJobsWithoutSponsorship = true
	silent := desc("Senior Python engineer. You will own our data pipeline and mentor two juniors.")
	v := r.Description(silent)
	if !v.Skip || !strings.Contains(strings.ToLower(v.Reason), "silent") || !strings.Contains(strings.ToLower(v.Reason), "not a refusal") || strings.Contains(v.Reason, "excludes visa sponsorship") {
		t.Errorf("%+v", v)
	}
	if v := r.Description(desc("This role is cap-exempt and we sponsor H-1B transfers.")); v.Skip {
		t.Errorf("%+v", v)
	}
	v = r.Description(desc("This role is not eligible for visa sponsorship."))
	if !v.Skip || !strings.Contains(v.Reason, "not eligible for visa sponsorship") || strings.Contains(strings.ToLower(v.Reason), "silent") {
		t.Errorf("a refusal is reported as a refusal: %+v", v)
	}
	r.RequireVisa = "No"
	if v := r.Description(silent); v.Skip {
		t.Error("strict mode is inert without require_visa")
	}
	r = pinned()
	r.Search.SkipJobsWithoutSponsorship, r.Search.SkipNonSponsoringJobs = true, false
	if r.Description(silent).Skip || r.Description(desc("This role is not eligible for visa sponsorship.")).Skip {
		t.Error("strict mode cannot bypass the master switch")
	}
}

func TestBadWordsAreWholeWordsAndClearanceAndExperienceRules(t *testing.T) {
	r := pinned()
	r.Search.BadWords = []string{"java"}
	if v := r.Description(desc("Senior JavaScript engineer, React and Node.")); v.Skip {
		t.Errorf("javascript is not java: %+v", v)
	}
	if v := r.Description(desc("Strong Java and Spring experience")); !v.Skip || v.Detail != "java" {
		t.Errorf("%+v", v)
	}
	r = pinned()
	r.Search.SecurityClearance = false
	if v := r.Description(desc("Active TS/SCI clearance required")); !v.Skip || !strings.Contains(v.Reason, "clearance") {
		t.Errorf("%+v", v)
	}
	if v := r.Description(desc("Experience as an executive secretary; top-secretarial duties")); v.Skip {
		t.Errorf("whole words: %+v", v)
	}
	r = pinned()
	r.Search.CurrentExperience = 5
	v := r.Description(desc("We need 8+ years of Go and 3 years of SQL."))
	if !v.Skip || v.ExperienceRequired != 8 || v.Detail != "8 > 5" {
		t.Errorf("%+v", v)
	}
	r.Search.DidMasters = true
	if v := r.Description(desc("We need 7 years of Go. A master's degree is a plus.")); v.Skip || v.ExperienceRequired != 7 {
		t.Errorf("a master's adds two years: %+v", v)
	}
	if v := r.Description(desc("Join us for 100 years of history; 4 years of experience.")); v.ExperienceRequired != 4 {
		t.Errorf("figures above 12 are not experience: %+v", v)
	}
	if v := r.Description(Job{DescriptionOK: false}); !v.Skip || !strings.Contains(v.Reason, "unable to read") {
		t.Errorf("%+v", v)
	}
}

func TestCardAndCompanyRules(t *testing.T) {
	r := pinned()
	r.Search.AboutCompanyBadWords = []string{"Crossover"}
	r.Search.AboutCompanyGoodWords = []string{"remote-first"}
	if v := r.Card(Job{ID: "1", Company: "ACME"}, map[string]bool{"ACME": true}, nil, nil); !v.Skip || v.Reason != "blacklisted company" {
		t.Errorf("%+v", v)
	}
	if v := r.Card(Job{ID: "2"}, nil, map[string]bool{"2": true}, nil); !v.Skip || v.Reason != "previously rejected" {
		t.Errorf("%+v", v)
	}
	if v := r.Card(Job{ID: "3", AlreadyApplied: true}, nil, nil, nil); !v.Skip || v.Reason != "already applied" {
		t.Errorf("%+v", v)
	}
	if v := r.Card(Job{ID: "4"}, nil, nil, map[string]bool{"4": true}); !v.Skip {
		t.Errorf("%+v", v)
	}
	if v := r.Card(Job{ID: "5"}, nil, nil, nil); v.Skip {
		t.Errorf("%+v", v)
	}
	if v := r.Company(Job{AboutCompany: "Crossover is a remote staffing firm"}); !v.Skip || !v.BlacklistCompany {
		t.Errorf("%+v", v)
	}
	if v := r.Company(Job{AboutCompany: "Crossover is a remote-first staffing firm"}); v.Skip {
		t.Errorf("a good word waives the check: %+v", v)
	}
	if v := r.Company(Job{AboutCompany: "Crossovers of all kinds"}); v.Skip {
		t.Errorf("whole word: %+v", v)
	}
}

func TestAGoodWordInTheTitleOrDescriptionWaivesABadWord(t *testing.T) {
	cases := []struct {
		name       string
		good       []string
		bad        []string
		job        Job
		wantSkip   bool
		wantReason string
		wantDetail string
	}{
		{
			name: "good and bad both in the description: not skipped",
			good: []string{"AWS"}, bad: []string{"Azure"},
			job:      Job{Description: "We run Azure and AWS in production.", DescriptionOK: true},
			wantSkip: false,
		},
		{
			name: "bad only in the description: skipped as today",
			good: []string{"AWS"}, bad: []string{"Azure"},
			job:        Job{Description: "We run Azure in production.", DescriptionOK: true},
			wantSkip:   true,
			wantReason: "bad word in the job description",
			wantDetail: "Azure",
		},
		{
			name: "good words set, no bad word present: not skipped",
			good: []string{"AWS"}, bad: []string{"Azure"},
			job:      Job{Description: "Python and Postgres.", DescriptionOK: true},
			wantSkip: false,
		},
		{
			name: "good word only in the title, bad word in the description: not skipped",
			good: []string{"AWS"}, bad: []string{"Azure"},
			job:      Job{Title: "Cloud Engineer (AWS)", Description: "We run Azure in production.", DescriptionOK: true},
			wantSkip: false,
		},
		{
			name: "empty good_words: bad word still skips",
			good: nil, bad: []string{"Azure"},
			job:        Job{Description: "AWS and Azure.", DescriptionOK: true},
			wantSkip:   true,
			wantReason: "bad word in the job description",
			wantDetail: "Azure",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := pinned()
			r.Search.BadWords = c.bad
			r.Search.GoodWords = c.good
			v := r.Description(c.job)
			if v.Skip != c.wantSkip {
				t.Fatalf("Skip = %v, want %v: %+v", v.Skip, c.wantSkip, v)
			}
			if c.wantSkip && (v.Reason != c.wantReason || v.Detail != c.wantDetail) {
				t.Errorf("Reason/Detail = %q/%q, want %q/%q", v.Reason, v.Detail, c.wantReason, c.wantDetail)
			}
		})
	}
}
