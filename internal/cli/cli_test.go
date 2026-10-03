package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/config"
)

// harness runs ajai with AJAI_HOME pointing at a temp dir and a fake environment.
type harness struct {
	home  string
	env   map[string]string
	stdin string    // typed into the next command
	now   time.Time // the clock commands see; zero: the real one
	t     *testing.T
	mu    sync.Mutex // a long-running command (ajai daemon) may run beside others
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	saved := systemctl // no test reaches this machine's systemd: fakeSystemd replaces it where one is needed
	systemctl = func(...string) (string, error) { return "", errors.New("no systemctl in tests") }
	t.Cleanup(func() { systemctl = saved })
	return &harness{home: t.TempDir(), env: map[string]string{}, t: t}
}

// run executes one command and returns exit code, stdout and stderr.
func (h *harness) run(args ...string) (int, string, string) {
	h.t.Helper()
	return h.runCtx(context.Background(), args...)
}

// runCtx is run with a context the test cancels (Ctrl-C), for long-running commands.
func (h *harness) runCtx(ctx context.Context, args ...string) (int, string, string) {
	h.t.Helper()
	h.mu.Lock()
	env := map[string]string{"AJAI_HOME": h.home, "NO_COLOR": "1"}
	for k, v := range h.env {
		env[k] = v
	}
	var out, errb bytes.Buffer
	app := &App{Version: "test", Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(h.stdin), Env: config.MapEnv(env)}
	if now := h.now; !now.IsZero() {
		app.Now = func() time.Time { return now }
	}
	h.stdin = ""
	h.mu.Unlock()
	code := app.Execute(ctx, args)
	return code, out.String(), errb.String()
}

func (h *harness) write(name, text string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.home, name), []byte(text), 0o600); err != nil {
		h.t.Fatal(err)
	}
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
	if !strings.HasPrefix(strings.TrimSpace(out), "ajai test "+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("got %q", out)
	}
}

func TestResolveVersionPrefersTheReleaseTagThenTheModuleVersion(t *testing.T) {
	withModule := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	cases := []struct {
		ldflag string
		info   *debug.BuildInfo
		want   string
	}{
		{"v0.1.0", withModule("v9.9.9"), "v0.1.0"},                                                      // a goreleaser build
		{"dev", withModule("v0.2.0"), "v0.2.0"},                                                         // go install ...@v0.2.0
		{"dev", withModule("v0.0.0-20261002025052-8017aed917c5"), "v0.0.0-20261002025052-8017aed917c5"}, // go build in a git checkout
		{"dev", withModule("(devel)"), "dev"},                                                           // go build -buildvcs=false, or outside a git checkout
		{"", nil, "dev"},
	}
	for _, tc := range cases {
		if got := ResolveVersion(tc.ldflag, tc.info); got != tc.want {
			t.Errorf("ResolveVersion(%q, %v) = %q, want %q", tc.ldflag, tc.info, got, tc.want)
		}
	}
}

func TestInitWritesACommentedValidFileUnderAJAIHome(t *testing.T) {
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
	if !strings.Contains(string(text), "# ajai configuration.") {
		t.Error("comments lost after set")
	}
	// Negative numbers are values, not flags.
	code, _, errText = h.run("config", "set", "pacing.max_per_run", "-3")
	if code == 0 || !strings.Contains(errText, "pacing.max_per_run: must be at least 1") {
		t.Errorf("negative value: %d %s", code, errText)
	}
}

func TestCommandsThatWriteLeaveAStructuredLogLine(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.run("config", "set", "search.switch_number", "4")
	entries, err := os.ReadDir(filepath.Join(h.home, "logs"))
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "ajai-") {
		t.Fatalf("log dir: %v %v", entries, err)
	}
	text, _ := os.ReadFile(filepath.Join(h.home, "logs", entries[0].Name()))
	if !strings.Contains(string(text), `"msg":"config created"`) || !strings.Contains(string(text), `"msg":"config set"`) || !strings.Contains(string(text), `"key":"search.switch_number"`) {
		t.Errorf("log:\n%s", text)
	}
}

