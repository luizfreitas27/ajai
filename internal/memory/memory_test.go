package memory

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/store"
)

func fresh(t *testing.T) (*Memory, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "aja.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var log bytes.Buffer
	m := New(st, slog.New(slog.NewTextHandler(&log, nil)))
	tick := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { tick = tick.Add(time.Second); return tick }
	return m, &log
}

func rem(t *testing.T, m *Memory, label, kind, answer, source, jobLink string) *Remembered {
	t.Helper()
	entry, err := m.Remember(label, kind, answer, source, jobLink)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func approve(t *testing.T, m *Memory, id, answer string) *Remembered {
	t.Helper()
	entry, err := m.Approve(id, answer)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func use(t *testing.T, m *Memory, id, jobLink string) {
	t.Helper()
	if err := m.RecordUse(id, jobLink); err != nil {
		t.Fatal(err)
	}
}

func TestAnAIAnswerIsRememberedAsPendingAndFoundExactly(t *testing.T) {
	m, _ := fresh(t)
	entry := rem(t, m, "Years of experience with Python?", "text", "4", "ai", "https://www.linkedin.com/jobs/view/1")
	if entry.State != "pending" || entry.Source != "ai" || entry.Uses != 0 || !strings.HasSuffix(entry.LastJobLink, "/1") || len(entry.ID) != 12 {
		t.Errorf("%+v", entry)
	}
	found, approximate, _ := m.Lookup("Years of experience with Python?", "text", true)
	if found == nil || found.ID != entry.ID || approximate {
		t.Errorf("%+v %v", found, approximate)
	}
	found, approximate, _ = m.Lookup("  years OF experience, with python ", "text", true)
	if found == nil || approximate {
		t.Error("case, punctuation and spacing must not matter")
	}
}

func TestAUserAnswerIsApproved(t *testing.T) {
	m, _ := fresh(t)
	entry := rem(t, m, "Preferred pronouns", "text", "they/them", "user", "")
	if entry.State != "approved" || entry.Source != "user" {
		t.Errorf("%+v", entry)
	}
}

func TestARewordedQuestionMatchesApproximatelyAndIsLogged(t *testing.T) {
	m, log := fresh(t)
	rem(t, m, "How many years of experience do you have with Python?", "text", "4", "ai", "")
	found, approximate, _ := m.Lookup("How many years experience do you have with Python?", "text", true)
	if found == nil || !approximate || !strings.Contains(log.String(), "approximate") {
		t.Errorf("%+v %v %s", found, approximate, log.String())
	}
	if f, _, _ := m.Lookup("How many years experience do you have with Python?", "text", false); f != nil {
		t.Error("approximate matching can be turned off")
	}
	if f, _, _ := m.Lookup("How many years of experience do you have with Kubernetes?", "text", true); f != nil {
		t.Error("a different skill is below the threshold")
	}
}

func TestMatchingNeverCrossesKindsAndEmptyLabelsNeverMatch(t *testing.T) {
	m, _ := fresh(t)
	rem(t, m, "Are you willing to relocate?", "select", "Yes", "ai", "")
	for _, kind := range []string{"text", "radio"} {
		if f, _, _ := m.Lookup("Are you willing to relocate?", kind, true); f != nil {
			t.Errorf("crossed into %s", kind)
		}
	}
	rem(t, m, "x", "text", "1", "ai", "")
	for _, label := range []string{"", "   ?!"} {
		if f, _, _ := m.Lookup(label, "text", true); f != nil {
			t.Errorf("%q matched", label)
		}
	}
}

func TestRememberingAgainUpdatesInsteadOfDuplicatingAndTheUsersWordStands(t *testing.T) {
	m, _ := fresh(t)
	first := rem(t, m, "Desired start date", "text", "ASAP", "ai", "")
	second := rem(t, m, "Desired start date?", "text", "In two weeks", "user", "")
	if second.ID != first.ID || second.Answer != "In two weeks" || second.State != "approved" || second.Source != "user" {
		t.Errorf("%+v", second)
	}
	again := rem(t, m, "Desired start date", "text", "ASAP", "ai", "")
	if again.Answer != "In two weeks" || again.State != "approved" || again.Source != "user" {
		t.Errorf("an AI answer overrode the user's: %+v", again)
	}
	all, _ := m.List("")
	if len(all) != 1 {
		t.Errorf("%d entries", len(all))
	}
}

func TestALabelThatNormalisesToNothingIsNeverRemembered(t *testing.T) {
	m, _ := fresh(t)
	for _, label := range []string{"", "   ?!", "职位"} {
		if _, err := m.Remember(label, "text", "x", "ai", ""); err == nil {
			t.Errorf("%q was remembered; two such labels would collide", label)
		}
	}
}

func TestOnAnEqualRatioTheOldestEntryWins(t *testing.T) {
	m, _ := fresh(t)
	old := rem(t, m, "years with go aaa1", "text", "OLD", "ai", "")
	rem(t, m, "years with go aaa2", "text", "NEW", "ai", "")
	found, approximate, _ := m.Lookup("years with go aaa3", "text", true)
	if found == nil || !approximate || found.ID != old.ID {
		t.Errorf("%+v", found)
	}
}

func TestDeleteAllClearsOneState(t *testing.T) {
	m, _ := fresh(t)
	rem(t, m, "Q1", "text", "A1", "ai", "")
	rem(t, m, "Q2", "text", "A2", "ai", "")
	rem(t, m, "Q3", "text", "A3", "user", "")
	n, err := m.DeleteAll("pending")
	if err != nil || n != 2 {
		t.Errorf("%d %v", n, err)
	}
	if all, _ := m.List(""); len(all) != 1 || all[0].State != "approved" {
		t.Errorf("%+v", all)
	}
	if _, err := m.DeleteAll("maybe"); err == nil {
		t.Error("unknown state")
	}
}

func TestInvalidKindOrSourceIsRejected(t *testing.T) {
	m, _ := fresh(t)
	if _, err := m.Remember("q", "slider", "1", "ai", ""); err == nil {
		t.Error("kind")
	}
	if _, err := m.Remember("q", "text", "1", "oracle", ""); err == nil {
		t.Error("source")
	}
}

func TestRecordUseCountsAndTracksTheLastJob(t *testing.T) {
	m, _ := fresh(t)
	entry := rem(t, m, "Desired start date", "text", "ASAP", "ai", "")
	use(t, m, entry.ID, "https://www.linkedin.com/jobs/view/7")
	use(t, m, entry.ID, "https://www.linkedin.com/jobs/view/8")
	live, _ := m.Get(entry.ID)
	if live.Uses != 2 || !strings.HasSuffix(live.LastJobLink, "/8") {
		t.Errorf("%+v", live)
	}
	if err := m.RecordUse("deleted-meanwhile", ""); err != nil {
		t.Errorf("a vanished entry is nothing to count, not an error: %v", err)
	}
}

func TestPendingCountListApproveAndDelete(t *testing.T) {
	m, _ := fresh(t)
	a := rem(t, m, "Q1", "text", "A1", "ai", "")
	b := rem(t, m, "Q2", "textarea", "A2", "ai", "")
	c := rem(t, m, "Q3", "text", "A3", "user", "")
	if n, _ := m.PendingCount(); n != 2 {
		t.Errorf("pending %d", n)
	}
	pending, _ := m.List("pending")
	approved, _ := m.List("approved")
	if len(pending) != 2 || len(approved) != 1 || approved[0].ID != c.ID {
		t.Errorf("%v %v", pending, approved)
	}
	corrected := approve(t, m, a.ID, "A1 corrected")
	if corrected.State != "approved" || corrected.Answer != "A1 corrected" || corrected.Source != "user" {
		t.Errorf("%+v", corrected)
	}
	vetted := approve(t, m, b.ID, "")
	if vetted.State != "approved" || vetted.Source != "ai" { // approved as-is keeps the author
		t.Errorf("%+v", vetted)
	}
	if n, _ := m.PendingCount(); n != 0 {
		t.Errorf("pending %d", n)
	}
	if _, err := m.Approve("nope", ""); err != ErrUnknownID {
		t.Errorf("%v", err)
	}
	if err := m.Delete(b.ID); err != nil {
		t.Error(err)
	}
	if err := m.Delete(b.ID); err != ErrUnknownID {
		t.Errorf("second delete: %v", err)
	}
	if got, _ := m.Get(b.ID); got != nil {
		t.Error("still there")
	}
}

func TestAQueuedQuestionWithNoAnswerIsFoundButNeverAnswersOrShadows(t *testing.T) {
	m, _ := fresh(t)
	answered := rem(t, m, "How many years of experience do you have with Python?", "text", "4", "user", "")
	queued := rem(t, m, "How many years experience do you have with Python?", "text", "", "form", "")
	if got, _ := m.Find("How many years experience do you have with Python?", "text"); got == nil || got.ID != queued.ID {
		t.Errorf("Find must see the queued question: %+v", got)
	}
	found, approximate, _ := m.Lookup("How many years experience do you have with Python?", "text", true)
	if found == nil || found.ID != answered.ID || !approximate {
		t.Errorf("the queued entry shadowed the answered one: %+v", found)
	}
	if found, _, _ := m.Lookup("How many years experience do you have with Python?", "text", false); found != nil {
		t.Errorf("an exact lookup of a queued question answers nothing: %+v", found)
	}
	if _, err := m.Approve(queued.ID, ""); !errors.Is(err, ErrNothingToApprove) {
		t.Errorf("approving a question with no answer must be refused: %v", err)
	}
	if e, err := m.Approve(queued.ID, "  5 "); err != nil || e.Answer != "5" || e.State != "approved" || e.Source != "user" {
		t.Errorf("%+v %v", e, err)
	}
	// An AI answer for a queued question fills it in (still pending).
	q2 := rem(t, m, "Favourite colour?", "radio", "", "form", "")
	if e := rem(t, m, "Favourite colour?", "radio", "Blue", "ai", ""); e.ID != q2.ID || e.Answer != "Blue" || e.State != "pending" {
		t.Errorf("%+v", e)
	}
}
