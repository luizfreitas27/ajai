package apply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/questions"
)

// MaxPages bounds the Next loop; the Python bot used the same ceiling.
const MaxPages = 15

// Options for one application.
type Options struct {
	DryRun          bool
	ResumePath      string // absolute path to upload, "" to keep LinkedIn's previous resume
	FollowCompanies bool
	Description     string // the job description, for the AI
}

// Kind classifies an Outcome.
type Kind string

// Outcome kinds.
const (
	Applied    Kind = "applied"    // submitted
	DryRun     Kind = "dry-run"    // reached Review with everything filled in, then discarded
	Unanswered Kind = "unanswered" // required questions stayed empty; discarded
	Stopped    Kind = "stopped"    // the user cancelled; discarded
	Failed     Kind = "failed"     // something broke; discarded, with Stage and Screenshot
)

// Outcome of one application attempt.
type Outcome struct {
	Kind       Kind
	Stage      string   // Failed: what was being done
	Screenshot string   // Failed: the screenshot file, "" when none was taken
	Blocked    []string // Unanswered: the questions left empty
	Answered   []string // "label: value" for every control the applier set, each once
	Resume     string   // the uploaded file's name, or "Previous resume"
	Captured   int      // answers the user typed during a help pause and aja remembered
	Queued     int      // unanswered questions put in the review queue for the user
}

// HelpRequest is what the person at the keyboard is asked to fill in.
type HelpRequest struct {
	Job        linkedin.Card
	Questions  []string // the stuck questions, with their options
	Screenshot string   // full path, "" when none was taken
}

// Helper is the person at the keyboard during an attended run (the help pause).
// Help returns nil when the user says the form is filled in; an error (the run
// was cancelled, nobody answered in time) means no help came.
type Helper interface {
	Help(ctx context.Context, req HelpRequest) error
}

// PreviousResume is Outcome.Resume when nothing was uploaded.
const PreviousResume = "Previous resume"

// Applier runs the modal for one job.
type Applier struct {
	Page          browser.Page
	Pacer         *pacing.Pacer
	Answerer      *questions.Answerer
	Logger        *slog.Logger
	ScreenshotDir string // "" disables screenshots
	Now           func() time.Time
	// Helper, when set (an attended run), is asked to fill in what stays unanswered
	// before the job is given up. nil (unattended) skips the job at once.
	Helper Helper
}

// ErrModalMissing: the apply button was clicked but no Easy Apply modal appeared.
var ErrModalMissing = errors.New("the Easy Apply modal did not open")

// Apply clicks the apply button and works the modal to the end.
func (a *Applier) Apply(ctx context.Context, job linkedin.Card, button browser.Element, opts Options) (Outcome, error) {
	if a.Logger == nil {
		a.Logger = slog.New(slog.DiscardHandler)
	}
	if a.Now == nil {
		a.Now = time.Now
	}
	out := Outcome{Resume: PreviousResume}
	if err := button.Click(); err != nil {
		return a.fail(job, &out, "clicking the apply button", err)
	}
	modal, err := a.Page.WaitElement(ctx, linkedin.ModalXPath, 8*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return a.stopped(&out), ctx.Err()
		}
		return a.fail(job, &out, "waiting for the Easy Apply modal", ErrModalMissing)
	}
	qjob := questions.Job{Link: job.Link, WorkLocation: job.Location, Description: opts.Description}
	answered := map[string]bool{}
	helped := map[string]bool{} // one help pause per distinct set of stuck questions
	var previousBlocked []string
	var stuck []stuckControl
	for page := 1; page <= MaxPages; page++ {
		if ctx.Err() != nil {
			return a.stopped(&out), ctx.Err()
		}
		controls, err := ReadForm(modal)
		if err != nil {
			return a.fail(job, &out, "reading the form", err)
		}
		var blocked []string
		stuck = stuck[:0]
		for _, c := range controls {
			if c.FollowCompany {
				continue
			}
			d := a.Answerer.AnswerContext(ctx, c.Question, qjob)
			if d.Action == questions.Blocked {
				blocked = append(blocked, describe(c.Question))
				stuck = append(stuck, stuckControl{c.Question, d})
			}
			if err := a.applyDecision(ctx, c, d); err != nil {
				if ctx.Err() != nil {
					return a.stopped(&out), ctx.Err()
				}
				return a.fail(job, &out, "filling "+c.Question.Label, err)
			}
			if d.Action == questions.Fill || d.Action == questions.Choose || d.Action == questions.Tick {
				if line := c.Question.Label + ": " + d.Value; !answered[line] {
					answered[line] = true
					out.Answered = append(out.Answered, line)
				}
			}
		}
		// A date picker's "today" button, when the page has one (the Python bot clicked it after every pass).
		if today, err := modal.Element(linkedin.TodayButtonXPath); err == nil {
			_ = today.Click()
		}
		sort.Strings(blocked)
		if len(blocked) > 0 && equal(blocked, previousBlocked) {
			// Nothing new was answered and required questions are still empty: LinkedIn will
			// never enable Next. In an attended run, ask the person at the keyboard once.
			signature := strings.Join(blocked, "\n")
			if a.Helper != nil && !helped[signature] {
				helped[signature] = true
				captured, err := a.askForHelp(ctx, job, modal, blocked)
				out.Captured += captured
				if ctx.Err() != nil {
					return a.stopped(&out), ctx.Err()
				}
				if err == nil {
					previousBlocked = nil
					continue // a fresh pass over what the user filled in
				}
				a.Logger.Info("no help came; giving the job up", "job", job.ID, "why", err)
			}
			// Not guessing is right, but it needs an exit. What nothing could answer goes to the
			// review queue, so the user answers it once and the next form like it goes through.
			for _, sc := range stuck {
				if a.Answerer.RememberUnanswered(sc.question, sc.decision, job.Link) {
					out.Queued++
				}
			}
			out.Blocked, out.Kind = blocked, Unanswered
			a.discard()
			return out, nil
		}
		previousBlocked = blocked
		if opts.ResumePath != "" && out.Resume == PreviousResume {
			if input, err := modal.Element(linkedin.ResumeUploadInputXPath); err == nil {
				if err := input.SetFiles([]string{opts.ResumePath}); err == nil {
					out.Resume = filepath.Base(opts.ResumePath)
					a.Logger.Info("resume uploaded", "job", job.ID, "file", out.Resume)
				} else {
					a.Logger.Warn("resume upload failed; keeping the previous one", "error", err)
				}
			}
		}
		if _, err := modal.Element(linkedin.SubmitButtonXPath); err == nil {
			return a.review(ctx, job, modal, &out, opts)
		}
		next, err := modal.Element(linkedin.ReviewButtonXPath)
		if err != nil {
			if next, err = modal.Element(linkedin.NextButtonXPath); err != nil {
				return a.fail(job, &out, "finding Next, Review or Submit", err)
			}
		}
		if err := next.Click(); err != nil {
			return a.fail(job, &out, "clicking Next", err)
		}
		if err := a.Pacer.BetweenActions(ctx); err != nil {
			return a.stopped(&out), err
		}
	}
	return a.fail(job, &out, "walking the form", fmt.Errorf("still not at Review after %d pages", MaxPages))
}

