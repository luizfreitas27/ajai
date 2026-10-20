package linkedin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/pacing"
)

// Session drives LinkedIn in one tab: login state, search, filters, job cards,
// job details. It knows the markup (selectors.go) and nothing about what to do
// with a job; the run loop decides that.
type Session struct {
	Page      browser.Page
	Browser   browser.Browser // for tab handling; nil in tests that only need the page
	Pacer     *pacing.Pacer
	Logger    *slog.Logger
	CloseTabs bool // close the tab an external application opened (settings.close_tabs)
}

// NewSession wires a session; br and logger may be nil.
func NewSession(page browser.Page, br browser.Browser, pacer *pacing.Pacer, logger *slog.Logger) *Session {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Session{Page: page, Browser: br, Pacer: pacer, Logger: logger}
}

// URLs.
const (
	LoginURL  = "https://www.linkedin.com/login"
	FeedURL   = "https://www.linkedin.com/feed"
	SearchURL = "https://www.linkedin.com/jobs/search/?keywords="
	JobURL    = "https://www.linkedin.com/jobs/view/"
)

// IsLoggedIn is true only when the tab is on the feed. LinkedIn sends a
// signed-out visitor from the feed to /login or an authwall, in whatever
// language it picked for the visitor, so the URL is the one reliable,
// language-independent signal. (The Python bot assumed "logged in" when it saw
// no Sign in link, which on a Portuguese page is always.)
func (s *Session) IsLoggedIn() bool {
	u, err := s.Page.URL()
	return err == nil && strings.HasPrefix(u, FeedURL)
}

// ErrLoginTimeout: the user did not sign in within the allowed time.
var ErrLoginTimeout = errors.New("no LinkedIn login detected in time")

// EnsureLoggedIn opens the feed and, when LinkedIn redirects to a sign-in page,
// waits for the user to sign in by hand (ajai never types a password). onWait is
// called once when waiting starts, so the caller can tell the user.
func (s *Session) EnsureLoggedIn(ctx context.Context, timeout time.Duration, onWait func()) error {
	if err := s.Page.Navigate(ctx, FeedURL); err != nil {
		return err
	}
	if s.IsLoggedIn() {
		return nil
	}
	if onWait != nil {
		onWait()
	}
	deadline := time.Now().Add(timeout)
	poll := min(5*time.Second, timeout) // a short timeout is not overshot by a long poll
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
		if s.IsLoggedIn() {
			s.Logger.Info("linkedin login detected")
			return nil
		}
		if time.Now().After(deadline) {
			return ErrLoginTimeout
		}
	}
}

// Search opens the job search for a term.
func (s *Session) Search(ctx context.Context, term string) error {
	return s.Page.Navigate(ctx, SearchURL+url.QueryEscape(term))
}

// TypeHuman types text one character at a time with the pacer's delays.
func (s *Session) TypeHuman(ctx context.Context, el browser.Element, text string) error {
	for _, r := range text {
		if err := el.Input(string(r)); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Pacer.TypingDelay()):
		}
	}
	return nil
}

// TextXPath matches a tag whose text contains text, ignoring ASCII case (the
// Python text_xpath). The text is embedded as a proper XPath string literal.
func TextXPath(tag, text string) string {
	return fmt.Sprintf(`.//%s[contains(translate(normalize-space(.), "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz"), %s)]`,
		tag, XPathLiteral(strings.ToLower(strings.TrimSpace(text))))
}

// XPathLiteral quotes text for XPath 1.0, which has no escapes: a string with
// both quote kinds becomes a concat() of pieces.
func XPathLiteral(text string) string {
	switch {
	case !strings.Contains(text, `"`):
		return `"` + text + `"`
	case !strings.Contains(text, `'`):
		return `'` + text + `'`
	}
	parts := strings.Split(text, `"`)
	quoted := make([]string, 0, 2*len(parts))
	for i, part := range parts {
		if i > 0 {
			quoted = append(quoted, `'"'`)
		}
		if part != "" {
			quoted = append(quoted, `"`+part+`"`)
		}
	}
	return "concat(" + strings.Join(quoted, ", ") + ")"
}

// clickText clicks the first visible span carrying text; false when none.
func (s *Session) clickText(ctx context.Context, text string, wait time.Duration) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	el, err := s.Page.WaitElement(ctx, TextXPath("span", text), wait)
	if err != nil {
		s.Logger.Warn("filter control not found", "text", text)
		return false
	}
	if err := el.Click(); err != nil {
		s.Logger.Warn("filter click failed", "text", text, "error", err)
		return false
	}
	_ = s.Pacer.BetweenActions(ctx)
	return true
}

