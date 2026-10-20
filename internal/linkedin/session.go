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
	CloseTabs bool   // close the tab an external application opened (settings.close_tabs)
	jobBase   string // where job pages are, JobURL when blank; tests serve their own
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

// TypeHuman types text one character at a time with the pacer's delays. A
// line break is Shift+Enter, which breaks the line in a message box where Enter
// may send.
func (s *Session) TypeHuman(ctx context.Context, el browser.Element, text string) error {
	for _, r := range text {
		var err error
		switch r {
		case '\r':
			continue
		case '\n':
			err = s.Page.Press("Shift+Enter")
		default:
			err = el.Input(string(r))
		}
		if err != nil {
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
	SendNoRecipient SendResult = "no_recipient" // the hirer changed, or only Premium or a paid InMail is offered
	SendBlocked     SendResult = "blocked"      // a restriction or CAPTCHA: stop sending
)

// HiringTeam reads the job page's hiring team: the first hirer whose card holds
// both a profile link and a free Message control (HirerCardXPath). It clicks
// nothing. ok is false, with no error, when the section is missing or empty or
// no card has a Message control.
func (s *Session) HiringTeam(ctx context.Context) (messages.Recipient, bool, error) {
	to, _, ok, err := s.hirer()
	return to, ok, err
}

// hirer is HiringTeam with the hirer's card.
func (s *Session) hirer() (messages.Recipient, browser.Element, bool, error) {
	section, err := s.Page.Element(HiringTeamSectionXPath)
	var missing *browser.ErrNotFound
	if errors.As(err, &missing) {
		return messages.Recipient{}, nil, false, nil
	}
	if err != nil {
		return messages.Recipient{}, nil, false, err
	}
	cards, err := section.Elements(HirerCardXPath)
	if err != nil {
		return messages.Recipient{}, nil, false, err
	}
	for _, card := range cards {
		link, err := card.Element(HirerProfileLinkXPath)
		if err != nil {
			return messages.Recipient{}, nil, false, err
		}
		href, _ := link.Attr("href")
		profile, ok := NormaliseProfileURL(href)
		if !ok {
			continue
		}
		var name string
		if el, err := link.Element(HirerNameXPath); err == nil {
			name, _ = el.Text()
		}
		return messages.Recipient{Name: strings.TrimSpace(name), URL: profile}, card, true, nil
	}
	return messages.Recipient{}, nil, false, nil
}

// NormaliseProfileURL returns the canonical profile URL of a LinkedIn member
// link, "https://www.linkedin.com/in/<slug>", for an absolute or relative /in/
// href. The query, the fragment and a trailing slash go. ok is false for
// anything that is not a member profile on www.linkedin.com over https.
func NormaliseProfileURL(href string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", false
	}
	if (u.Scheme != "" && u.Scheme != "https") || (u.Host != "" && u.Host != "www.linkedin.com") {
		return "", false
	}
	slug, ok := strings.CutPrefix(strings.TrimSuffix(u.Path, "/"), "/in/")
	if !ok || slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return "https://www.linkedin.com/in/" + slug, true
}

// The bounds of SendMessage's waits; vars so the tests can shorten them.
var (
	composeWait = 20 * time.Second
	sentWait    = 20 * time.Second
)

// SendMessage sends text to the hirer of the job jobID, only for free: a
// connection's message, or LinkedIn's free message to an open profile. It opens
// the job page and reads the hiring team again; a hirer other than to, a Premium
// upsell or an InMail that would use a paid credit is SendNoRecipient, with
// nothing sent. The optional subject is left empty. A checkpoint page is
// SendBlocked. An error means the message may not have gone out: the box did
// not hold exactly text (nothing sent), or the send was clicked and never seen
// through.
func (s *Session) SendMessage(ctx context.Context, to messages.Recipient, jobID, text string) (SendResult, error) {
	base := s.jobBase
	if base == "" {
		base = JobURL
	}
	if err := s.Page.Navigate(ctx, base+jobID+"/"); err != nil {
		return "", err
	}
	if s.blocked() {
		return SendBlocked, nil
	}
	if _, err := s.Page.WaitElement(ctx, HiringTeamSectionXPath, 15*time.Second); ctx.Err() != nil {
		return "", ctx.Err()
	} else if err != nil {
		s.Logger.Info("no hiring team on the job page", "job", jobID)
	}
	found, card, ok, err := s.hirer()
	if err != nil {
		return "", err
	}
	if !ok || found.URL != to.URL {
		return SendNoRecipient, nil
	}
	link, err := card.Element(HirerMessageXPath)
	if err != nil {
		return "", err
	}
	// Rod's click on this link hangs (the overlay opens in place); the page's own click does not.
	if err := link.ScriptClick(); err != nil {
		return "", err
	}
	host, bubble, err := s.waitCompose(ctx, to)
	if err != nil {
		return "", err
	}
	if bubble == nil { // the Premium upsell, dismissed, or no compose to the hirer opened
		return SendNoRecipient, nil
	}
	if !freeCompose(bubble) {
		closeBubble(bubble)
		return SendNoRecipient, nil
	}
	box, err := bubble.Element(MessageBoxXPath)
	if err != nil {
		return "", err
	}
	if err := s.TypeHuman(ctx, box, text); err != nil {
		return "", err
	}
	if err := s.Pacer.BetweenActions(ctx); err != nil {
		return "", err
	}
	if typed, _ := box.Text(); squash(typed) != squash(text) {
		closeBubble(bubble)
		return "", errors.New("the message box does not hold the approved text; nothing was sent")
	}
	if !freeCompose(bubble) { // LinkedIn may have shown the credits once typing began
		closeBubble(bubble)
		return SendNoRecipient, nil
	}
	send, err := bubble.Element(MessageSendButtonXPath)
	if err != nil {
		return "", err
	}
	if _, disabled := send.Attr("disabled"); disabled {
		closeBubble(bubble)
		return "", errors.New("the send button stayed disabled; nothing was sent")
	}
	if err := send.ScriptClick(); err != nil {
		return "", fmt.Errorf("clicking send: %w", err)
	}
	sent, err := s.waitSent(ctx, host, box, text)
	if err != nil {
		return "", err
	}
	if sent == SendSent {
		closeBubble(bubble)
	}
	return sent, nil
}

// blocked: LinkedIn sent the tab to a checkpoint (a CAPTCHA or a restriction).
// Read from the URL, as login is; no such page is captured yet.
func (s *Session) blocked() bool {
	u, err := s.Page.URL()
	return err == nil && strings.Contains(u, "/checkpoint/")
}

// waitCompose waits, up to composeWait, for the overlay the Message link opens:
// the compose bubble to to, or the Premium upsell, which it dismisses and
// reports as a nil bubble. A compose to another member, or one without its
// profile link, is not ours and is left alone; when none turns up the result is
// a nil bubble too. host is the overlay's shadow host.
func (s *Session) waitCompose(ctx context.Context, to messages.Recipient) (host, bubble browser.Element, err error) {
	err = poll(ctx, composeWait, "the compose box did not open", func() bool {
		if host, err = s.Page.Element(MessageOutletXPath); err != nil {
			return false
		}
		if upsell, _ := host.ShadowElements(UpsellModalXPath); len(upsell) > 0 {
			if dismiss, err := upsell[0].Element(UpsellDismissXPath); err == nil {
				_ = dismiss.ScriptClick()
			}
			return true
		}
		bubbles, _ := host.ShadowElements(ComposeBubbleXPath)
		for _, b := range bubbles {
			if _, err := b.Element(MessageBoxXPath); err != nil {
				continue
			}
			link, err := b.Element(ComposeRecipientXPath)
			if err != nil {
				continue
			}
			href, _ := link.Attr("href")
			if u, _ := NormaliseProfileURL(href); u != to.URL {
				continue
			}
			bubble = b
			return true
		}
		return false
	})
	if err != nil && ctx.Err() == nil { // never opened: no recipient to send to
		return host, nil, nil
	}
	return host, bubble, err
}

// freeCompose: the compose costs nothing. An InMail shows what it costs
// (InMailCreditsXPath) and is free only when that says "Free message"; a compose
// with a subject and no such line is an InMail too, and is not free. A
// connection's compose has neither.
func freeCompose(bubble browser.Element) bool {
	if credits, err := bubble.Element(InMailCreditsXPath); err == nil {
		t, _ := credits.Text()
		return strings.Contains(t, FreeMessageText)
	}
	_, err := bubble.Element(MessageSubjectXPath)
	return err != nil
}

// waitSent waits, up to sentWait, for LinkedIn to show the message sent. The
// heuristic: the box empties (LinkedIn clears it once the message is accepted)
// or the message shows in the conversation (SentMessageBodyXPath, from the
// overlay's root, as the bubble may be drawn anew as a conversation). Neither
// is an error: the send was clicked, so the message must never be tried again.
func (s *Session) waitSent(ctx context.Context, host, box browser.Element, text string) (SendResult, error) {
	var result SendResult
	err := poll(ctx, sentWait, "the send was clicked but the message never showed as sent", func() bool {
		if s.blocked() {
			result = SendBlocked
			return true
		}
		if t, err := box.Text(); err == nil && strings.TrimSpace(t) == "" {
			result = SendSent
			return true
		}
		bodies, _ := host.ShadowElements(SentMessageBodyXPath)
		for _, b := range bodies {
			if t, _ := b.Text(); squash(t) == squash(text) {
				result = SendSent
				return true
			}
		}
		return false
	})
	return result, err
}

// closeBubble closes a compose or conversation bubble; a failure only leaves it open.
func closeBubble(bubble browser.Element) {
	if c, err := bubble.Element(BubbleCloseButtonXPath); err == nil {
		_ = c.ScriptClick()
	}
}

// squash trims text and collapses its runs of white space, as a contenteditable
// box gives back line breaks its own way.
func squash(text string) string { return strings.Join(strings.Fields(text), " ") }

// poll calls done every 250 ms until it reports true, up to wait; past that it
// is an error saying what never happened.
func poll(ctx context.Context, wait time.Duration, never string, done func() bool) error {
	deadline := time.Now().Add(wait)
	for !done() {
		if time.Now().After(deadline) {
			return errors.New(never)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}