// review is the last page: discard in a dry run, otherwise set the follow box and submit.
func (a *Applier) review(ctx context.Context, job linkedin.Card, modal browser.Element, out *Outcome, opts Options) (Outcome, error) {
	if opts.DryRun {
		a.Logger.Info("dry run: reached Review with everything filled in; discarding", "job", job.ID)
		a.discard()
		out.Kind = DryRun
		return *out, nil
	}
	a.setFollowCompany(modal, opts.FollowCompanies)
	submit, err := modal.Element(linkedin.SubmitButtonXPath)
	if err != nil {
		return a.fail(job, out, "finding Submit", err)
	}
	if err := submit.Click(); err != nil {
		return a.fail(job, out, "clicking Submit", err)
	}
	// The application is in: whatever happens from here, it is recorded as applied.
	out.Kind = Applied
	a.Logger.Info("application submitted", "job", job.ID, "title", job.Title, "company", job.Company)
	if done, err := a.Page.WaitElement(context.Background(), linkedin.TextXPath("span", linkedin.DoneButtonText), 5*time.Second); err == nil {
		_ = done.Click()
	} else {
		_ = a.Page.Press("Escape")
	}
	return *out, nil
}

func (a *Applier) setFollowCompany(modal browser.Element, follow bool) {
	box, err := modal.Element(linkedin.FollowCompanyInputXPath)
	if err != nil {
		return
	}
	checked, err := box.Property("checked")
	if err != nil {
		a.Logger.Warn("could not read the follow-company box", "error", err)
		return
	}
	if (checked == "true") != follow {
		if label, err := modal.Element(linkedin.FollowCompanyLabelXPath); err == nil {
			_ = label.Click()
		}
	}
}

// stopped discards after a cancellation and marks the outcome.
func (a *Applier) stopped(out *Outcome) Outcome {
	a.discard()
	out.Kind = Stopped
	return *out
}

// applyDecision puts a decision into the control.
func (a *Applier) applyDecision(ctx context.Context, c Control, d questions.Decision) error {
	switch d.Action {
	case questions.Fill:
		if err := c.Element.Clear(); err != nil {
			return err
		}
		for _, r := range d.Value {
			if err := c.Element.Input(string(r)); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(a.Pacer.TypingDelay()):
			}
		}
		if d.Typeahead {
			// LinkedIn's location box is a typeahead: give the suggestions a moment, then
			// pick the first one (the Python bot slept a fixed two seconds here).
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(TypeaheadSettle):
			}
			_ = a.Page.Press("ArrowDown")
			_ = a.Page.Press("Enter")
		}
	case questions.Choose:
		if c.Question.Kind == questions.Select {
			return c.Element.SelectOption(d.Value)
		}
		if d.Option < 0 || d.Option >= len(c.RadioInputs) {
			return fmt.Errorf("radio option %d out of range", d.Option)
		}
		return c.RadioInputs[d.Option].Click()
	case questions.Tick:
		return c.Element.Click()
	case questions.Blocked:
		if d.Clear {
			return c.Element.Clear()
		}
	}
	return nil
}

