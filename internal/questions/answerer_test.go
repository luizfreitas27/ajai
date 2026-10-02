package questions

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/store"
)

const (
	state       = "Teststate"
	citizenship = "Non-citizen allowed to work for any employer"
)

// pinned is the config every assertion depends on, so the tests hold whatever
// the user configures (the Python suite pinned the same values).
func pinned() config.Config {
	c := config.Defaults()
	c.Personals = config.Personals{FirstName: "Jane", MiddleName: "Q", LastName: "Applicant", Phone: "5551234567",
		City: "", Street: "1 Main St", State: state, Zip: "12345", Country: "Testland",
		Ethnicity: "Decline", Gender: "Decline", Disability: "Decline", Veteran: "Decline"}
	c.Questions.RequireVisa, c.Questions.LegallyAuthorized, c.Questions.Citizenship = "Yes", "Yes", citizenship
	c.Questions.YearsOfExperience, c.Questions.DesiredSalary, c.Questions.CurrentSalary = "6", 250000, 200000
	c.Questions.NoticePeriod, c.Questions.LinkedIn, c.Questions.Website = 60, "https://linkedin.com/in/example", "https://example.dev"
	c.Questions.Headline, c.Questions.Summary, c.Questions.CoverLetter = "Headline", "Summary text", "Cover text"
	c.Questions.RecentEmployer, c.Questions.ConfidenceLevel = "ACME", "8"
	c.Search.Terms = []string{"x"}
	return c
}

func answerer(t *testing.T, cfg config.Config) (*Answerer, *memory.Memory) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "aja.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mem := memory.New(st, nil)
	return New(cfg, mem, nil, nil), mem
}

// Builders mirroring the Python tests' fakes: one question each.
func text(label string) Question {
	return Question{Label: label, Kind: Text}
}

func textarea(label string) Question {
	return Question{Label: label, Kind: Textarea}
}

func dropdown(label string, opts ...string) Question {
	return Question{Label: label, Kind: Select, Options: opts, Current: opts[0]}
}

func radio(label string, opts ...string) Question {
	return Question{Label: label, Kind: Radio, Options: opts}
}

func checkbox(visible, hidden string) Question {
	return Question{Label: visible, GroupTitle: hidden, Kind: Checkbox}
}

var job = Job{Link: "https://www.linkedin.com/jobs/view/111", WorkLocation: "Remote"}

// ------------------------------------------------------------ routing
func TestWorkAuthorizationBeatsLocationAndTheThreeQuestionsDiffer(t *testing.T) {
	cfg := pinned()
	cfg.Questions.RequireVisa, cfg.Questions.LegallyAuthorized = "SPONSORSHIP", "AUTHORIZED"
	a, _ := answerer(t, cfg)
	cases := map[string]string{
		"are you currently legally authorized to work in the united states?":            "AUTHORIZED",
		"what state do you live in?":                                                    "",
		"will you now or in the future require sponsorship for employment visa status?": "SPONSORSHIP",
		"what is your citizenship status?":                                              citizenship,
		"would you need your next employer to sponsor a new u.s. work visa or work authorization that you do not currently hold?": "SPONSORSHIP",
	}
	for label, want := range cases {
		if _, got := a.WorkAuthorizationAnswer(label); got != want {
			t.Errorf("%q -> %q, want %q", label, got, want)
		}
	}
	if matched, _ := a.WorkAuthorizationAnswer("what state do you live in?"); matched {
		t.Error("a location question is not a work-authorization one")
	}
}

func TestABlankWorkAuthorizationSettingNeverFallsThroughToLocationOrFallbacks(t *testing.T) {
	cfg := pinned()
	cfg.Questions.RequireVisa, cfg.Questions.LegallyAuthorized, cfg.Questions.Citizenship = "", "", ""
	a, mem := answerer(t, cfg)
	a.AI = &fakeAI{replies: map[string]string{"Are you authorized to work in this country?": "Yes"}}
	for _, q := range []Question{
		text("Are you legally authorized to work in this state?"),
		text("Will you require visa sponsorship to work in this country?"),
		dropdown("Are you authorized to work in this country?", "Select an option", "Yes", "Testland"),
		dropdown("Citizenship country", "Select an option", "Testland", "Other"),
		radio("Are you a citizen of this country?", "Yes", "No"),
	} {
		if _, err := mem.Remember(q.Label, string(q.Kind), "leaked", "user", ""); err != nil {
			t.Fatal(err)
		}
		if d := a.Answer(q, job); d.Action != Blocked || d.Value == state || d.Value == "Testland" || d.Value == "Yes" {
			t.Errorf("%q: %+v (a false legal answer on a real application)", q.Label, d)
		}
	}
	if len(a.AI.(*fakeAI).calls) != 0 {
		t.Error("the AI was asked a work-authorization question")
	}
}

func TestTheThreeVisaLabelsFromTheLiveRunAreSponsorshipNotLocation(t *testing.T) {
	a, _ := answerer(t, pinned())
	for _, label := range []string{
		"Will you now or in the future require sponsorship for employment visa status?",
		"Would you need your next employer to sponsor a new U.S. work visa or work authorization that you do not currently hold? (e.g. new H-1B, or TN work status)",
		"Would you need your next employer to transfer or continue an existing U.S. work visa or work authorization? (e.g. H-1B transfer, OPT/STEM OPT employer change)",
	} {
		if d := a.Answer(dropdown(label, "Select an option", "Yes", "No"), job); d.Action != Choose || d.Value != "Yes" {
			t.Errorf("%q: %+v", label, d)
		}
	}
}

