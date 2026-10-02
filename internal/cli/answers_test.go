package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnswersListApproveEditDelete(t *testing.T) {
	h := newHarness(t)
	h.run("import", pythonCheckout(t)) // one pending checkbox answer, id a1
	_, out, _ := h.run("answers", "list")
	if !strings.Contains(out, "a1") || !strings.Contains(out, "pending") || !strings.Contains(out, "Consent?") || !strings.Contains(out, "1 pending") {
		t.Errorf("list:\n%s", out)
	}
	_, out, _ = h.run("answers", "list", "--state", "approved")
	if !strings.Contains(out, "No remembered answers in state approved") {
		t.Errorf("%s", out)
	}
	if code, _, errText := h.run("answers", "list", "--state", "maybe"); code == 0 || !strings.Contains(errText, "pending or approved") {
		t.Errorf("%d %s", code, errText)
	}

	if code, out, _ := h.run("answers", "approve", "a1"); code != 0 || !strings.Contains(out, "approved") {
		t.Errorf("%d %s", code, out)
	}
	_, out, _ = h.run("answers", "list", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0]["State"] != "approved" || rows[0]["Source"] != "form" {
		t.Errorf("approve as-is keeps the author: %v %s", err, out)
	}

	if code, _, _ := h.run("answers", "edit", "a1", "unchecked"); code != 0 {
		t.Error("edit")
	}
	_, out, _ = h.run("answers", "list", "--json")
	_ = json.Unmarshal([]byte(out), &rows)
	if rows[0]["Answer"] != "unchecked" || rows[0]["Source"] != "user" {
		t.Errorf("edit is the user's word: %s", out)
	}
	if code, _, errText := h.run("answers", "edit", "a1", "  "); code == 0 || !strings.Contains(errText, "blank") {
		t.Errorf("%d %s", code, errText)
	}

	if code, _, _ := h.run("answers", "delete", "a1"); code != 0 {
		t.Error("delete")
	}
	if code, _, errText := h.run("answers", "delete", "a1"); code == 0 || !strings.Contains(errText, "no remembered answer") {
		t.Errorf("%d %s", code, errText)
	}
	if _, out, _ := h.run("answers", "list"); !strings.Contains(out, "No remembered answers.") {
		t.Errorf("%s", out)
	}
}
