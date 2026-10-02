package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pythonCheckout writes the smallest Python checkout the importer understands.
func pythonCheckout(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	files := map[string]string{
		"user_config.json": `{"personals": {"first_name": "Ana", "last_name": "Silva"},
			"search": {"search_terms": ["Dev Python"]},
			"secrets": {"llm_api_key": "sk-NEVER", "telegram_bot_token": "tok-NEVER", "username": "u", "password": "p"}}`,
		"answers_memory.json": `{"version": 1, "answers": [{"label": "Consent?", "kind": "checkbox", "answer": "checked",
			"source": "form", "state": "pending", "id": "a1", "uses": 0, "last_job_link": "", "created_at": "2026-10-01T00:00:00", "updated_at": "2026-10-01T00:00:00"}]}`,
		"all excels/all_applied_applications_history.csv": "Job ID,Title,Company,Work Location,Work Style,About Job,Experience required,Skills required,HR Name,HR Link,Resume,Re-posted,Date Posted,Date Applied,Job Link,External Job link,Questions Found,Connect Request\n" +
			"111,Dev,ACME,Rio,Remote,d,3,,U,U,resume.pdf,False,2026-09-30,2026-10-01 10:00:00,https://l/111,,,In Development\n",
		"all excels/all_failed_applications_history.csv": "Job ID,Job Link,Resume Tried,Date listed,Date Tried,Assumed Reason,Stack Trace,External Job link,Screenshot Name\n" +
			"333,https://l/333,resume.pdf,2026-09-30,2026-10-01 11:00:00,modal closed,tb,,333.png\n",
		"all resumes/default/resume.pdf": "%PDF",
	}
	for rel, text := range files {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func TestImportWritesConfigDatabaseAndResumeWithoutSecrets(t *testing.T) {
	h := newHarness(t)
	src := pythonCheckout(t)
	code, out, errText := h.run("import", src)
	if code != 0 {
		t.Fatalf("%s", errText)
	}
	for _, want := range []string{"settings mapped", "1 remembered answers", "1 applied, 0 external, 1 failed", "resume", "AJA_AI_API_KEY=...", "AJA_TELEGRAM_BOT_TOKEN=...", "secrets.password"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	for _, never := range []string{"sk-NEVER", "tok-NEVER"} {
		if strings.Contains(out, never) {
			t.Errorf("secret %q printed", never)
		}
	}
	cfgText, _ := os.ReadFile(filepath.Join(h.home, "config.yaml"))
	if !strings.Contains(string(cfgText), "first_name: Ana") || strings.Contains(string(cfgText), "NEVER") {
		t.Errorf("config:\n%s", cfgText)
	}
	if _, err := os.Stat(filepath.Join(h.home, "resumes", "resume.pdf")); err != nil {
		t.Error("resume not copied")
	}
	if code, out, _ := h.run("config", "validate"); code != 0 {
		t.Errorf("imported config should validate: %s", out)
	}
	if code, out, _ := h.run("doctor"); code != 0 && !strings.Contains(out, "FAIL  chrome") {
		t.Errorf("doctor after import should pass except for a machine without Chrome:\n%s", out)
	}
	// Second import: same counts, nothing duplicated.
	_, out, _ = h.run("import", src)
	if !strings.Contains(out, "1 remembered answers") || !strings.Contains(out, "1 applied, 0 external, 1 failed") {
		t.Errorf("second import:\n%s", out)
	}
}

func TestJobsListFiltersAndJSON(t *testing.T) {
	h := newHarness(t)
	h.run("import", pythonCheckout(t))
	_, out, _ := h.run("jobs", "list")
	if !strings.Contains(out, "applied") || !strings.Contains(out, "111") || !strings.Contains(out, "333") {
		t.Errorf("list:\n%s", out)
	}
	_, out, _ = h.run("jobs", "list", "--failed")
	if strings.Contains(out, "111") || !strings.Contains(out, "modal closed") {
		t.Errorf("failed only:\n%s", out)
	}
	_, out, _ = h.run("jobs", "list", "--json", "--since", "2000-01-01")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 2 {
		t.Errorf("json: %v %s", err, out)
	}
	if code, _, errText := h.run("jobs", "list", "--since", "yesterday"); code == 0 || !strings.Contains(errText, "YYYY-MM-DD") {
		t.Errorf("bad since: %d %s", code, errText)
	}
	h2 := newHarness(t)
	if _, out, _ := h2.run("jobs", "list"); !strings.Contains(out, "No jobs recorded yet") {
		t.Errorf("empty: %s", out)
	}
}

func TestExportWritesThePythonSpreadsheets(t *testing.T) {
	h := newHarness(t)
	h.run("import", pythonCheckout(t))
	dir := filepath.Join(h.home, "out")
	code, out, errText := h.run("export", "--csv", dir)
	if code != 0 {
		t.Fatal(errText)
	}
	if !strings.Contains(out, "1 rows (applied and external)") || !strings.Contains(out, "1 rows") {
		t.Errorf("%s", out)
	}
	text, err := os.ReadFile(filepath.Join(dir, "all_applied_applications_history.csv"))
	if err != nil || !strings.HasPrefix(string(text), "Job ID,Title,Company,") || !strings.Contains(string(text), "111,Dev,ACME") {
		t.Errorf("%v\n%s", err, text)
	}
	if code, _, errText := h.run("export"); code == 0 || !strings.Contains(errText, "--csv") {
		t.Errorf("missing dir: %d %s", code, errText)
	}
}
