package apply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/pacing"
	"github.com/luizfreitas27/ajai/internal/questions"
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
	Captured   int      // answers the user typed during a help pause and ajai remembered
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
	st := &formState{job: job, qjob: questions.Job{Link: job.Link, WorkLocation: job.Location, Description: opts.Description},
		answered: map[string]bool{}, helped: map[string]bool{}, userFilled: map[string]bool{}}
	var err error
	if st.modal, err = a.Page.WaitElement(ctx, linkedin.ModalXPath, modalOpenWait); err != nil {
		if ctx.Err() != nil {
			return a.stopped(&out), ctx.Err()
		}
		return a.fail(job, &out, "waiting for the Easy Apply modal", ErrModalMissing)
	}
	// Every turn of this loop either returns, clicks Next (at most MaxPages times) or
	// follows a help pause (at most MaxHelpPauses times), so it always ends.
	for pages := 0; ; {
		if ctx.Err() != nil {
			return a.stopped(&out), ctx.Err()
		}
		blocked, stuck, stage, err := a.pass(ctx, st, &out)
		if err != nil {
			if ctx.Err() != nil {
				return a.stopped(&out), ctx.Err()
			}
			return a.fail(job, &out, stage, err)
		}
		if len(blocked) > 0 && equal(blocked, st.previousBlocked) {
			// Nothing new was answered and required questions are still empty: LinkedIn will
			// never enable Next. In an attended run, ask the person at the keyboard.
			if a.tryHelp(ctx, st, &out, blocked, stuck) {
				st.previousBlocked = nil
				continue // a fresh pass over what the user filled in
			}
			if ctx.Err() != nil {
				return a.stopped(&out), ctx.Err()
			}
			return a.giveUp(st, &out, blocked, stuck), nil
		}
		st.previousBlocked, st.stuck = blocked, stuck
		if a.uploadResume(st, &out, opts.ResumePath) {
			// LinkedIn takes the file, and a person would not press on at once.
			if err := a.Pacer.BetweenActions(ctx); err != nil {
				return a.stopped(&out), err
			}
		}
		if _, err := st.modal.Element(linkedin.SubmitButtonXPath); err == nil {
			return a.review(ctx, st, &out, opts)
		}
		if controls, err := ReadForm(st.modal); err == nil {
			if page := pageSignature(st.modal, controls); page == st.page && pages > 0 {
				st.samePage++
			} else {
				st.page, st.samePage = page, 0
			}
		}
		if st.samePage >= stuckAfter && len(blocked) == 0 { // LinkedIn will not take the page and says nothing: no question to name
			return a.giveUp(st, &out, []string{"the form stayed on the same page after Next, with no question flagged"}, nil), nil
		}
		if pages == MaxPages {
			if len(blocked) > 0 {
				return a.giveUp(st, &out, blocked, stuck), nil
			}
			return a.fail(job, &out, "walking the form", fmt.Errorf("still not at Review after %d pages", MaxPages))
		}
		next, err := nextButton(st.modal)
		if err != nil {
			return a.fail(job, &out, "finding Next, Review or Submit", err)
		}
		err = next.Click()
		if notReady := (*browser.NotReadyError)(nil); errors.As(err, &notReady) && notReady.Reason == browser.LeftThePage {
			// LinkedIn drew the button anew while it was waited for (as while it takes an
			// uploaded resume), sometimes with the whole modal: the Next it drew is pressed.
			if err = a.findModalAgain(ctx, st); err == nil {
				if next, err = nextButton(st.modal); err == nil {
					err = next.Click()
				}
				if err != nil {
					err = fmt.Errorf("in the modal found again: %w", err)
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return a.stopped(&out), ctx.Err()
			}
			return a.fail(job, &out, "clicking Next", err)
		}
		pages++
		if err := a.Pacer.BetweenActions(ctx); err != nil {
			return a.stopped(&out), err
		}
	}
}

// modalOpenWait bounds the wait for the modal to open after the apply button;
// modalRedrawWait the look for it again once LinkedIn drew it anew.
const (
	modalOpenWait   = 8 * time.Second
	modalRedrawWait = 3 * time.Second
)