// addCompany is the Python company_search_click: a company that is not in the
// filter list is typed into "Add a company" and picked from the suggestions.
func (s *Session) addCompany(ctx context.Context, name string) {
	s.clickText(ctx, "Add a company", time.Second)
	box, err := s.Page.Element(AddCompanyInputXPath)
	if err != nil {
		s.Logger.Warn("company search box not found", "company", name)
		return
	}
	_ = box.Click()
	_ = box.Clear()
	if err := s.TypeHuman(ctx, box, name); err != nil {
		return
	}
	_ = s.Pacer.BetweenActions(ctx)
	_ = s.Page.Press("ArrowDown")
	_ = s.Page.Press("Enter")
	s.Logger.Info("company added to the filter by search", "company", name)
}

func (s *Session) clickSwitch(ctx context.Context, title string) {
	container, err := s.Page.Element(TextXPath("h3", title) + "/ancestor::fieldset")
	if err != nil {
		s.Logger.Warn("filter switch not found", "title", title)
		return
	}
	sw, err := container.Element(FilterSwitchXPath)
	if err != nil {
		s.Logger.Warn("filter switch input not found", "title", title)
		return
	}
	if switchOn(sw) { // already on: a click would turn it off
		return
	}
	// The input is drawn over by LinkedIn's toggle (pointer-events none on it):
	// clicked where it shows, as the Python bot's mouse did.
	err = sw.ClickAtCentre()
	_ = s.Pacer.BetweenActions(ctx)
	switch {
	case err != nil:
		s.Logger.Warn("filter switch click failed", "title", title, "error", err)
	case !switchOn(sw):
		s.Logger.Warn("filter switch did not turn on", "title", title)
	}
}

// switchOn says whether a switch is on, by its state or by what it tells a
// screen reader (LinkedIn's toggle keeps both).
func switchOn(sw browser.Element) bool {
	if on, _ := sw.Property("checked"); on == "true" {
		return true
	}
	aria, _ := sw.Attr("aria-checked")
	return aria == "true"
}

// ApplyFilters sets the search location and every configured filter through
// LinkedIn's "All filters" panel, as the Python apply_filters did. Each control
// that cannot be found is logged and skipped; the search still runs.
func (s *Session) ApplyFilters(ctx context.Context, f config.Search, location string) error {
	if strings.TrimSpace(location) != "" {
		if el, err := s.Page.Element(SearchLocationInputXPath); err == nil {
			_ = el.Click()
			_ = el.Clear()
			if err := s.TypeHuman(ctx, el, strings.TrimSpace(location)); err != nil {
				return err
			}
			_ = s.Pacer.BetweenActions(ctx)
			_ = s.Page.Press("Enter")
		} else {
			s.Logger.Warn("search location box not found; continuing with LinkedIn's default")
		}
	}
	all, err := s.Page.WaitElement(ctx, AllFiltersButtonXPath, 10*time.Second)
	if err != nil {
		return fmt.Errorf("the All filters button did not appear: %w", err)
	}
	if err := all.Click(); err != nil {
		return err
	}
	_ = s.Pacer.BetweenActions(ctx)

	short := 2 * time.Second
	s.clickText(ctx, f.SortBy, short)
	s.clickText(ctx, f.DatePosted, short)
	for _, text := range f.ExperienceLevel {
		s.clickText(ctx, text, short)
	}
	for _, company := range f.Companies {
		if !s.clickText(ctx, company, short) {
			s.addCompany(ctx, company)
		}
	}
	for _, group := range [][]string{f.JobType, f.OnSite, f.Locations, f.Industry, f.JobFunction, f.JobTitles} {
		for _, text := range group {
			s.clickText(ctx, text, short)
		}
	}
	if f.EasyApplyOnly {
		s.clickSwitch(ctx, "Easy Apply")
	}
	if f.Under10Applicants {
		s.clickSwitch(ctx, "Under 10 applicants")
	}
	if f.InYourNetwork {
		s.clickSwitch(ctx, "In your network")
	}
	if f.FairChanceEmployer {
		s.clickSwitch(ctx, "Fair Chance Employer")
	}
	s.clickText(ctx, f.Salary, short)
	for _, text := range append(append([]string{}, f.Benefits...), f.Commitments...) {
		s.clickText(ctx, text, short)
	}
	show, err := s.Page.WaitElement(ctx, ShowResultsButtonXPath, 10*time.Second)
	if err != nil {
		return fmt.Errorf("the Show results button did not appear: %w", err)
	}
	if err := show.Click(); err != nil {
		return err
	}
	select { // let the results reload settle before anything reads the list
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	return nil
}

// Card is what a job card in the results list says.
type Card struct {
	ID             string
	Title          string
	Company        string
	Location       string
	Style          string // Remote, Hybrid, On-site, Unknown
	AlreadyApplied bool
	Link           string
}

// Posting names the role a card shows: one title, company and location are one
// role, however many jobs LinkedIn lists it under (case and spacing aside). A
// card missing any of them names only its own job, so unreadable cards never
// pass for each other.
func (c Card) Posting() string {
	if strings.TrimSpace(c.Title) == "" || strings.TrimSpace(c.Company) == "" || c.Location == "" || c.Location == "Unknown" {
		return "job " + c.ID
	}
	return strings.ToLower(strings.Join(strings.Fields(c.Title+" | "+c.Company+" | "+c.Location), " "))
}

// WaitForCards waits for the results list to render.
func (s *Session) WaitForCards(ctx context.Context) error {
	_, err := s.Page.WaitElement(ctx, JobCardsXPath, 15*time.Second)
	return err
}

// Cards re-queries the virtualised results list; call it before every index.
func (s *Session) Cards() ([]browser.Element, error) { return s.Page.Elements(JobCardsXPath) }

// ReadCard extracts the card's details (without opening it).
func (s *Session) ReadCard(card browser.Element) (Card, error) {
	var c Card
	c.ID, _ = card.Attr(JobIDAttribute)
	link, err := card.Element(JobCardTitleLinkXPath)
	if err != nil {
		return c, fmt.Errorf("card %s has no title link: %w", c.ID, err)
	}
	title, ok := link.Attr("aria-label")
	if !ok || title == "" {
		title, _ = link.Text()
	}
	c.Title = strings.TrimSpace(strings.TrimSuffix(strings.SplitN(title, "\n", 2)[0], TitleVerificationSuffix))
	if company, err := card.Element(JobCardCompanyXPath); err == nil {
		t, _ := company.Text()
		c.Company = strings.TrimSpace(t)
	}
	c.Location, c.Style = "Unknown", "Unknown"
	if loc, err := card.Element(JobCardLocationXPath); err == nil {
		t, _ := loc.Text()
		c.Location, c.Style = SplitLocation(strings.TrimSpace(t))
	}
	if state, err := card.Element("." + ClassXPath(JobCardFooterStateClass)); err == nil {
		t, _ := state.Text()
		c.AlreadyApplied = strings.TrimSpace(t) == "Applied"
	}
	c.Link = JobURL + c.ID
	return c, nil
}

// SplitLocation turns "New York, United States (Hybrid)" into its two parts.
func SplitLocation(text string) (location, style string) {
	if i := strings.LastIndex(text, "("); i >= 0 && strings.HasSuffix(text, ")") {
		return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1 : len(text)-1])
	}
	if text == "" {
		return "Unknown", "Unknown"
	}
	return text, "Unknown"
}