// TypeaheadSettle is how long suggestions get to appear before the first is picked.
var TypeaheadSettle = 1500 * time.Millisecond

// DiscardWait is how long the "Save this application?" dialog gets to appear.
var DiscardWait = 3 * time.Second

// discard closes the modal and confirms the Discard dialog, Escape as fallback.
// It never depends on the run's context: a cancelled run must still close the
// modal, or every later click on the page would be swallowed.
func (a *Applier) discard() {
	ctx := context.Background()
	for attempt, dismiss := range []func(){
		func() {
			if b, err := a.Page.Element(linkedin.FromDocument(linkedin.ModalCloseXPath)); err == nil {
				_ = b.Click()
			}
		},
		func() { _ = a.Page.Press("Escape") },
	} {
		dismiss()
		if !a.modalOpen() {
			return
		}
		if b, err := a.Page.WaitElement(ctx, linkedin.FromDocument(linkedin.DiscardButtonXPath), DiscardWait); err == nil {
			_ = b.Click()
		}
		if !a.modalOpen() {
			return
		}
		a.Logger.Warn("the Easy Apply modal is still open after a discard attempt", "attempt", attempt+1)
	}
}

func (a *Applier) modalOpen() bool {
	els, err := a.Page.Elements(linkedin.ModalXPath)
	if err != nil {
		return false
	}
	for _, el := range els {
		if el.Visible() {
			return true
		}
	}
	return false
}

// stuckControl is a question the last pass could not answer, with the decision that said so.
type stuckControl struct {
	question questions.Question
	decision questions.Decision
}

// askForHelp is the help pause: snapshot the form, show the user what is stuck,
// wait for them, snapshot again, and remember as approved answers what they
// filled into controls that were empty. Only controls present and empty before
// the pause count: a control that appeared afterwards belongs to another page
// (the user clicked Next anyway) and carries LinkedIn's prefills, not answers.
func (a *Applier) askForHelp(ctx context.Context, job linkedin.Card, modal browser.Element, blocked []string) (int, error) {
	before, err := ReadForm(modal)
	if err != nil {
		return 0, err
	}
	shot := a.screenshot(job, "help")
	if err := a.Helper.Help(ctx, HelpRequest{Job: job, Questions: blocked, Screenshot: shot}); err != nil {
		return 0, err
	}
	after, err := ReadForm(modal)
	if err != nil {
		a.Logger.Warn("could not re-read the form after the help pause; nothing captured", "error", err)
		return 0, nil
	}
	empty := map[string]bool{}
	for _, c := range before {
		if !c.FollowCompany && filled(c.Question) == "" {
			empty[snapshotKey(c.Question)] = true
		}
	}
	captured := 0
	for _, c := range after {
		if c.FollowCompany || !empty[snapshotKey(c.Question)] {
			continue
		}
		if value := filled(c.Question); value != "" && a.Answerer.Capture(c.Question, value, job.Link) {
			captured++
		}
	}
	return captured, nil
}

// snapshotKey identifies a control across two reads of the same page.
func snapshotKey(q questions.Question) string {
	return string(q.Kind) + "\x00" + q.GroupTitle + "\x00" + q.Label
}

// filled is a control's current answer, "" when it is empty or on a placeholder.
func filled(q questions.Question) string {
	if q.Kind == questions.Select && questions.IsPlaceholder(q.Current) {
		return ""
	}
	return strings.TrimSpace(q.Current)
}

// screenshot saves the page as <job>-<tag>-<time>.png and returns the full path, "" on failure.
func (a *Applier) screenshot(job linkedin.Card, tag string) string {
	if a.ScreenshotDir == "" {
		return ""
	}
	path := filepath.Join(a.ScreenshotDir, fmt.Sprintf("%s-%s-%s.png", job.ID, tag, a.Now().Format("20060102-150405")))
	if err := a.Page.Screenshot(path); err != nil {
		a.Logger.Warn("screenshot failed", "error", err)
		return ""
	}
	return path
}

// fail screenshots, discards and returns the error with the stage named.
func (a *Applier) fail(job linkedin.Card, out *Outcome, stage string, err error) (Outcome, error) {
	out.Kind, out.Stage = Failed, stage
	if shot := a.screenshot(job, "failed"); shot != "" {
		out.Screenshot = filepath.Base(shot)
	}
	a.discard()
	return *out, fmt.Errorf("%s: %w", stage, err)
}

func describe(q questions.Question) string {
	if len(q.Options) > 0 {
		return fmt.Sprintf("%s [%s]", q.Label, strings.Join(q.Options, ", "))
	}
	if q.Kind == questions.Checkbox {
		return strings.TrimSpace(q.GroupTitle + " " + q.Label)
	}
	return q.Label
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