// findModalAgain finds the Easy Apply modal again from the page and makes it
// the form's: LinkedIn sometimes draws the whole modal anew, and the rest of
// the application goes on in the one it drew.
func (a *Applier) findModalAgain(ctx context.Context, st *formState) error {
	modal, err := a.Page.WaitElement(ctx, linkedin.ModalXPath, modalRedrawWait)
	if err != nil {
		return fmt.Errorf("the Easy Apply modal is gone: %w", err)
	}
	st.modal = modal
	return nil
}

// nextButton is the button that moves the form on: Review when the page has
// it, Next otherwise.
func nextButton(modal browser.Element) (browser.Element, error) {
	if next, err := modal.Element(linkedin.ReviewButtonXPath); err == nil {
		return next, nil
	}
	return modal.Element(linkedin.NextButtonXPath)
}

// refusedReason starts the reason of a question LinkedIn refused.
const refusedReason = "LinkedIn refused it: "

// refused is the decision for a question LinkedIn refused, with its message:
// blocked, and queued for review unless the config gave the answer (which is
// then fixed in the config, not in the queue).
func refused(d questions.Decision, message string) questions.Decision {
	d.Action, d.Reason, d.Remember = questions.Blocked, refusedReason+message, d.Source != "config"
	return d
}

// pageSignature tells one page of the form from another: how far the
// application is, and the questions on it.
func pageSignature(modal browser.Element, controls []Control) string {
	var parts []string
	if p, err := modal.Element(linkedin.FormProgressXPath); err == nil {
		label, _ := p.Attr("aria-label")
		parts = append(parts, label)
	}
	for _, c := range controls {
		parts = append(parts, c.Question.Label)
	}
	return strings.Join(parts, "\n")
}

// stuckAfter: Next pressed this many times without the page changing means
// LinkedIn will not take the page, even when it says nothing; one press is
// allowed for a page slow to change.
const stuckAfter = 2

// MaxHelpPauses bounds the help pauses in one application: a user who fills one
// question per pause still gets a few rounds, but the job cannot loop forever.
const MaxHelpPauses = 5

// formState is what one application accumulates while the modal is worked.
type formState struct {
	job             linkedin.Card
	modal           browser.Element
	qjob            questions.Job
	answered        map[string]bool // "label: value" lines already recorded
	helped          map[string]bool // stuck-question sets the user was already asked about
	userFilled      map[string]bool // controls the user filled during a pause: never touched again
	previousBlocked []string
	stuck           []stuckControl
	page            string // the page shown last (pageSignature): a page that stays after Next did not advance
	samePage        int    // Next pressed that many times without the page changing
}

// pass answers every control on the current page once and returns what stayed
// unanswered. stage names what was being done when err is not nil.
func (a *Applier) pass(ctx context.Context, st *formState, out *Outcome) (blocked []string, stuck []stuckControl, stage string, err error) {
	controls, err := ReadForm(st.modal)
	if err != nil {
		return nil, nil, "reading the form", err
	}
	for _, c := range controls {
		key := snapshotKey(c.Question)
		if c.FollowCompany || (st.userFilled[key] && c.Refusal == "") {
			continue
		}
		var d questions.Decision
		if !st.userFilled[key] { // what the user put in is never answered over
			d = a.Answerer.AnswerContext(ctx, c.Question, st.qjob)
		}
		if c.Refusal != "" { // LinkedIn refused what is there, whoever put it: it will not take it as is
			d = refused(d, c.Refusal)
		}
		if d.Action == questions.Blocked {
			blocked = append(blocked, describe(c.Question, d))
			stuck = append(stuck, stuckControl{c.Question, d})
		}
		if err := a.applyDecision(ctx, c, d); err != nil {
			return nil, nil, "filling " + c.Question.Label, err
		}
		if d.Action == questions.Fill || d.Action == questions.Choose || d.Action == questions.Tick {
			if line := c.Question.Label + ": " + d.Value; !st.answered[line] {
				st.answered[line] = true
				out.Answered = append(out.Answered, line)
			}
		}
	}
	// A date picker's "today" button, when the page has one (the Python bot clicked it after every pass).
	if today, err := st.modal.Element(linkedin.TodayButtonXPath); err == nil {
		_ = today.Click()
	}
	sort.Strings(blocked)
	return slices.Compact(blocked), stuck, "", nil // LinkedIn can draw one question twice: it is named once
}

