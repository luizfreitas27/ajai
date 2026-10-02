package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/store"
)

const pyUserConfig = `{
  "personals": {"first_name": "Ana", "last_name": "Silva", "phone_number": "11999", "current_city": "Rio", "zipcode": "20000"},
  "questions": {"default_resume_path": "all resumes/default/resume.pdf", "desired_salary": 120000, "current_ctc": 96000,
                "user_information_all": "Go dev", "linkedIn": "https://linkedin.com/in/ana", "pause_before_submit": true,
                "years_of_experience": "7"},
  "search": {"search_terms": ["Dev Python", "QA"], "search_location": "Brasil", "location": ["Rio"], "on_site": ["Remote"],
             "experience_level": ["Mid-Senior level"], "current_experience": 5, "switch_number": 10},
  "settings": {"stop_before_submit": true, "alternate_sortby": false, "safe_mode": false, "mystery": 1},
  "secrets": {"use_AI": true, "ai_provider": "lm studio", "llm_model": "qwen", "llm_api_url": "http://localhost:1234/v1/",
              "llm_api_key": "sk-NEVER", "telegram_bot_token": "tok-NEVER", "telegram_chat_id": "", "username": "me@x", "password": "pw"},
  "markets": {"market": "brasil", "br_desired_salary_monthly": 9000, "br_search_terms": ["Engenheiro de Software"]}
}`

const pyAnswers = `{"version": 1, "answers": [
 {"label": "I consent to a background check", "kind": "checkbox", "answer": "checked", "source": "form", "state": "pending",
  "normalised": "i consent to a background check", "id": "15ef2bdabac3", "uses": 0, "last_job_link": "", "created_at": "2026-10-01T16:45:25", "updated_at": "2026-10-01T16:45:25"},
 {"label": "Anos com Go?", "kind": "text", "answer": "3", "source": "user", "state": "approved", "id": "aaa", "uses": 4, "last_job_link": "https://l/1"},
 {"label": "", "kind": "text"}
]}`

const pyApplied = "Job ID,Title,Company,Work Location,Work Style,About Job,Experience required,Skills required,HR Name,HR Link,Resume,Re-posted,Date Posted,Date Applied,Job Link,External Job link,Questions Found,Connect Request\n" +
	"111,Dev,ACME,Rio,Remote,desc,3,\"['go', 'sql']\",Unknown,Unknown,resume.pdf,False,2026-09-30,2026-10-01 10:00:00,https://l/111,,set(),In Development\n" +
	"222,Dev Ext,Other,SP,Hybrid,desc,Unknown,,Unknown,Unknown,resume.pdf,False,2026-09-30,Pending,https://l/222,https://other/apply,,In Development\n"

const pyFailed = "Job ID,Job Link,Resume Tried,Date listed,Date Tried,Assumed Reason,Stack Trace,External Job link,Screenshot Name\n" +
	"333,https://l/333,resume.pdf,2026-09-30,2026-10-01 11:00:00,modal closed,Traceback...,,333.png\n"

func pythonCheckout(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("user_config.json", pyUserConfig)
	write("answers_memory.json", pyAnswers)
	write("all excels/all_applied_applications_history.csv", pyApplied)
	write("all excels/all_failed_applications_history.csv", pyFailed)
	write("all resumes/default/resume.pdf", "%PDF-1.4 fake")
	return src
}