func TestIsSensitiveMatchesWholeWords(t *testing.T) {
	cases := map[string]bool{
		"Will you require visa sponsorship?":            true,
		"Are you legally authorized to work in the US?": true,
		"Expected compensation":                         true,
		"What is your expected pay range?":              true,
		"Do you pay attention to detail?":               false,
		"What is your favourite programming language?":  false,
		"Secretary experience in years":                 false,
		"Você precisa de visto?":                        true,
		"Pretensão salarial":                            true,
		"Você é PCD?":                                   true,
	}
	for label, want := range cases {
		if IsSensitive(label) != want {
			t.Errorf("IsSensitive(%q) = %v", label, !want)
		}
	}
}

// ------------------------------------------------------------ select
func TestDropdownRouting(t *testing.T) {
	cases := []struct {
		name       string
		q          Question
		wantAction Action
		wantValue  string
	}{
		{"authorization yes/no is answered yes", dropdown("Are you currently legally authorized to work in the United States?", "Select an option", "Yes", "No"), Choose, "Yes"},
		{"authorization is not the state even when offered", dropdown("Are you currently legally authorized to work in the United States?", "Select an option", state, "Yes", "No"), Choose, "Yes"},
		{"citizenship status gets the status", dropdown("What is your citizenship status?", "Select an option", "U.S. Citizen/Permanent Resident", citizenship, "Other"), Choose, citizenship},
		{"an answer containing 'no' as a substring never selects No", dropdown("What is your citizenship status?", "Select an option", "Yes", "No"), Blocked, ""},
		{"a real state question answers with the state", dropdown("What state do you live in?", "Select an option", state), Choose, state},
		{"a worded sponsorship option", dropdown("Will you now or in the future require sponsorship for employment visa status?", "Select an option", "Yes, I will require sponsorship", "No, I will not"), Choose, "Yes, I will require sponsorship"},
		{"an unrecognised dropdown is left alone", dropdown("Do you have an active security clearance?", "Select an option", "Yes", "No"), Blocked, ""},
		{"a prefilled dropdown is kept", Question{Label: "Gender", Kind: Select, Options: []string{"Select an option", "Male", "Female"}, Current: "Female"}, Leave, "Female"},
		{"a phone code dropdown keeps LinkedIn's value", dropdown("Phone country code", "Select an option", "+1", "+55"), Leave, "Select an option"},
		{"a Portuguese visa question answers from config", dropdown("Você precisa de visto para trabalhar no país?", "Select an option", "Sim", "Não"), Choose, "Sim"},
		{"a Portuguese authorization question maps Yes to Sim", dropdown("Você está legalmente autorizado a trabalhar no Brasil?", "Select an option", "Sim", "Não"), Choose, "Sim"},
	}
	a, _ := answerer(t, pinned())
	for _, tc := range cases {
		d := a.Answer(tc.q, job)
		// A Blocked decision keeps whatever LinkedIn had (the placeholder) in Value; the option is what matters.
		if d.Action != tc.wantAction || (d.Action != Blocked && d.Value != tc.wantValue) || (d.Action == Blocked && d.Option != -1) {
			t.Errorf("%s: got %+v, want %v %q", tc.name, d, tc.wantAction, tc.wantValue)
		}
	}
}

func TestDeclineAnswersReachADeclineOption(t *testing.T) {
	for _, configured := range []string{"Decline", "I do not wish to disclose"} {
		cfg := pinned()
		cfg.Personals.Disability = configured
		a, _ := answerer(t, cfg)
		for _, q := range []Question{
			dropdown("Do you have a disability?", "Select an option", "Yes", "No", "I don't wish to answer"),
			radio("Do you have a disability?", "Yes", "No", "I don't wish to answer"),
		} {
			if d := a.Answer(q, job); d.Action != Choose || d.Value != "I don't wish to answer" {
				t.Errorf("%s %q: %+v", q.Kind, configured, d)
			}
		}
	}
}

func TestAConfiguredYesOrNoMapsOntoAWordedOption(t *testing.T) {
	for configured, want := range map[string]string{"Yes": "Yes, I will require sponsorship", "No": "No, I will not"} {
		cfg := pinned()
		cfg.Questions.RequireVisa = configured
		a, _ := answerer(t, cfg)
		d := a.Answer(dropdown("Will you now or in the future require sponsorship for employment visa status?", "Select an option", "Yes, I will require sponsorship", "No, I will not"), job)
		if d.Value != want {
			t.Errorf("%s: %+v", configured, d)
		}
	}
}

// ------------------------------------------------------------ radio
func TestRadioRouting(t *testing.T) {
	cfg := pinned()
	cfg.Personals.Veteran = "I am not a protected veteran"
	cfg.Personals.Disability = "No"
	a, _ := answerer(t, cfg)
	cases := []struct {
		name       string
		q          Question
		wantAction Action
		wantValue  string
	}{
		{"unrecognised clearance question is left alone", radio("Do you have an active security clearance?", "Yes", "No"), Blocked, ""},
		{"unrecognised experience question is left alone", radio("Do you have 10+ years of experience?", "Yes", "No"), Blocked, ""},
		{"configured veteran status selects its radio", radio("Please select your veteran status", "I identify as one or more of the classifications of a protected veteran", "I am not a protected veteran", "I don't wish to answer"), Choose, "I am not a protected veteran"},
		{"configured No reaches the worded option", radio("Do you have a disability?", "Yes, I have a disability", "No, I don't have a disability", "I don't wish to answer"), Choose, "No, I don't have a disability"},
		{"authorization radio", radio("Are you currently legally authorized to work in the United States?", "Yes", "No"), Choose, "Yes"},
		{"sponsorship radio", radio("Will you now or in the future require sponsorship for employment visa status?", "Yes", "No"), Choose, "Yes"},
		{"citizenship into yes/no never selects No", radio("Are you a US citizen?", "Yes", "No"), Blocked, ""},
		{"a selected radio is kept", Question{Label: "Shift", Kind: Radio, Options: []string{"Day", "Night"}, Current: "Night"}, Leave, "Night"},
	}
	for _, tc := range cases {
		d := a.Answer(tc.q, job)
		if d.Action != tc.wantAction || (d.Action != Blocked && d.Value != tc.wantValue) {
			t.Errorf("%s: got %+v, want %v %q", tc.name, d, tc.wantAction, tc.wantValue)
		}
	}
	cfg.Questions.RequireVisa = "No"
	a, _ = answerer(t, cfg)
	if d := a.Answer(radio("Precisa de patrocínio de visto?", "Sim, preciso", "Não, não preciso"), job); d.Option != 1 {
		t.Errorf("a configured No selects the Não option: %+v", d)
	}
}