// tryHelp runs a help pause when one is allowed (an attended run, a set of stuck
// questions not asked about yet, pauses left) and reports whether help came.
func (a *Applier) tryHelp(ctx context.Context, st *formState, out *Outcome, blocked []string, stuck []stuckControl) bool {
	signature := strings.Join(blocked, "\n")
	if a.Helper == nil || st.helped[signature] || len(st.helped) >= MaxHelpPauses {
		return false
	}
	st.helped[signature] = true
	captured, err := a.askForHelp(ctx, st, blocked, stuck)
	out.Captured += captured
	if err != nil {
		if ctx.Err() == nil {
			a.Logger.Info("no help came; giving the job up", "job", st.job.ID, "why", err)
		}
		return false
	}
	return true
}

// giveUp ends an application whose required questions nothing could answer:
// they go to the review queue, so the user answers each once and the next form
// like it goes through. Not guessing is right, but it needs an exit.
func (a *Applier) giveUp(st *formState, out *Outcome, blocked []string, stuck []stuckControl) Outcome {
	for _, sc := range stuck {
		if a.Answerer.RememberUnanswered(sc.question, sc.decision, st.job.Link) {
			out.Queued++
		}
	}
	out.Blocked, out.Kind = blocked, Unanswered
	a.discard()
	return *out
}

// uploadResume sets the resume file once per application, when the page has
// the input, and reports whether it did.
func (a *Applier) uploadResume(st *formState, out *Outcome, path string) bool {
	if path == "" || out.Resume != PreviousResume {
		return false
	}
	input, err := st.modal.Element(linkedin.ResumeUploadInputXPath)
	if err != nil {
		return false
	}
	if err := input.SetFiles([]string{path}); err != nil {
		a.Logger.Warn("resume upload failed; keeping the previous one", "error", err)
		return false
	}
	out.Resume = filepath.Base(path)
	a.Logger.Info("resume uploaded", "job", st.job.ID, "file", out.Resume)
	return true
}

// review is the last page: discard in a dry run, otherwise set the follow box and submit.
func (a *Applier) review(ctx context.Context, st *formState, out *Outcome, opts Options) (Outcome, error) {
	job, modal := st.job, st.modal
	if opts.DryRun {
		a.Logger.Info("dry run: reached Review with everything filled in; discarding", "job", job.ID)
		a.discard()
		out.Kind = DryRun
		return *out, nil
	}
	// A person glances over the Review page before submitting: never at once. The
	// page is read after the pause, so nothing found on it has been drawn anew since.
	if err := a.Pacer.BetweenActions(ctx); err != nil {
		return a.stopped(out), err
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
	a.closeSent()
	return *out, nil
}

// closeSent closes the "Application sent" dialog LinkedIn draws over the whole
// page after Submit: its Done, else its close button, else Escape. Left open,
// it covers every card that follows.
func (a *Applier) closeSent() {
	if done, err := a.Page.WaitElement(context.Background(), linkedin.DialogButtonXPath(linkedin.DoneButtonText), 5*time.Second); err == nil && done.Click() == nil {
		return
	}
	if x, err := a.Page.Element(linkedin.FromDocument(linkedin.ModalCloseXPath)); err == nil && x.Click() == nil {
		return
	}
	a.Logger.Warn("could not close the Application sent dialog; pressing Escape")
	_ = a.Page.Press("Escape")
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
		return chooseOption(c, d.Option)
	case questions.Tick:
		return clickThrough(c.Element, c.BoxLabel, true)
	case questions.Blocked:
		if d.Clear {
			return c.Element.Clear()
		}
	}
	return nil
}

// chooseOption picks option i of a choice and no other, so a single-answer group
// never ends with two options ticked.
func chooseOption(c Control, i int) error {
	if err := clickThrough(c.RadioInputs[i], labelAt(c.RadioLabels, i), true); err != nil {
		return err
	}
	for j, in := range c.RadioInputs {
		if j != i && ticked(in) {
			if err := clickThrough(in, labelAt(c.RadioLabels, j), false); err != nil {
				return err
			}
		}
	}
	return nil
}