func setup(t *testing.T) (paths.Paths, *store.Store) {
	t.Helper()
	home := t.TempDir()
	p := paths.Resolve("", func(k string) string {
		if k == "AJA_HOME" {
			return home
		}
		return ""
	})
	st, err := store.Open(p.DBFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return p, st
}

func TestImportMapsTheConfigAndNeverCopiesSecrets(t *testing.T) {
	src := pythonCheckout(t)
	p, st := setup(t)
	cfg := config.Defaults()
	rep, err := Run(src, &cfg, st, p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Personals.FirstName != "Ana" || cfg.Personals.Phone != "11999" || cfg.Personals.City != "Rio" || cfg.Personals.Zip != "20000" {
		t.Errorf("personals %+v", cfg.Personals)
	}
	if cfg.Questions.Resume != "resume.pdf" || cfg.Questions.DesiredSalary != 120000 || cfg.Questions.CurrentSalary != 96000 ||
		cfg.Questions.About != "Go dev" || cfg.Questions.LinkedIn != "https://linkedin.com/in/ana" || cfg.Questions.YearsOfExperience != "7" {
		t.Errorf("questions %+v", cfg.Questions)
	}
	if strings.Join(cfg.Search.Terms, "|") != "Dev Python|QA" || cfg.Search.Location != "Brasil" || cfg.Search.Locations[0] != "Rio" ||
		cfg.Search.OnSite[0] != "Remote" || cfg.Search.CurrentExperience != 5 || cfg.Search.SwitchNumber != 10 {
		t.Errorf("search %+v", cfg.Search)
	}
	if cfg.Settings.AlternateSortBy {
		t.Error("alternate_sortby false not applied")
	}
	if !cfg.AI.Enabled || cfg.AI.Provider != "lmstudio" || cfg.AI.Model != "qwen" || cfg.AI.BaseURL != "" {
		t.Errorf("ai %+v (default base url must stay blank)", cfg.AI)
	}
	if cfg.Markets.Active != "brasil" || cfg.Markets.Brasil.DesiredSalary != 9000 || cfg.Markets.Brasil.Terms[0] != "Engenheiro de Software" {
		t.Errorf("markets %+v", cfg.Markets)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("imported config invalid: %v", err)
	}
	if strings.Join(rep.SecretsSeen, " ") != config.EnvAIAPIKey+" "+config.EnvTelegramBotToken {
		t.Errorf("secrets seen %v (blank chat id must not be listed)", rep.SecretsSeen)
	}
	data, _ := config.Marshal(cfg)
	for _, never := range []string{"sk-NEVER", "tok-NEVER", "me@x", "pw"} {
		if strings.Contains(string(data), never) {
			t.Errorf("secret %q leaked into the config", never)
		}
	}
	for _, key := range []string{"questions.pause_before_submit", "settings.stop_before_submit", "settings.safe_mode", "secrets.username", "secrets.password"} {
		if _, ok := rep.Unmapped[key]; !ok {
			t.Errorf("%s should be reported as superseded", key)
		}
	}
	if rep.Unmapped["settings.mystery"] != "no aja counterpart" {
		t.Errorf("unknown key: %v", rep.Unmapped["settings.mystery"])
	}
	if rep.ResumeCopied != filepath.Join(p.ResumeDir, "resume.pdf") {
		t.Errorf("resume copied to %q", rep.ResumeCopied)
	}
	if _, err := os.Stat(rep.ResumeCopied); err != nil {
		t.Error("resume file not copied")
	}
}

func TestImportBringsAnswersAndJobsAndIsIdempotent(t *testing.T) {
	src := pythonCheckout(t)
	p, st := setup(t)
	for i := 0; i < 2; i++ {
		cfg := config.Defaults()
		rep, err := Run(src, &cfg, st, p)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Answers != 2 || rep.Applied != 1 || rep.External != 1 || rep.Failed != 1 {
			t.Errorf("run %d counts: %+v", i, rep)
		}
		if _, ok := rep.Unmapped["answers_memory.json entry "]; !ok {
			t.Error("the malformed answer should be reported")
		}
	}
	answers, _ := st.ListAnswers("")
	if len(answers) != 2 {
		t.Fatalf("answers duplicated: %d", len(answers))
	}
	byID := map[string]store.Answer{}
	for _, a := range answers {
		byID[a.ID] = a
	}
	if a := byID["15ef2bdabac3"]; a.State != "pending" || a.Source != "form" || a.Kind != "checkbox" || a.CreatedAt != "2026-10-01T16:45:25" {
		t.Errorf("%+v", a)
	}
	if a := byID["aaa"]; a.State != "approved" || a.Uses != 4 || a.Normalised != "anos com go" || a.LastJobLink != "https://l/1" {
		t.Errorf("normalised derived when missing: %+v", a)
	}
	counts, _ := st.CountJobs()
	if counts[store.Applied] != 1 || counts[store.External] != 1 || counts[store.Failed] != 1 {
		t.Errorf("jobs %v", counts)
	}
	jobs, _ := st.ListJobs(store.JobFilter{Outcomes: []store.Outcome{store.Failed}})
	if jobs[0].Screenshot != "333.png" || jobs[0].Reason != "modal closed" {
		t.Errorf("%+v", jobs[0])
	}
}

func TestImportOfABareCheckoutReportsWhatWasMissing(t *testing.T) {
	p, st := setup(t)
	cfg := config.Defaults()
	rep, err := Run(t.TempDir(), &cfg, st, p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ConfigWritten || rep.Answers != 0 || rep.ResumeMissing == "" || !strings.Contains(rep.Unmapped["user_config.json"], "not found") {
		t.Errorf("%+v", rep)
	}
	if _, err := Run(filepath.Join(t.TempDir(), "nope"), &cfg, st, p); err == nil {
		t.Error("a missing directory should be an error")
	}
}

func TestProviderMapping(t *testing.T) {
	cases := []struct{ prov, url, wantProv, wantURL string }{
		{"openai", "https://api.openai.com/v1/", "openai", ""},
		{"lm studio", "http://localhost:1234/v1/", "lmstudio", ""},
		{"ollama", "http://nas:11434/v1/", "ollama", "http://nas:11434/v1/"},
		{"google", "", "gemini", ""},
		{"my-proxy", "https://proxy/v1", "custom", "https://proxy/v1"},
		{"", "", "openai", ""},
	}
	for _, tc := range cases {
		p, u := providerFromPython(tc.prov, tc.url)
		if p != tc.wantProv || u != tc.wantURL {
			t.Errorf("(%q,%q) = %q,%q want %q,%q", tc.prov, tc.url, p, u, tc.wantProv, tc.wantURL)
		}
	}
}

func TestNormaliseMatchesThePythonFunction(t *testing.T) {
	cases := map[string]string{
		"I consent to a background check": "i consent to a background check",
		"  Years   of GO (total)? ":       "years of go total",
		"Anos com Go?":                    "anos com go",
		"":                                "",
	}
	for in, want := range cases {
		if got := Normalise(in); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}
