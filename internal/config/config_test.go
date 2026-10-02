package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/paths"
)

func tmpPaths(t *testing.T) paths.Paths {
	t.Helper()
	return paths.Resolve("", func(k string) string {
		if k == "AJA_HOME" {
			return t.TempDir()
		}
		return ""
	})
}

func valid() Config {
	c := Defaults()
	c.Personals.FirstName, c.Personals.LastName = "Ana", "Silva"
	c.Search.Terms = []string{"Software Engineer"}
	return c
}

// ------------------------------------------------------------ defaults & validation
func TestDefaultsNeedOnlyNameAndSearchTermsToBeValid(t *testing.T) {
	c := Defaults()
	err := c.Validate()
	ve, ok := AsValidation(err)
	if !ok {
		t.Fatalf("expected validation error, got %v", err)
	}
	keys := []string{}
	for _, e := range ve.Errors {
		keys = append(keys, e.Key)
	}
	want := "personals.first_name personals.last_name search.terms"
	if got := strings.Join(keys, " "); got != want {
		t.Errorf("missing-value keys = %q, want %q", got, want)
	}
	c = valid()
	if err := c.Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestMarketTermsSatisfyTheSearchTermsRule(t *testing.T) {
	c := valid()
	c.Search.Terms = nil
	c.Markets.Active = "brasil"
	c.Markets.Brasil.Terms = []string{"Dev Python"}
	if err := c.Validate(); err != nil {
		t.Errorf("market terms should count: %v", err)
	}
}

func TestClosedOptionSetsAreQuotedInTheMessage(t *testing.T) {
	c := valid()
	c.Search.SortBy = "Newest"
	c.Search.OnSite = []string{"Remote", "Moon"}
	c.Markets.Active = "europa"
	err := c.Validate()
	msg := err.Error()
	for _, want := range []string{
		`search.sort_by: "Newest" is not a valid value; valid values: "", "Most recent", "Most relevant"`,
		`search.on_site: "Moon" is not a valid value`,
		`markets.active: "europa" is not a valid value; valid values: "brasil", "internacional"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, `"Remote" is not`) {
		t.Error("a valid list member was reported")
	}
}

func TestMinimumsCurrenciesAndPacingRanges(t *testing.T) {
	c := valid()
	c.Search.SwitchNumber = 0
	c.Questions.DesiredSalary = -1
	c.Markets.Brasil.Currency = "R$"
	c.Pacing.BetweenJobsMax = 10 // below the 30 minimum default
	msg := c.Validate().Error()
	for _, want := range []string{
		"search.switch_number: must be at least 1 (got 0)",
		"questions.desired_salary: must be at least 0 (got -1)",
		`markets.brasil.currency: "R$" is not a three-letter ISO 4217 code`,
		"pacing.between_jobs_max_seconds: must not be below its minimum (10 < 30)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestAIRequiresAModelWhenEnabledAndABaseURLWhenCustom(t *testing.T) {
	c := valid()
	c.AI.Enabled, c.AI.Model, c.AI.Provider = true, "", "custom"
	msg := c.Validate().Error()
	if !strings.Contains(msg, "ai.model: must be set") || !strings.Contains(msg, "ai.base_url: must be set") {
		t.Error(msg)
	}
	c.AI.Provider, c.AI.Model = "gemini", "gemini-pro"
	if c.AIBaseURL() != ProviderBaseURLs["gemini"] {
		t.Errorf("base url %s", c.AIBaseURL())
	}
}

// ------------------------------------------------------------ get / set
func TestGetAndSetCoerceEveryKind(t *testing.T) {
	c := valid()
	cases := []struct{ key, in, out string }{
		{"search.terms", "a, b ,,c", "a, b, c"},
		{"search.terms", "", ""},
		{"search.switch_number", " 12 ", "12"},
		{"search.easy_apply_only", "FALSE", "false"},
		{"personals.first_name", "João", "João"},
		{"markets.brasil.terms", "Dev", "Dev"},
		{"pacing.max_per_run", "7", "7"},
	}
	for _, tc := range cases {
		if err := c.Set(tc.key, tc.in); err != nil {
			t.Fatalf("set %s: %v", tc.key, err)
		}
		got, err := c.Get(tc.key)
		if err != nil || got != tc.out {
			t.Errorf("get %s = %q (%v), want %q", tc.key, got, err, tc.out)
		}
	}
	if len(c.Search.Terms) != 0 {
		t.Errorf("empty string should clear the list, got %v", c.Search.Terms)
	}
}

func TestSetRejectsBadTypesAndUnknownKeysWithSuggestions(t *testing.T) {
	c := valid()
	if err := c.Set("search.switch_number", "many"); err == nil || !strings.Contains(err.Error(), "not a whole number") {
		t.Errorf("int: %v", err)
	}
	if err := c.Set("search.easy_apply_only", "yes please"); err == nil || !strings.Contains(err.Error(), "not a boolean") {
		t.Errorf("bool: %v", err)
	}
	err := c.Set("search.term", "x")
	var uk *UnknownKeyError
	if !asUnknown(err, &uk) || !contains(uk.Suggestions, "search.terms") {
		t.Errorf("unknown key: %v", err)
	}
}

func asUnknown(err error, target **UnknownKeyError) bool {
	e, ok := err.(*UnknownKeyError)
	if ok {
		*target = e
	}
	return ok
}

// ------------------------------------------------------------ write / load round trip
func TestWrittenFileIsCommentedAndRoundTrips(t *testing.T) {
	p := tmpPaths(t)
	c := valid()
	c.Search.Terms = []string{"Dev Python", "Engenheiro de Software"}
	if err := Write(p.ConfigFile, c); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(p.ConfigFile)
	for _, want := range []string{
		"# aja configuration.",
		"# Which market this run applies in.",
		`# Options: "brasil", "internacional"`,
		"# Example: Software Engineer,Python Developer",
		"desired_salary: 0",
		"experience_level: []",
		"- Engenheiro de Software",
	} {
		if !strings.Contains(string(text), want) {
			t.Errorf("file lacks %q", want)
		}
	}
	// Every key documented: no field without a comment line right above it.
	for _, f := range Fields() {
		if f.Doc == "" {
			t.Errorf("%s has no doc tag", f.Key)
		}
	}
	l, err := Load(p, MapEnv(nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !l.FileExisted || l.Config.Search.Terms[1] != "Engenheiro de Software" || l.Config.Pacing.MaxPerHour != 15 {
		t.Errorf("round trip lost data: %+v", l.Config.Search)
	}
}

func TestLoadWithoutAFileUsesDefaultsAndReportsIt(t *testing.T) {
	p := tmpPaths(t)
	l, err := Load(p, MapEnv(nil), nil, nil)
	if l == nil || l.FileExisted {
		t.Fatalf("loaded %+v err %v", l, err)
	}
	if _, ok := AsValidation(err); !ok {
		t.Errorf("defaults alone should fail validation (no name, no terms), got %v", err)
	}
}

func TestUnknownYAMLKeysAreErrorsAndSecretsPointToDotenv(t *testing.T) {
	p := tmpPaths(t)
	os.MkdirAll(filepath.Dir(p.ConfigFile), 0o755)
	os.WriteFile(p.ConfigFile, []byte("search:\n  trems: [a]\n"), 0o600)
	_, err := Load(p, MapEnv(nil), nil, nil)
	if err == nil || !strings.Contains(err.Error(), `unknown key "trems"`) || !strings.Contains(err.Error(), "search.terms") {
		t.Errorf("typo: %v", err)
	}
	os.WriteFile(p.ConfigFile, []byte("ai:\n  api_key: sk-123\n"), 0o600)
	_, err = Load(p, MapEnv(nil), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "looks like a secret") || !strings.Contains(err.Error(), EnvAIAPIKey) {
		t.Errorf("secret in yaml: %v", err)
	}
}

func TestLayersEnvironmentBeatsDotenvAndFlagsBeatEverything(t *testing.T) {
	p := tmpPaths(t)
	Write(p.ConfigFile, valid())
	os.WriteFile(p.EnvFile, []byte("AJA_AI_API_KEY=from-file\nAJA_TELEGRAM_CHAT_ID=42\nAJA_CFG_SEARCH_LOCATION=Lisboa\nAJA_CFG_MARKETS_BRASIL_TERMS=Dev,QA\n"), 0o600)
	env := MapEnv(map[string]string{"AJA_AI_API_KEY": "from-env", "AJA_CFG_SEARCH_SWITCH_NUMBER": "5"})
	environ := func() []string { return []string{"AJA_CFG_SEARCH_SWITCH_NUMBER=5", "PATH=/bin"} }
	l, err := Load(p, env, environ, map[string]string{"markets.active": "brasil"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Secrets.AIAPIKey != "from-env" || l.Secrets.TelegramChatID != "42" || l.Secrets.TelegramBotToken != "" {
		t.Errorf("secrets %+v", l.Secrets)
	}
	c := l.Config
	if c.Search.Location != "Lisboa" || c.Search.SwitchNumber != 5 || c.Markets.Active != "brasil" || len(c.Markets.Brasil.Terms) != 2 {
		t.Errorf("overrides not applied: %+v %+v", c.Search, c.Markets)
	}
	if strings.Join(l.Overrides, " ") != "markets.active markets.brasil.terms search.location search.switch_number" {
		t.Errorf("overrides list %v", l.Overrides)
	}
}

func TestSecretsAreNotKeys(t *testing.T) {
	for _, f := range Fields() {
		if looksLikeSecret(f.Key) {
			t.Errorf("%s would be mistaken for a secret", f.Key)
		}
	}
}