// ------------------------------------------------------------ text
func TestTextClassificationsThatWereRightStillAnswer(t *testing.T) {
	a, _ := answerer(t, pinned())
	cases := map[string]string{
		"What is your phone number?":                     "5551234567",
		"What is your notice period in months?":          "2",
		"Notice period (weeks)":                          "8",
		"Aviso prévio (dias)":                            "60",
		"What is your expected salary?":                  "250000",
		"Expected monthly salary":                        "20833.33",
		"Current compensation":                           "200000",
		"LinkedIn profile URL":                           "https://linkedin.com/in/example",
		"What is your zip code?":                         "12345",
		"CEP":                                            "12345",
		"How many years of work experience do you have?": "6",
		"Anos de experiência":                            "6",
		"Tempo de experiência em anos":                   "6",
		"Years of experience (in years)":                 "6",
		"Please type your signature":                     "Jane Q Applicant",
		"Nome completo":                                  "Jane Q Applicant",
		"Sobrenome":                                      "Applicant",
		"First name":                                     "Jane",
		"Telefone":                                       "5551234567",
		"Celular":                                        "5551234567",
		"Estado":                                         state,
		"País":                                           "Testland",
		"Site ou portfólio":                              "https://example.dev",
		"Headline":                                       "Headline",
		"Current employer name":                          "ACME",
		"How did you hear about this job?":               "LinkedIn",
		"Rate yourself on a scale of 1-10":               "8",
		"Street address":                                 "1 Main St",
	}
	for label, want := range cases {
		d := a.Answer(text(label), job)
		if d.Action != Fill || d.Value != want {
			t.Errorf("%q: got %+v, want Fill %q", label, d, want)
		}
	}
}

func TestUnclassifiedAndNearMissTextQuestionsAreLeftEmpty(t *testing.T) {
	a, _ := answerer(t, pinned())
	for _, label := range []string{
		"How many years of Kubernetes experience do you have?",
		"How many years of experience do you have with Python?",
		"Anos de experiência com Python",
		"How many people did you manage?",
		"What is your expected bonus?",
		"Are you willing to work on-site?", // "site" is not website
		"Disponibilidade para viagens?",    // not a notice period
		"Estado civil",                     // marital status
		"Quantos anos você tem?",           // age
	} {
		if d := a.Answer(text(label), job); d.Action != Blocked || d.Value != "" {
			t.Errorf("%q: %+v", label, d)
		}
	}
}

func TestTheCityQuestionTypesTheCityAndIsATypeahead(t *testing.T) {
	cfg := pinned()
	cfg.Personals.City = "Manaus"
	a, _ := answerer(t, cfg)
	if d := a.Answer(text("Cidade"), job); d.Action != Fill || d.Value != "Manaus" || !d.Typeahead {
		t.Errorf("%+v", d)
	}
	cfg.Personals.City = ""
	a, _ = answerer(t, cfg)
	if d := a.Answer(text("City"), Job{WorkLocation: "Lisbon"}); d.Value != "Lisbon" {
		t.Errorf("falls back to the job's location: %+v", d)
	}
	if d := a.Answer(text("Localização"), Job{WorkLocation: "Remoto"}); d.Value != "Remoto" {
		t.Errorf("%+v", d)
	}
}

func TestBrazilianSalaryQuestionsGetTheMonthlyFigureUnlessAnnualIsAsked(t *testing.T) {
	cfg := pinned()
	cfg.Markets.Active = "brasil"
	cfg.Markets.Brasil.DesiredSalary, cfg.Markets.Brasil.CurrentSalary = 8000, 6500
	a, _ := answerer(t, cfg)
	cases := map[string]string{
		"Pretensão salarial mensal":          "8000",
		"Pretensão salarial":                 "8000", // no period: the market's own
		"Qual sua pretensão salarial anual?": "96000",
		"Salário atual":                      "6500",
		"Remuneração atual anual":            "78000",
	}
	for label, want := range cases {
		if d := a.Answer(text(label), job); d.Value != want {
			t.Errorf("%q: got %q want %q", label, d.Value, want)
		}
	}
	cfg = pinned()
	cfg.Markets.Internacional.DesiredSalary, cfg.Markets.Internacional.CurrentSalary = 90000, 70000
	a, _ = answerer(t, cfg)
	for label, want := range map[string]string{"Expected annual salary": "90000", "Expected salary": "90000", "Expected monthly salary": "7500", "Current compensation": "70000"} {
		if d := a.Answer(text(label), job); d.Value != want {
			t.Errorf("%q: got %q want %q", label, d.Value, want)
		}
	}
}