func TestInitPacingFlagsReachTheConfig(t *testing.T) {
	h := newHarness(t)
	code, _, errText := h.run("init", "--first-name", "A", "--last-name", "B", "--terms", "x", "--max-per-hour", "5", "--max-per-run", "20")
	if code != 0 {
		t.Fatal(errText)
	}
	if _, out, _ := h.run("config", "get", "pacing.max_applications_per_hour"); strings.TrimSpace(out) != "5" {
		t.Errorf("max per hour %q", out)
	}
	if code, _, errText := h.run("--config", filepath.Join(h.home, "other.yaml"), "init", "--first-name", "A", "--last-name", "B", "--terms", "x", "--max-per-run", "zero"); code == 0 || !strings.Contains(errText, "--max-per-run") {
		t.Errorf("bad pacing flag: %d %s", code, errText)
	}
}

func TestSecretsComeOnlyFromEnvAndAreMaskedInShow(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.write(".env", "AJAI_AI_API_KEY=sk-secret-value\n")
	_, out, _ := h.run("config", "show")
	if strings.Contains(out, "sk-secret-value") {
		t.Error("secret leaked in show")
	}
	if !strings.Contains(out, "AJAI_AI_API_KEY") || !strings.Contains(out, config.Mask+" (set)") || !strings.Contains(out, "AJAI_TELEGRAM_BOT_TOKEN") || !strings.Contains(out, "not set") {
		t.Errorf("show:\n%s", out)
	}
	// A secret in the YAML is refused.
	h.write("config.yaml", "ai:\n  api_key: sk-123\n")
	code, _, errText := h.run("config", "validate")
	if code == 0 || !strings.Contains(errText, "looks like a secret") {
		t.Errorf("secret in yaml: %d %s", code, errText)
	}
}

func TestConfigSetNeverWritesAnOverrideFromDotenv(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.write(".env", "AJAI_CFG_SEARCH_LOCATION=Lisboa\n")
	if code, _, errText := h.run("config", "set", "search.terms", "Go"); code != 0 {
		t.Fatal(errText)
	}
	data, err := os.ReadFile(filepath.Join(h.home, "config.yaml"))
	if text := string(data); err != nil || strings.Contains(text, "Lisboa") || !strings.Contains(text, "- Go") {
		t.Errorf("the file got the .env override, or not the edit: %v\n%s", err, text)
	}
}

func TestConfigValidateListsEveryProblemWithExitCodeOne(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.write("config.yaml", "personals:\n  first_name: ''\nsearch:\n  terms: []\n  sort_by: Newest\n")
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
	h.env["AJAI_CFG_SEARCH_LOCATION"] = "Lisboa"
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

func TestDoctorFailsWithoutAConfigAndListsPathsUnderAJAIHome(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("doctor")
	if code != 1 || !strings.Contains(out, "FAIL  config") || !strings.Contains(out, "run `ajai init`") {
		t.Errorf("doctor without config: %d\n%s", code, out)
	}
	for _, label := range []string{"config", "database", "logs", "screenshots", "resumes"} {
		if !strings.Contains(out, label) {
			t.Errorf("paths lack %s", label)
		}
	}
	if strings.Count(out, h.home) < 6 {
		t.Errorf("paths should all be under AJAI_HOME:\n%s", out)
	}
	for _, name := range config.SecretEnvNames {
		if !strings.Contains(out, name) {
			t.Errorf("doctor should name %s", name)
		}
	}
}

func TestConfigPathHonoursTheFlag(t *testing.T) {
	h := newHarness(t)
	_, out, _ := h.run("--config", "/etc/ajai/x.yaml", "config", "path")
	if strings.TrimSpace(out) != "/etc/ajai/x.yaml" {
		t.Errorf("got %q", out)
	}
}
