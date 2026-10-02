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
	var previousBlocked []string
	for page := 1; page <= MaxPages; page++ {
		if ctx.Err() != nil {
			return a.stopped(&out), ctx.Err()
		}
		controls, err := ReadForm(modal)
		if err != nil {
			return a.fail(job, &out, "reading the form", err)
		}
		var blocked []string
		for _, c := range controls {
			if c.FollowCompany {
				continue
			}
			d := a.Answerer.Answer(c.Question, qjob)
			if d.Action == questions.Blocked {
				blocked = append(blocked, describe(c.Question))
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
			// never enable Next. Not guessing is right, but it needs an exit.
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

// fail screenshots, discards and returns the error with the stage named.
func (a *Applier) fail(job linkedin.Card, out *Outcome, stage string, err error) (Outcome, error) {
	out.Kind, out.Stage = Failed, stage
	if a.ScreenshotDir != "" {
		path := filepath.Join(a.ScreenshotDir, fmt.Sprintf("%s-%s.png", job.ID, a.Now().Format("20060102-150405")))
		if serr := a.Page.Screenshot(path); serr == nil {
			out.Screenshot = filepath.Base(path)
		} else {
			a.Logger.Warn("screenshot failed", "error", serr)
		}
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