// OpenCard scrolls the card into view like a reader would, pauses, then clicks
// its title so the details pane shows this job.
func (s *Session) OpenCard(ctx context.Context, card browser.Element) error {
	link, err := card.Element(JobCardTitleLinkXPath)
	if err != nil {
		return err
	}
	_ = card.ScrollIntoView()
	if err := s.Pacer.BetweenActions(ctx); err != nil {
		return err
	}
	if err := link.Click(); err != nil {
		return err
	}
	return s.Pacer.BetweenActions(ctx)
}

// AlreadyAppliedOnPage is the job page's own "application submitted" link, the
// Python backup check after the card's badge.
func (s *Session) AlreadyAppliedOnPage() bool {
	_, err := s.Page.Element(AppliedOnPageXPath)
	return err == nil
}

// Description returns the job description text, ok false when it cannot be read.
func (s *Session) Description(ctx context.Context) (string, bool) {
	el, err := s.Page.WaitElement(ctx, ClassXPath(JobDescriptionClass), 5*time.Second)
	if err != nil {
		return "", false
	}
	t, err := el.Text()
	return t, err == nil
}

// AboutCompany returns the "About the company" text, "" when absent.
func (s *Session) AboutCompany() string {
	el, err := s.Page.Element(ClassXPath(AboutCompanyClass))
	if err != nil {
		return ""
	}
	t, _ := el.Text()
	return t
}

var postedRe = regexp.MustCompile(`(?i)(\d+)\s+(second|minute|hour|day|week|month|year)s?\s+ago`)

// DatePosted parses the top card's "Reposted 3 days ago" into an approximate
// time and whether the job was reposted. ok false when nothing was found.
func (s *Session) DatePosted(now time.Time) (posted time.Time, reposted, ok bool) {
	el, err := s.Page.Element(PostedAgoXPath)
	if err != nil {
		return time.Time{}, false, false
	}
	text, _ := el.Text()
	return ParsePosted(text, now)
}