func TestPrefilledTextIsKeptUnlessOverwriteIsOn(t *testing.T) {
	cfg := pinned()
	a, _ := answerer(t, cfg)
	if d := a.Answer(Question{Label: "First name", Kind: Text, Current: "Prefilled"}, job); d.Action != Leave || d.Value != "Prefilled" {
		t.Errorf("%+v", d)
	}
	cfg.Questions.OverwritePrevious = true
	a, _ = answerer(t, cfg)
	if d := a.Answer(Question{Label: "First name", Kind: Text, Current: "Prefilled"}, job); d.Action != Fill || d.Value != "Jane" {
		t.Errorf("%+v", d)
	}
}

// ------------------------------------------------------------ textarea
func TestTextareaRouting(t *testing.T) {
	a, _ := answerer(t, pinned())
	if d := a.Answer(textarea("Summary"), job); d.Value != "Summary text" {
		t.Errorf("%+v", d)
	}
	if d := a.Answer(textarea("Carta de apresentação"), job); d.Value != "Cover text" {
		t.Errorf("%+v", d)
	}
	if d := a.Answer(textarea("Why do you want to work here?"), job); d.Action != Blocked {
		t.Errorf("%+v", d)
	}
}

// ------------------------------------------------------------ memory in forms
func TestAnUnrecognisedTextQuestionIsAnsweredFromMemoryEvenWhilePending(t *testing.T) {
	a, mem := answerer(t, pinned())
	if _, err := mem.Remember("Desired start date", "text", "ASAP", "ai", ""); err != nil {
		t.Fatal(err)
	}
	d := a.Answer(text("Desired start date"), job)
	if d.Action != Fill || d.Value != "ASAP" || d.Source != "memory" {
		t.Errorf("%+v", d)
	}
	found, _, _ := mem.Lookup("Desired start date", "text", false)
	if found.Uses != 1 || found.LastJobLink != job.Link {
		t.Errorf("use not recorded: %+v", found)
	}
}

func TestARewordedQuestionReusesTheAnswerButADifferentSkillDoesNot(t *testing.T) {
	a, mem := answerer(t, pinned())
	if _, err := mem.Remember("How many years of experience do you have with Python?", "text", "4", "user", ""); err != nil {
		t.Fatal(err)
	}
	if d := a.Answer(text("How many years experience do you have with Python?"), job); d.Value != "4" {
		t.Errorf("%+v", d)
	}
	if d := a.Answer(text("How many years of experience do you have with Kubernetes?"), job); d.Action != Blocked {
		t.Errorf("%+v", d)
	}
}

func TestTextareaAnswersHaveTheirOwnKind(t *testing.T) {
	a, mem := answerer(t, pinned())
	if _, err := mem.Remember("Why do you want to work here?", "textarea", "Because of the mission.", "ai", ""); err != nil {
		t.Fatal(err)
	}
	if d := a.Answer(textarea("Why do you want to work here?"), job); d.Value != "Because of the mission." {
		t.Errorf("%+v", d)
	}
	if d := a.Answer(text("Why do you want to work here?"), job); d.Action != Blocked {
		t.Errorf("a textarea memory must not fill a text input: %+v", d)
	}
}

func TestSensitiveQuestionsNeverReachMemory(t *testing.T) {
	cfg := pinned()
	cfg.Questions.DesiredSalary, cfg.Questions.CurrentSalary = 0, 0
	cfg.Questions.RequireVisa, cfg.Personals.Veteran = "", ""
	a, mem := answerer(t, cfg)
	for _, q := range []Question{
		text("Do you have a disability?"), text("Are you a protected veteran?"), text("Do you hold an active security clearance?"),
		dropdown("Will you require visa sponsorship?", "Select an option", "Yes", "No"),
		radio("Are you a protected veteran?", "Yes", "No"),
	} {
		if _, err := mem.Remember(q.Label, string(q.Kind), "leaked", "user", ""); err != nil {
			t.Fatal(err)
		}
		if d := a.Answer(q, job); d.Action != Blocked {
			t.Errorf("%q (%s): %+v", q.Label, q.Kind, d)
		}
	}
	// A salary question with the salary blank gets "0", not a leak: the figure is configured, just zero.
	if d := a.Answer(text("What are your salary expectations in USD?"), job); d.Source == "memory" {
		t.Errorf("salary came from memory: %+v", d)
	}
}

func TestTheEmailQuestionIsNeverGuessedOrRemembered(t *testing.T) {
	a, mem := answerer(t, pinned())
	ai := &fakeAI{replies: map[string]string{"Email address": "made.up@example.com"}}
	a.AI = ai
	if d := a.Answer(text("Email address"), job); d.Action != Blocked {
		t.Errorf("%+v", d)
	}
	if all, _ := mem.List(""); len(all) != 0 || len(ai.calls) != 0 {
		t.Errorf("email reached the AI (%d calls) or the memory (%d entries)", len(ai.calls), len(all))
	}
}

func TestSensitiveTextQuestionsNeverReachTheAIAndStayEmpty(t *testing.T) {
	cfg := pinned()
	cfg.Questions.DesiredSalary = 0
	a, _ := answerer(t, cfg)
	ai := &fakeAI{replies: map[string]string{}}
	a.AI = ai
	for _, label := range []string{"Do you have a disability?", "Are you a protected veteran?", "Do you hold an active security clearance?"} {
		ai.replies[label] = "guessed"
		if d := a.Answer(text(label), job); d.Action != Blocked || d.Value != "" {
			t.Errorf("%q: %+v", label, d)
		}
	}
	// A salary question is sensitive too; with the salary configured as 0 it is answered "0", never guessed.
	ai.replies["What are your salary expectations in USD?"] = "999999"
	if d := a.Answer(text("What are your salary expectations in USD?"), job); d.Value != "0" || d.Source != "config" {
		t.Errorf("%+v", d)
	}
	if len(ai.calls) != 0 {
		t.Errorf("AI asked: %v", ai.calls)
	}
}

