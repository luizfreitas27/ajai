package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/config"
)

// run executes aja with AJA_HOME pointing at a temp dir and returns exit code,
// stdout and stderr. extraEnv is merged into the fake environment.
type harness struct {
	home string
	env  map[string]string
	t    *testing.T
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{home: t.TempDir(), env: map[string]string{}, t: t}
}

func (h *harness) run(args ...string) (int, string, string) {
	h.t.Helper()
	env := map[string]string{"AJA_HOME": h.home, "NO_COLOR": "1"}
	for k, v := range h.env {
		env[k] = v
	}
	var out, errb bytes.Buffer
	app := &App{Version: "test", Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""),
		Getenv: func(k string) string { return env[k] },
		Env:    config.MapEnv(env),
		Environ: func() []string {
			var kv []string
			for k, v := range env {
				kv = append(kv, k+"="+v)
			}
			return kv
		},
	}
	code := app.Execute(context.Background(), args)
	return code, out.String(), errb.String()
}

func (h *harness) initValid() {
	h.t.Helper()
	code, _, errText := h.run("init", "--first-name", "Ana", "--last-name", "Silva", "--terms", "Dev Python, QA", "--location", "Brasil", "--market", "brasil", "--salary", "9000")
	if code != 0 {
		h.t.Fatalf("init failed: %s", errText)
	}
}

func TestVersion(t *testing.T) {
	_, out, _ := newHarness(t).run("version")
	if strings.TrimSpace(out) != "aja test" {
		t.Errorf("got %q", out)
	}
}

func TestInitWritesACommentedValidFileUnderAJAHome(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	text, err := os.ReadFile(filepath.Join(h.home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first_name: Ana", "- Dev Python", "active: brasil", "desired_salary: 9000", "location: Brasil", "# Which market this run applies in."} {
		if !strings.Contains(string(text), want) {
			t.Errorf("config lacks %q", want)
		}
	}
	if code, out, _ := h.run("config", "validate"); code != 0 || !strings.Contains(out, "is valid") {
		t.Errorf("validate: %d %s", code, out)
	}
	for _, dir := range []string{"logs", "screenshots", "resumes"} {
		if _, err := os.Stat(filepath.Join(h.home, dir)); err != nil {
			t.Errorf("data dir %s missing", dir)
		}
	}
}

func TestInitRefusesToOverwriteWithoutForceAndNeedsATerminalOtherwise(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	code, _, errText := h.run("init", "--first-name", "B", "--last-name", "C", "--terms", "x")
	if code == 0 || !strings.Contains(errText, "already exists") {
		t.Errorf("overwrite: %d %s", code, errText)
	}
	if code, _, _ := h.run("init", "--force", "--first-name", "B", "--last-name", "C", "--terms", "x"); code != 0 {
		t.Error("--force should overwrite")
	}
	h2 := newHarness(t)
	code, _, errText = h2.run("init")
	if code == 0 || !strings.Contains(errText, "no terminal") {
		t.Errorf("no tty: %d %s", code, errText)
	}
}

func TestConfigSetGetAndRejection(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	if code, out, _ := h.run("config", "set", "search.terms", "a,b"); code != 0 || !strings.Contains(out, "a, b") {
		t.Errorf("set: %d %s", code, out)
	}
	if _, out, _ := h.run("config", "get", "search.terms"); strings.TrimSpace(out) != "a, b" {
		t.Errorf("get: %q", out)
	}
	code, _, errText := h.run("config", "set", "search.sort_by", "Newest")
	if code == 0 || !strings.Contains(errText, `search.sort_by: "Newest" is not a valid value`) || !strings.Contains(errText, `"Most recent"`) {
		t.Errorf("rejection: %d %s", code, errText)
	}
	if _, out, _ := h.run("config", "get", "search.sort_by"); strings.TrimSpace(out) != "" {
		t.Errorf("rejected value was written: %q", out)
	}
	code, _, errText = h.run("config", "set", "search.swich_number", "3")
	if code == 0 || !strings.Contains(errText, "search.switch_number") {
		t.Errorf("suggestion: %s", errText)
	}
	// Comments survive a set.
	text, _ := os.ReadFile(filepath.Join(h.home, "config.yaml"))
	if !strings.Contains(string(text), "# aja configuration.") {
		t.Error("comments lost after set")
	}
}

func TestSecretsComeOnlyFromEnvAndAreMaskedInShow(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	os.WriteFile(filepath.Join(h.home, ".env"), []byte("AJA_AI_API_KEY=sk-secret-value\n"), 0o600)
	_, out, _ := h.run("config", "show")
	if strings.Contains(out, "sk-secret-value") {
		t.Error("secret leaked in show")
	}
	if !strings.Contains(out, "AJA_AI_API_KEY") || !strings.Contains(out, config.Mask+" (set)") || !strings.Contains(out, "AJA_TELEGRAM_BOT_TOKEN") || !strings.Contains(out, "not set") {
		t.Errorf("show:\n%s", out)
	}
	// A secret in the YAML is refused.
	os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("ai:\n  api_key: sk-123\n"), 0o600)
	code, _, errText := h.run("config", "validate")
	if code == 0 || !strings.Contains(errText, "looks like a secret") {
		t.Errorf("secret in yaml: %d %s", code, errText)
	}
}

func TestConfigValidateListsEveryProblemWithExitCodeOne(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("personals:\n  first_name: ''\nsearch:\n  terms: []\n  sort_by: Newest\n"), 0o600)
	code, out, _ := h.run("config", "validate")
	if code != 1 {
		t.Errorf("exit %d", code)
	}
	for _, want := range []string{"personals.first_name", "search.terms", "search.sort_by"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

func TestEnvironmentOverridesApplyAtRuntimeButSetWritesTheFile(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.env["AJA_CFG_SEARCH_LOCATION"] = "Lisboa"
	if _, out, _ := h.run("config", "get", "search.location"); strings.TrimSpace(out) != "Lisboa" {
		t.Errorf("override not applied: %q", out)
	}
	h.run("config", "set", "search.switch_number", "9")
	text, _ := os.ReadFile(filepath.Join(h.home, "config.yaml"))
	if strings.Contains(string(text), "Lisboa") {
		t.Error("an environment override was persisted by set")
	}
	_, out, _ := h.run("config", "show")
	if !strings.Contains(out, "Overridden by the environment") || !strings.Contains(out, "search.location") {
		t.Errorf("show should list overrides:\n%s", out)
	}
}

func TestDoctorFailsWithoutAConfigAndListsPathsUnderAJAHome(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("doctor")
	if code != 1 || !strings.Contains(out, "FAIL  config") || !strings.Contains(out, "run `aja init`") {
		t.Errorf("doctor without config: %d\n%s", code, out)
	}
	for _, label := range []string{"config", "database", "logs", "screenshots", "resumes"} {
		if !strings.Contains(out, label) {
			t.Errorf("paths lack %s", label)
		}
	}
	if strings.Count(out, h.home) < 6 {
		t.Errorf("paths should all be under AJA_HOME:\n%s", out)
	}
	for _, name := range config.SecretEnvNames {
		if !strings.Contains(out, name) {
			t.Errorf("doctor should name %s", name)
		}
	}
}

func TestConfigPathHonoursTheFlag(t *testing.T) {
	h := newHarness(t)
	_, out, _ := h.run("--config", "/etc/aja/x.yaml", "config", "path")
	if strings.TrimSpace(out) != "/etc/aja/x.yaml" {
		t.Errorf("got %q", out)
	}
}