// ParsePosted is the pure half of DatePosted.
func ParsePosted(text string, now time.Time) (posted time.Time, reposted, ok bool) {
	reposted = strings.Contains(text, "Reposted")
	m := postedRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, reposted, false
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"second": time.Second, "minute": time.Minute, "hour": time.Hour,
		"day": 24 * time.Hour, "week": 7 * 24 * time.Hour, "month": 30 * 24 * time.Hour, "year": 365 * 24 * time.Hour}[strings.ToLower(m[2])]
	return now.Add(-time.Duration(n) * unit), reposted, true
}

// ApplyKind says what the job page's apply button is.
type ApplyKind string

// Apply kinds.
const (
	NoApplyButton ApplyKind = "none"
	EasyApply     ApplyKind = "easy"
	ExternalApply ApplyKind = "external"
)

// FindApplyButton reports the apply button on the page and its kind without
// clicking. The kind is read from the button itself: LinkedIn labels an in-app
// application "Easy Apply" and an off-site one "Apply". (The Python bot clicked
// to find out; a dry run must not.)
func (s *Session) FindApplyButton() (browser.Element, ApplyKind, string) {
	for _, loc := range EasyApplyLocators {
		el, err := s.Page.Element(strings.TrimPrefix(loc.XPath, "."))
		if err != nil {
			continue
		}
		label, _ := el.Attr("aria-label")
		text, _ := el.Text()
		if strings.Contains(label, EasyApplyAriaLabelPrefix) || strings.Contains(text, EasyApplyAriaLabelPrefix) || strings.Contains(loc.XPath, "openSDUIApplyFlow") {
			return el, EasyApply, loc.How
		}
		return el, ExternalApply, loc.How
	}
	return nil, NoApplyButton, ""
}

// ExternalLink clicks an off-site apply button, reads the URL of the tab it
// opens, closes that tab and returns to LinkedIn. "" when no tab opened.
func (s *Session) ExternalLink(ctx context.Context, button browser.Element) (string, error) {
	if s.Browser == nil {
		return "", errors.New("no browser handle for tabs")
	}
	before, err := s.Browser.Pages()
	if err != nil {
		return "", err
	}
	if err := button.Click(); err != nil {
		return "", err
	}
	if cont, err := s.Page.WaitElement(ctx, TextXPath("span", ContinueButtonText), time.Second); err == nil {
		_ = cont.Click()
	}
	_ = s.Pacer.BetweenActions(ctx)
	after, err := s.Browser.Pages()
	if err != nil {
		return "", err
	}
	if len(after) <= len(before) {
		return "", errors.New("no new tab opened")
	}
	tab := after[len(after)-1]
	link, _ := tab.URL()
	if s.CloseTabs {
		_ = tab.Close()
	}
	return link, nil
}

// DailyLimitReached reads LinkedIn's inline notice about the Easy Apply cap.
func (s *Session) DailyLimitReached() bool {
	el, err := s.Page.Element(InlineFeedbackXPath)
	if err != nil {
		return false
	}
	t, _ := el.Text()
	return strings.Contains(t, DailyLimitPhrase)
}

// NextPage moves to the following results page; false at the last page.
func (s *Session) NextPage(ctx context.Context) (bool, error) {
	var pagination browser.Element
	for _, cls := range PaginationClasses {
		if el, err := s.Page.Element(ClassXPath(cls)); err == nil {
			pagination = el
			break
		}
	}
	if pagination == nil {
		return false, nil
	}
	active, err := pagination.Element(PaginationActiveXPath)
	if err != nil {
		return false, nil
	}
	text, _ := active.Text()
	current, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return false, nil
	}
	next, err := pagination.Element(PageButtonXPath(current + 1))
	if err != nil {
		return false, nil
	}
	if err := next.Click(); err != nil {
		return false, err
	}
	s.Logger.Info("results page", "page", current+1)
	return true, s.Pacer.BetweenActions(ctx)
}

// SendResult says what SendMessage did with a recruiter message.
type SendResult string

// The outcomes of SendMessage.
const (
	SendSent        SendResult = "sent"
	SendNoRecipient SendResult = "no_recipient" // the hirer changed, or only InMail or Premium is offered
	SendBlocked     SendResult = "blocked"      // a restriction or CAPTCHA: stop sending
)

// HiringTeam reads the job page's hiring team: the hirer a free Message button
// is offered for, ok false when there is none.
//
// Not implemented until the hiring-team fixtures are captured (see testdata/fixtures/README.md).
func (s *Session) HiringTeam(ctx context.Context) (messages.Recipient, bool, error) {
	return messages.Recipient{}, false, nil
}

// SendMessage sends text to the hirer of the job jobID.
//
// Not implemented until the hiring-team fixtures are captured (see testdata/fixtures/README.md).
func (s *Session) SendMessage(ctx context.Context, to messages.Recipient, jobID, text string) (SendResult, error) {
	return SendNoRecipient, nil
}