func TestAnUnrelatedQuestionBelowTheThresholdGoesToTheAIAndIsRemembered(t *testing.T) {
	a, mem := answerer(t, pinned())
	if _, err := mem.Remember("How many years of experience do you have with Python?", "text", "4", "user", ""); err != nil {
		t.Fatal(err)
	}
	ai := &fakeAI{replies: map[string]string{"How many years of experience do you have with Kubernetes?": "2"}}
	a.AI = ai
	if d := a.Answer(text("How many years of experience do you have with Kubernetes?"), job); d.Value != "2" || d.Source != "ai" {
		t.Errorf("%+v", d)
	}
	if all, _ := mem.List(""); len(all) != 2 || len(ai.calls) != 1 {
		t.Errorf("%d entries, %d calls", len(all), len(ai.calls))
	}
}

func TestAnApproximateMemoryHitIsLoggedThroughTheFormPath(t *testing.T) {
	var log bytes.Buffer
	st, err := store.Open(filepath.Join(t.TempDir(), "aja.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	logger := slog.New(slog.NewTextHandler(&log, nil))
	mem := memory.New(st, logger)
	a := New(pinned(), mem, nil, logger)
	if _, err := mem.Remember("How many years of experience do you have with Python?", "text", "4", "user", ""); err != nil {
		t.Fatal(err)
	}
	if d := a.Answer(text("How many years experience do you have with Python?"), job); d.Value != "4" {
		t.Errorf("%+v", d)
	}
	if !strings.Contains(log.String(), "approximate") {
		t.Errorf("log lacks the approximation:\n%s", log.String())
	}
}

func TestOverwriteOnWithNoAnswerClearsThePrefillAndAPlaceholderWithSpacesIsAPlaceholder(t *testing.T) {
	cfg := pinned()
	cfg.Questions.OverwritePrevious = true
	a, _ := answerer(t, cfg)
	d := a.Answer(Question{Label: "How many people did you manage?", Kind: Text, Current: "12"}, job)
	if d.Action != Blocked || !d.Clear {
		t.Errorf("%+v", d)
	}
	a, _ = answerer(t, pinned())
	d = a.Answer(Question{Label: "Do you have an active security clearance?", Kind: Select, Options: []string{"Select an option ", "Yes", "No"}, Current: "Select an option "}, job)
	if d.Action != Blocked {
		t.Errorf("a placeholder with trailing spaces was taken as prefilled: %+v", d)
	}
	// An unknown dropdown already holding a real value, with overwrite on: reported, value kept.
	cfg.Questions.OverwritePrevious = true
	a, _ = answerer(t, cfg)
	d = a.Answer(Question{Label: "Favourite colour", Kind: Select, Options: []string{"Select an option", "Red", "Blue"}, Current: "Red"}, job)
	if d.Action != Blocked || d.Value != "Red" || !strings.Contains(d.Reason, "no configured, remembered or AI answer") {
		t.Errorf("%+v", d)
	}
}

func TestLakhsUsePythonsRounding(t *testing.T) {
	cfg := pinned()
	cfg.Questions.DesiredSalary = 1200000
	a, _ := answerer(t, cfg)
	if d := a.Answer(text("Expected CTC in lakhs"), job); d.Value != "12.0" {
		t.Errorf("%+v", d)
	}
	cfg.Questions.DesiredSalary = 275000
	a, _ = answerer(t, cfg)
	if d := a.Answer(text("Expected CTC in lakhs"), job); d.Value != "2.75" {
		t.Errorf("%+v", d)
	}
}

func TestARadioOptionWithExtraWhitespaceStillMatchesExactly(t *testing.T) {
	cfg := pinned()
	cfg.Personals.Veteran = "I am not a protected veteran"
	a, _ := answerer(t, cfg)
	if d := a.Answer(radio("Veteran status", " I am not a  protected veteran ", "Other"), job); d.Option != 0 || d.Source != "config" {
		t.Errorf("%+v", d)
	}
}

func TestRememberedDropdownAndRadioAnswersAreSnappedToThisFormsOptions(t *testing.T) {
	a, mem := answerer(t, pinned())
	must := func(label, kind, answer, source string) {
		t.Helper()
		if _, err := mem.Remember(label, kind, answer, source, ""); err != nil {
			t.Fatal(err)
		}
	}
	must("Are you willing to relocate?", "select", "Yes", "user")
	must("Preferred work style", "select", "Hybrid", "ai")
	must("Do you have a driver's licence?", "radio", "No", "ai")
	must("Preferred shift", "radio", "Night", "user")
	cases := []struct {
		q         Question
		wantValue string
		blocked   bool
	}{
		{dropdown("Are you willing to relocate?", "Select an option", "Yes", "No"), "Yes", false},
		{dropdown("Are you willing to relocate?", "Select an option", "Yes, I am", "No, I am not"), "Yes, I am", false},
		{dropdown("Preferred work style", "Select an option", "Remote", "Hybrid", "On-site"), "Hybrid", false},
		{dropdown("Preferred work style", "Select an option", "Remote", "On-site"), "", true},
		{radio("Do you have a driver's licence?", "Yes, I do", "No, I do not"), "No, I do not", false},
		{radio("Preferred shift", "Morning", "Afternoon"), "", true},
	}
	for _, tc := range cases {
		d := a.Answer(tc.q, job)
		if (d.Action == Blocked) != tc.blocked || (!tc.blocked && d.Value != tc.wantValue) {
			t.Errorf("%q %v: %+v", tc.q.Label, tc.q.Options, d)
		}
	}
	// A dropdown memory never fills a text control of the same label.
	if d := a.Answer(text("Are you willing to relocate?"), job); d.Action != Blocked {
		t.Errorf("%+v", d)
	}
}

func TestMemoryIsNotConsultedForARecognisedDropdownButIsWhenTheSettingIsBlank(t *testing.T) {
	cfg := pinned()
	cfg.Personals.Gender = "Nonbinary"
	a, mem := answerer(t, cfg)
	if _, err := mem.Remember("Gender", "select", "Male", "user", ""); err != nil {
		t.Fatal(err)
	}
	if d := a.Answer(dropdown("Gender", "Select an option", "Male", "Female"), job); d.Action != Blocked {
		t.Errorf("configured answers rule: %+v", d)
	}
	cfg.Personals.Gender = ""
	a = New(cfg, mem, nil, nil)
	if d := a.Answer(dropdown("Gender", "Select an option", "Male", "Female"), job); d.Value != "Male" || d.Source != "memory" {
		t.Errorf("blank setting lets memory answer: %+v", d)
	}
}

// ------------------------------------------------------------ checkbox
func TestCheckboxesTickOnlyFromAnApprovedAnswerAndAreOfferedForReview(t *testing.T) {
	a, mem := answerer(t, pinned())
	if _, err := mem.Remember("Subscribe to job alerts", "checkbox", "checked", "user", ""); err != nil {
		t.Fatal(err)
	}
	if d := a.Answer(checkbox("Subscribe to job alerts", ""), job); d.Action != Tick || d.Source != "memory" {
		t.Errorf("%+v", d)
	}
	if _, err := mem.Remember("Newsletter", "checkbox", "checked", "form", ""); err != nil { // pending
		t.Fatal(err)
	}
	if d := a.Answer(checkbox("Newsletter", ""), job); d.Action != Blocked {
		t.Errorf("a pending answer never ticks: %+v", d)
	}
	for _, attestation := range []string{"I certify that the information provided is accurate", "I agree to the terms and conditions", "I consent to a background check"} {
		d := a.Answer(checkbox(attestation, ""), job)
		if d.Action != Blocked || !strings.Contains(d.Reason, "attestation") {
			t.Errorf("%q: %+v", attestation, d)
		}
		found, _, _ := mem.Lookup(attestation, "checkbox", false)
		if found == nil || found.State != "pending" || found.Source != "form" || found.LastJobLink != job.Link {
			t.Errorf("%q should be offered for review: %+v", attestation, found)
		}
	}
	if d := a.Answer(checkbox("Please tick this box", ""), job); d.Action != Blocked || !strings.Contains(d.Reason, "classified") {
		t.Errorf("%+v", d)
	}
	if d := a.Answer(Question{Label: "I agree to the terms", Kind: Checkbox, Current: "checked"}, job); d.Action != Leave {
		t.Errorf("a ticked box is the user's: %+v", d)
	}
}

func TestCheckboxMatchingIsExactAndSensitiveBoxesAreNeverTickedNorOffered(t *testing.T) {
	a, mem := answerer(t, pinned())
	if _, err := mem.Remember("I agree to the terms and conditions of Acme", "checkbox", "checked", "user", ""); err != nil {
		t.Fatal(err)
	}
	if d := a.Answer(checkbox("I agree to the terms and conditions of Acme Inc", ""), job); d.Action != Blocked {
		t.Errorf("near-identical attestation must not tick: %+v", d)
	}
	before, _ := mem.List("")
	for _, s := range []string{"I certify that I am a U.S. citizen", "I confirm I do not require visa sponsorship", "I hold an active security clearance"} {
		if _, err := mem.Remember(s, "checkbox", "checked", "user", ""); err != nil {
			t.Fatal(err)
		}
		if d := a.Answer(checkbox(s, ""), job); d.Action != Blocked {
			t.Errorf("%q: %+v", s, d)
		}
	}
	after, _ := mem.List("")
	if len(after) != len(before)+3 {
		t.Errorf("sensitive boxes were offered for review: %d -> %d", len(before), len(after))
	}
	// A box with no text at all is never remembered, even with an approved "Unknown".
	if _, err := mem.Remember("Unknown", "checkbox", "checked", "user", ""); err != nil {
		t.Fatal(err)
	}
	n := len(after) + 1
	if d := a.Answer(checkbox(UnknownTitle, ""), job); d.Action != Blocked {
		t.Errorf("%+v", d)
	}
	if all, _ := mem.List(""); len(all) != n {
		t.Errorf("an unlabeled box was remembered")
	}
}

func TestTheGroupTitleIsPartOfTheCheckboxQuestion(t *testing.T) {
	a, mem := answerer(t, pinned())
	a.Answer(checkbox("Email", "How may we contact you?"), job)
	if found, _, _ := mem.Lookup("How may we contact you? Email", "checkbox", false); found == nil {
		t.Error("not remembered under the group title")
	}
	if CheckboxKey(UnknownTitle, "Email") != "Email" || CheckboxKey("", UnknownTitle) != "" {
		t.Error("CheckboxKey")
	}
}

// ------------------------------------------------------------ option mapping
func TestMatchAnswerToOption(t *testing.T) {
	cases := []struct {
		answer  string
		options []string
		want    int
	}{
		{"No", []string{"Sim, mas não agora", "Não"}, 1},
		{"Yes", []string{"Sim", "Não"}, 0},
		{"Decline", []string{"Sim", "Não", "Prefiro não informar"}, 2},
		{"Yes", []string{"I do not wish to answer", "I do"}, 1},
		{"No", []string{"Yes, I do", "No, I do not"}, 1},
		{citizenship, []string{"Yes", "No"}, -1},
		{"", []string{"Yes", "No"}, -1},
		{"Hybrid", []string{"Remote", "Hybrid"}, 1},
		{"Night", []string{"Morning", "Afternoon"}, -1},
	}
	for _, tc := range cases {
		if got := MatchAnswerToOption(tc.answer, tc.options); got != tc.want {
			t.Errorf("MatchAnswerToOption(%q, %v) = %d, want %d", tc.answer, tc.options, got, tc.want)
		}
	}
}

// ------------------------------------------------------------ AI (fake) in option controls
type fakeAI struct {
	replies map[string]string
	calls   []string
	options [][]string
	kinds   []Kind
}

func (f *fakeAI) Answer(_ context.Context, question string, kind Kind, options []string, _ string) (string, error) {
	f.calls = append(f.calls, question)
	f.options = append(f.options, options)
	f.kinds = append(f.kinds, kind)
	return f.replies[question], nil
}

func TestTheAIIsShownOnlyRealOptionsAndARejectedAnswerIsNotRemembered(t *testing.T) {
	a, mem := answerer(t, pinned())
	ai := &fakeAI{replies: map[string]string{"Preferred shift": "Evening", "Willing to travel?": "No"}}
	a.AI = ai
	if d := a.Answer(dropdown("Preferred shift", "Select an option", "Day", "Night"), job); d.Action != Blocked {
		t.Errorf("an answer that is not an option leaves the dropdown alone: %+v", d)
	}
	if strings.Join(ai.options[0], "|") != "Day|Night" {
		t.Errorf("the placeholder was offered: %v", ai.options[0])
	}
	if found, _ := mem.Find("Preferred shift", "select"); found != nil {
		t.Errorf("a rejected AI answer was remembered: %+v", found)
	}
	d := a.Answer(radio("Willing to travel?", "Yes", "No"), job)
	if d.Action != Choose || d.Value != "No" || d.Source != "ai" {
		t.Errorf("%+v", d)
	}
	found, _ := mem.Find("Willing to travel?", "radio")
	if found == nil || found.Answer != "No" || found.State != "pending" || found.Source != "ai" || found.Uses != 1 {
		t.Errorf("an AI radio answer is remembered as pending: %+v", found)
	}
}

func TestMemoryIsConsultedBeforeTheAIAndAITextAnswersKeepTheirKind(t *testing.T) {
	a, mem := answerer(t, pinned())
	ai := &fakeAI{replies: map[string]string{"Are you willing to relocate?": "No", "Why do you want to work here?": "The mission.", "Favourite editor?": "Vim"}}
	a.AI = ai
	if _, err := mem.Remember("Are you willing to relocate?", "select", "Yes", "user", ""); err != nil {
		t.Fatal(err)
	}
	if d := a.Answer(dropdown("Are you willing to relocate?", "Select an option", "Yes", "No"), job); d.Value != "Yes" || d.Source != "memory" || len(ai.calls) != 0 {
		t.Errorf("%+v calls=%v", d, ai.calls)
	}
	a.Answer(textarea("Why do you want to work here?"), job)
	a.Answer(text("Favourite editor?"), job)
	if strings.Join([]string{string(ai.kinds[0]), string(ai.kinds[1])}, ",") != "textarea,text" {
		t.Errorf("kinds %v", ai.kinds)
	}
	for label, kind := range map[string]string{"Why do you want to work here?": "textarea", "Favourite editor?": "text"} {
		found, _ := mem.Find(label, kind)
		if found == nil || found.State != "pending" || found.Source != "ai" || found.Kind != kind {
			t.Errorf("%s: %+v", label, found)
		}
	}
}

func TestTheAIFillsAnUnrecognisedControlOnlyWhenItNamesAnOptionAndIsRemembered(t *testing.T) {
	a, mem := answerer(t, pinned())
	ai := &fakeAI{replies: map[string]string{"Preferred work style": "Hybrid", "Favourite language": "Python", "Coffee or tea?": "Mate"}}
	a.AI = ai
	if d := a.Answer(dropdown("Preferred work style", "Select an option", "Remote", "Hybrid"), job); d.Value != "Hybrid" || d.Source != "ai" {
		t.Errorf("%+v", d)
	}
	if found, _, _ := mem.Lookup("Preferred work style", "select", false); found == nil || found.State != "pending" || found.Uses != 1 {
		t.Errorf("AI pick not remembered as pending: %+v", found)
	}
	if d := a.Answer(radio("Coffee or tea?", "Coffee", "Tea"), job); d.Action != Blocked {
		t.Errorf("an answer that is not an option leaves the group alone: %+v", d)
	}
	if d := a.Answer(text("Favourite language"), job); d.Value != "Python" || d.Source != "ai" {
		t.Errorf("%+v", d)
	}
	// Second time: memory, no AI call.
	calls := len(ai.calls)
	if d := a.Answer(text("Favourite language"), job); d.Source != "memory" || len(ai.calls) != calls {
		t.Errorf("%+v %d", d, len(ai.calls))
	}
	// Sensitive and configured questions never reach the AI.
	a.Answer(dropdown("Will you require visa sponsorship?", "Select an option", "Yes", "No"), job)
	a.Answer(dropdown("Gender", "Select an option", "Male", "Female"), job)
	for _, c := range ai.calls {
		if c == "Will you require visa sponsorship?" || c == "Gender" {
			t.Errorf("AI asked %q", c)
		}
	}
}

// ------------------------------------------------------------ learning from the user
func TestCaptureRemembersWhatTheUserTypedAsApprovedExceptSensitiveAndBlank(t *testing.T) {
	a, mem := answerer(t, pinned())
	if _, err := mem.Remember("Favourite language", "text", "Python", "ai", ""); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		q     Question
		value string
		want  bool
	}{
		{text("Favourite language"), "Go", true}, // the stale AI entry gives way
		{dropdown("Preferred shift", "Select an option", "Day", "Night"), "Night", true},
		{radio("Relocate?", "Yes", "No"), "No", true},
		{checkbox("Email", "How may we contact you?"), "checked", true},
		{textarea("Why us?"), "Because.", true},
		{text("What are your salary expectations?"), "90000", false},
		{text(UnknownTitle), "x", false},
		{text("Empty"), "   ", false},
		{dropdown("Shift?", "Select an option", "Day"), "Select an option", false},
	}
	rememberable := Decision{Action: Blocked, Remember: true}
	for _, tc := range cases {
		if got := a.Capture(tc.q, rememberable, tc.value, job.Link); got != tc.want {
			t.Errorf("Capture(%q, %q) = %v", tc.q.Label, tc.value, got)
		}
	}
	// A question the memory is never consulted for is never captured.
	for _, q := range []Question{text("Email address"), dropdown("Gender", "Select an option", "Male", "Female")} {
		if a.Capture(q, Decision{Action: Blocked, Remember: false}, "x", job.Link) {
			t.Errorf("%q captured although its answer would never be used", q.Label)
		}
	}
	found, _, _ := mem.Lookup("Favourite language", "text", false)
	if found.Answer != "Go" || found.State != "approved" || found.Source != "user" {
		t.Errorf("%+v", found)
	}
	if found, _, _ := mem.Lookup("How may we contact you? Email", "checkbox", false); found == nil || found.Answer != "checked" {
		t.Errorf("checkbox captured under its group title: %+v", found)
	}
	if found, _, _ := mem.Lookup("What are your salary expectations?", "text", false); found != nil {
		t.Error("a sensitive answer was captured")
	}
}

func TestUnansweredQuestionsAreQueuedForTheUserButNeverSensitiveOnesOrConfigProblems(t *testing.T) {
	cfg := pinned()
	cfg.Personals.Gender = "Nonbinary" // configured, but no option fits: a configuration problem
	a, mem := answerer(t, cfg)
	for _, tc := range []struct {
		q     Question
		queue bool
	}{
		{text("Favourite colour?"), true},
		{dropdown("Preferred shift", "Select an option", "Day", "Night"), true},
		{textarea("Why us?"), true},
		{text("Email address"), false},
		{text("Do you hold an active security clearance?"), false},
		{dropdown("Gender", "Select an option", "Male", "Female"), false},
	} {
		d := a.Answer(tc.q, job)
		if d.Action != Blocked || d.Remember != tc.queue {
			t.Errorf("%q: %+v", tc.q.Label, d)
		}
		if got := a.RememberUnanswered(tc.q, d, job.Link); got != tc.queue {
			t.Errorf("%q queued = %v", tc.q.Label, got)
		}
	}
	pending, _ := mem.List("pending")
	if len(pending) != 3 {
		t.Fatalf("%+v", pending)
	}
	for _, p := range pending {
		if p.Answer != "" || p.Source != "form" {
			t.Errorf("%+v", p)
		}
	}
	// A queued question with no answer yet is a memory miss: the AI is still asked next time.
	ai := &fakeAI{replies: map[string]string{"Favourite colour?": "Blue"}}
	a.AI = ai
	if d := a.Answer(text("Favourite colour?"), job); d.Value != "Blue" || d.Source != "ai" {
		t.Errorf("%+v", d)
	}
	// ...and queuing never overwrites an entry that has an answer.
	if a.RememberUnanswered(text("Favourite colour?"), Decision{Action: Blocked, Remember: true}, job.Link) {
		t.Error("an answered entry was re-queued")
	}
	if found, _, _ := mem.Lookup("Favourite colour?", "text", false); found.Answer != "Blue" {
		t.Errorf("%+v", found)
	}
}

func TestTheAIIsGivenTheContext(t *testing.T) {
	a, _ := answerer(t, pinned())
	ctxAI := &ctxRecorder{}
	a.AI = ctxAI
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.AnswerContext(ctx, text("Favourite colour?"), job)
	if ctxAI.got != ctx {
		t.Error("the AI did not receive the caller's context")
	}
}

type ctxRecorder struct{ got context.Context }

func (c *ctxRecorder) Answer(ctx context.Context, _ string, _ Kind, _ []string, _ string) (string, error) {
	c.got = ctx
	return "", nil
}

func TestGenderAndEthnicityAreAnsweredFromTheConfigInDropdownsAndRadios(t *testing.T) {
	cfg := pinned()
	cfg.Personals.Gender, cfg.Personals.Ethnicity = "Female", "Hispanic/Latino"
	a, _ := answerer(t, cfg)
	cases := []struct {
		q    Question
		want string
	}{
		{dropdown("What is your race/ethnicity?", "Select an option", "Asian", "Hispanic/Latino", "White", "I don't wish to answer"), "Hispanic/Latino"},
		{radio("Ethnicity", "Asian", "Hispanic/Latino", "Decline to self-identify"), "Hispanic/Latino"},
		{radio("Gender", "Male", "Female", "Prefer not to say"), "Female"},
		{dropdown("Qual é a sua cor ou raça?", "Select an option", "Branca", "Hispanic/Latino", "Prefiro não informar"), "Hispanic/Latino"},
	}
	for _, tc := range cases {
		if d := a.Answer(tc.q, job); d.Action != Choose || d.Value != tc.want || d.Source != "config" {
			t.Errorf("%q: %+v", tc.q.Label, d)
		}
	}
	// "Decline" (the default) lands on the option that declines.
	a, _ = answerer(t, pinned())
	if d := a.Answer(radio("Ethnicity", "Asian", "White", "I don't wish to answer"), job); d.Value != "I don't wish to answer" {
		t.Errorf("%+v", d)
	}
	// Words that merely contain "race" are not ethnicity questions.
	if d := a.Answer(text("Do you embrace challenges?"), job); d.Source == "config" {
		t.Errorf("%+v", d)
	}
}
