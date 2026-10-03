package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/ajai/internal/paths"
)

func tmpPaths(t *testing.T) paths.Paths {
	t.Helper()
	return paths.Resolve("", func(k string) string {
		if k == "AJAI_HOME" {
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
	cases := []struct{ key, in, wantErr string }{
		{"search.switch_number", "many", "not a whole number"},
		{"search.easy_apply_only", "yes please", "not a boolean"},
		{"search.term", "x", `unknown key "search.term"; did you mean`},
		{"search.trems", "x", "search.terms"},
	}
	for _, tc := range cases {
		c := valid()
		err := c.Set(tc.key, tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("set %s=%q: got %v, want %q", tc.key, tc.in, err, tc.wantErr)
		}
	}
	var uk *UnknownKeyError
	c := valid()
	if err := c.Set("search.term", "x"); !errors.As(err, &uk) || !contains(uk.Suggestions, "search.terms") {
		t.Errorf("unknown key type: %v", err)
	}
}

func TestSetCheckedSeparatesTheNewValueFromOlderProblems(t *testing.T) {
	c := valid()
	c.Search.SortBy = "Newest" // pre-existing problem
	other, err := c.SetChecked("search.switch_number", "3")
	if err != nil || other == nil || other.Errors[0].Key != "search.sort_by" {
		t.Errorf("other=%v err=%v", other, err)
	}
	var fe *FieldError
	if _, err := c.SetChecked("markets.active", "europa"); !errors.As(err, &fe) || fe.Key != "markets.active" {
		t.Errorf("new bad value should be a FieldError for its key: %v", err)
	}
}

func TestEssentialsGoToTheMarketForBrazilAndToTheGeneralSettingsOtherwise(t *testing.T) {
	cases := []struct {
		market                string
		wantBRSalary, wantGen int
		wantBRLoc, wantGenLoc string
	}{
		{"brasil", 9000, 0, "Rio", ""},
		{"internacional", 0, 9000, "Brasil", "Rio"},
	}
	for _, tc := range cases {
		c := Defaults()
		err := c.ApplyEssentials(Essentials{FirstName: "A", LastName: "B", Terms: "x", Market: tc.market,
			Location: "Rio", Resume: "r.pdf", DesiredSalary: 9000, MaxPerRun: 7})
		if err != nil || c.Validate() != nil {
			t.Fatalf("%s: %v %v", tc.market, err, c.Validate())
		}
		if c.Markets.Brasil.DesiredSalary != tc.wantBRSalary || c.Questions.DesiredSalary != tc.wantGen ||
			c.Markets.Brasil.Location != tc.wantBRLoc || c.Search.Location != tc.wantGenLoc || c.Pacing.MaxPerRun != 7 || c.Pacing.MaxPerHour != 15 {
			t.Errorf("%s: %+v %+v %+v", tc.market, c.Markets.Brasil, c.Questions.DesiredSalary, c.Search.Location)
		}
	}
}

func TestMarketNamesMatchTheOptionsTag(t *testing.T) {
	for _, f := range Fields() {
		if f.Key == "markets.active" {
			want := make([]string, len(MarketNames))
			for i, m := range MarketNames {
				want[i] = string(m)
			}
			if strings.Join(f.Options, "|") != strings.Join(want, "|") {
				t.Errorf("opts %v, MarketNames %v", f.Options, want)
			}
		}
	}
}

// ------------------------------------------------------------ write / load round trip
func TestWrittenFileIsCommentedAndRoundTrips(t *testing.T) {
	p := tmpPaths(t)
	c := valid()
	c.Search.Terms = []string{"Dev Python", "Engenheiro de Software"}
	if err := Write(p.ConfigFile, c); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# ajai configuration.",
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
	l, err := Load(p, MapEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !l.FileExisted || l.Config.Search.Terms[1] != "Engenheiro de Software" || l.Config.Pacing.MaxPerHour != 15 {
		t.Errorf("round trip lost data: %+v", l.Config.Search)
	}
}

func TestLoadWithoutAFileUsesDefaultsAndReportsIt(t *testing.T) {
	p := tmpPaths(t)
	l, err := Load(p, MapEnv(nil), nil)
	if l == nil || l.FileExisted {
		t.Fatalf("loaded %+v err %v", l, err)
	}
	if _, ok := AsValidation(err); !ok {
		t.Errorf("defaults alone should fail validation (no name, no terms), got %v", err)
	}
}

func TestUnknownYAMLKeysAreErrorsAndSecretsPointToDotenv(t *testing.T) {
	p := tmpPaths(t)
	mustWrite(t, p.ConfigFile, "search:\n  trems: [a]\n")
	_, err := Load(p, MapEnv(nil), nil)
	if err == nil || !strings.Contains(err.Error(), `unknown key "trems"`) || !strings.Contains(err.Error(), "search.terms") {
		t.Errorf("typo: %v", err)
	}
	mustWrite(t, p.ConfigFile, "ai:\n  api_key: sk-123\n")
	_, err = Load(p, MapEnv(nil), nil)
	if err == nil || !strings.Contains(err.Error(), "looks like a secret") || !strings.Contains(err.Error(), EnvAIAPIKey) {
		t.Errorf("secret in yaml: %v", err)
	}
}

func TestLayersEnvironmentBeatsDotenvAndFlagsBeatEverything(t *testing.T) {
	p := tmpPaths(t)
	if err := Write(p.ConfigFile, valid()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, p.EnvFile, "AJAI_AI_API_KEY=from-file\nAJAI_TELEGRAM_CHAT_ID=42\nAJAI_CFG_SEARCH_LOCATION=Lisboa\nAJAI_CFG_MARKETS_BRASIL_TERMS=Dev,QA\n")
	env := MapEnv(map[string]string{"AJAI_AI_API_KEY": "from-env", "AJAI_CFG_SEARCH_SWITCH_NUMBER": "5", "PATH": "/bin"})
	l, err := Load(p, env, map[string]string{"markets.active": "brasil"})
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

func TestTheOldAJAVariablesAreNotRead(t *testing.T) {
	p := tmpPaths(t)
	if err := Write(p.ConfigFile, valid()); err != nil {
		t.Fatal(err)
	}
	old := "AJA_AI_API_KEY=k\nAJA_TELEGRAM_BOT_TOKEN=t\nAJA_TELEGRAM_CHAT_ID=42\nAJA_CFG_SEARCH_LOCATION=Lisboa\n" // as aja named them
	mustWrite(t, p.EnvFile, old)
	l, err := Load(p, MapEnv(map[string]string{"AJA_AI_API_KEY": "k", "AJA_CFG_SEARCH_SWITCH_NUMBER": "5"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.Secrets != (Secrets{}) || len(l.Overrides) > 0 || l.Config.Search.Location == "Lisboa" || l.Config.Search.SwitchNumber == 5 {
		t.Errorf("an AJA_ variable was read: secrets set %v, overrides %v", l.Secrets != (Secrets{}), l.Overrides)
	}
}

func TestLoadFileIsTheFileAloneWithNoOverrideFromDotenv(t *testing.T) {
	p := tmpPaths(t)
	if err := Write(p.ConfigFile, valid()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, p.EnvFile, "AJAI_AI_API_KEY=from-file\nAJAI_CFG_SEARCH_LOCATION=Lisboa\n")
	l, err := LoadFile(p)
	if err != nil || !l.FileExisted {
		t.Fatal(l, err)
	}
	if l.Config.Search.Location == "Lisboa" || len(l.Overrides) != 0 || l.Secrets.AIAPIKey != "" {
		t.Errorf("an override or a secret from .env reached the file's configuration: %q %v %+v", l.Config.Search.Location, l.Overrides, l.Secrets)
	}
}

func TestABrokenDotenvIsReportedWithoutItsContent(t *testing.T) {
	p := tmpPaths(t)
	mustWrite(t, p.EnvFile, "AJAI_AI_API_KEY='sk-SECRET\nAJAI_TELEGRAM_BOT_TOKEN=123:ALSO\n")
	_, err := Load(p, MapEnv(nil), nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "ALSO") || !strings.Contains(err.Error(), "never shows its content") {
		t.Errorf("%v", err)
	}
}

func TestSecretsAreNotKeys(t *testing.T) {
	for _, f := range Fields() {
		if looksLikeSecret(f.Key) {
			t.Errorf("%s would be mistaken for a secret", f.Key)
		}
	}
}

func mustWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestARetiredKeyStillLoadsWithAWarningAndSetDropsIt(t *testing.T) {
	p := tmpPaths(t)
	if err := Write(p.ConfigFile, valid()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p.ConfigFile)
	old := strings.Replace(string(data), "settings:\n", "settings:\n  click_gap: 2\n", 1)
	mustWrite(t, p.ConfigFile, old)
	mustWrite(t, p.EnvFile, "AJAI_CFG_SETTINGS_CLICK_GAP=3\n")
	l, err := Load(p, MapEnv(nil), nil)
	if err != nil {
		t.Fatalf("an old file must keep loading: %v", err)
	}
	if len(l.Warnings) != 2 || !strings.Contains(l.Warnings[0], "settings.click_gap is no longer used") ||
		!strings.Contains(l.Warnings[0], "pacing.between_actions") || !strings.Contains(l.Warnings[1], "environment override") {
		t.Errorf("%q", l.Warnings)
	}
	if l.Config.Search.Terms[0] != "Software Engineer" {
		t.Error("the rest of the file must load as before")
	}
	// The retired key must not shift the line numbers of real errors after it,
	// and the same name in another section is still an unknown key.
	bad := strings.Replace(old, "  log_level: INFO\n", "  log_level: [not, a, string]\n", 1)
	if bad == old {
		t.Fatal("test setup: log_level not found")
	}
	mustWrite(t, p.ConfigFile, bad)
	line := 1 + strings.Count(bad[:strings.Index(bad, "  log_level:")], "\n")
	if _, err := Load(p, MapEnv(nil), nil); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("line %d:", line)) {
		t.Errorf("want the error at line %d: %v", line, err)
	}
	mustWrite(t, p.ConfigFile, strings.Replace(old, "search:\n", "search:\n  click_gap: 2\n", 1))
	var unknown *UnknownKeyError
	if _, err := Load(p, MapEnv(nil), nil); !errors.As(err, &unknown) {
		t.Errorf("search.click_gap was never a setting: %v", err)
	}
	mustWrite(t, p.ConfigFile, old)
	// Rewriting the file (what config set does) drops the retired key.
	if err := Write(p.ConfigFile, l.Config); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(p.ConfigFile)
	if strings.Contains(string(data), "click_gap") {
		t.Error("the rewritten file still has the retired key")
	}
}