// clickThrough clicks in. When the page refuses the click (LinkedIn draws its
// checkboxes with pointer-events none, the label takes the click), it clicks the
// label instead and checks that the input reached the wanted state.
func clickThrough(in, label browser.Element, want bool) error {
	err := in.Click()
	if err == nil || label == nil {
		return err
	}
	if lerr := label.Click(); lerr != nil {
		return fmt.Errorf("%w (its label: %v)", err, lerr)
	}
	if !reached(in, want) {
		return fmt.Errorf("%w (its label did not set it)", err)
	}
	return nil
}

// settleWait bounds how long a box gets to show its new state after a label
// click: a page may re-render the question a moment later. A var so tests can
// shorten it.
var settleWait = time.Second

// reached says whether the box is in the wanted state, looking again for up to
// settleWait. It reads the live checked property, never the attribute.
func reached(in browser.Element, want bool) bool {
	for deadline := time.Now().Add(settleWait); ; {
		if ticked(in) == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func labelAt(labels []browser.Element, i int) browser.Element {
	if i < len(labels) {
		return labels[i]
	}
	return nil
}

func ticked(in browser.Element) bool {
	v, _ := in.Property("checked")
	return v == "true"
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
//
// Only controls ajai could not answer and would consult the memory for are
// captured (see questions.Answerer.Capture); the others are shown to the user
// with what to fix in the config instead.
func (a *Applier) askForHelp(ctx context.Context, st *formState, blocked []string, stuck []stuckControl) (int, error) {
	decisions := map[string]questions.Decision{}
	for _, sc := range stuck {
		decisions[snapshotKey(sc.question)] = sc.decision
	}
	before, err := ReadForm(st.modal)
	if err != nil {
		return 0, err
	}
	shot := a.screenshot(st.job, "help")
	if err := a.Helper.Help(ctx, HelpRequest{Job: st.job, Questions: blocked, Screenshot: shot}); err != nil {
		return 0, err
	}
	after, err := ReadForm(st.modal)
	if err != nil {
		a.Logger.Warn("could not re-read the form after the help pause; nothing captured", "error", err)
		return 0, nil
	}
	open := map[string]string{} // what the user is asked to fill: empty fields, and fields LinkedIn refused, with what they held
	for _, c := range before {
		if !c.FollowCompany && (filled(c.Question) == "" || c.Refusal != "") {
			open[snapshotKey(c.Question)] = filled(c.Question)
		}
	}
	captured := 0
	for _, c := range after {
		key := snapshotKey(c.Question)
		held, asked := open[key]
		if c.FollowCompany || !asked {
			continue
		}
		value := filled(c.Question)
		if value == "" || value == held { // left empty, or the refused value left as it was
			continue
		}
		// What the user put in stays as they left it, captured or not (a sensitive
		// answer is not remembered, but it must not be wiped by the next pass either).
		st.userFilled[key] = true
		if a.Answerer.Capture(c.Question, decisions[key], value, st.job.Link) {
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

// describe is how a stuck question is shown to the user and recorded: its label,
// its options, and, when the cause is the configuration rather than a gap in
// what ajai knows, that cause.
func describe(q questions.Question, d questions.Decision) string {
	text := q.Label
	switch {
	case len(q.Options) > 0:
		text = fmt.Sprintf("%s [%s]", q.Label, strings.Join(q.Options, ", "))
	case q.Kind == questions.Checkbox:
		text = strings.TrimSpace(strings.TrimPrefix(q.GroupTitle+" ", questions.UnknownTitle+" ") + q.Label)
	}
	switch {
	case strings.HasPrefix(d.Reason, "no option matched the configured answer"):
		text += " (" + d.Reason + "; fix it in the config)"
	case strings.HasPrefix(d.Reason, refusedReason) && d.Source == "config":
		text += " (" + d.Reason + "; fix it in the config)"
	case strings.HasPrefix(d.Reason, refusedReason):
		text += " (" + d.Reason + ")"
	}
	return text
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
