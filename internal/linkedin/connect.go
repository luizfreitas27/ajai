package linkedin

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/paths"
)

// ConnectResult is what Connect did.
type ConnectResult string

// The outcomes of Connect.
const (
	ConnectSent        ConnectResult = "sent"        // the invite went out, without a note
	ConnectAlready     ConnectResult = "already"     // connected already, or an invite is pending
	ConnectUnavailable ConnectResult = "unavailable" // no Connect offered, an email asked for, or another profile
	ConnectLimited     ConnectResult = "limited"     // LinkedIn's weekly invitation limit
	ConnectBlocked     ConnectResult = "blocked"     // a /checkpoint/ page
)

// The bounds of Connect's waits; vars so the tests can shorten them.
var (
	profileWait = 15 * time.Second // the profile's actions row
	menuWait    = 5 * time.Second  // the More menu
	inviteWait  = 15 * time.Second // the invite's outcome
)

// linkedInProfileBase is where every member profile is.
const linkedInProfileBase = "https://www.linkedin.com/in/"

// Connect sends a connection invite, without a note, to the hirer to. The
// profile is opened and its Connect control found in the top card or in the
// More menu. A note dialog is answered with "Send without a note" and never
// with "Add a note"; an email or weekly-limit dialog is dismissed. The invite
// is sent once the control reads Pending. A /checkpoint/ page is ConnectBlocked,
// and a profile other than to's is ConnectUnavailable, with nothing clicked.
func (s *Session) Connect(ctx context.Context, to messages.Recipient) (ConnectResult, error) {
	want, ok := NormaliseProfileURL(to.URL)
	if !ok {
		return "", fmt.Errorf("connect: %q is not a member profile", to.URL)
	}
	if s.blocked() {
		return ConnectBlocked, nil
	}
	if err := s.Page.Navigate(ctx, s.profileAt(want)); err != nil {
		return "", err
	}
	if err := s.Pacer.BetweenActions(ctx); err != nil {
		return "", err
	}
	if s.blocked() {
		return ConnectBlocked, nil
	}
	u, err := s.Page.URL()
	if err != nil {
		return "", err
	}
	if got, _ := NormaliseProfileURL(s.profileHome(u)); got != want {
		return ConnectUnavailable, nil
	}
	if err := poll(ctx, profileWait, "the profile's actions did not show", func() bool {
		return s.exists(ProfileMoreXPath)
	}); err != nil {
		return "", err
	}
	// A Connect in the top card means not connected, whatever the degree text
	// says: a 2nd-degree page holds a hidden "· 1st" too.
	btn, offered := s.first(TopCardOffersConnectXPath)
	if !offered && (s.exists(TopCardDegreeXPath) || s.exists(TopCardPendingXPath)) {
		return ConnectAlready, nil
	}
	slug := profileSlug(want)
	if offered {
		return s.invite(ctx, btn, slug)
	}
	more, err := s.Page.Element(ProfileMoreXPath)
	if err != nil {
		return "", err
	}
	if err := more.ScriptClick(); err != nil {
		return "", err
	}
	if err := poll(ctx, menuWait, "the More menu did not open", func() bool {
		return s.exists(MenuXPath)
	}); err != nil {
		return "", err
	}
	if _, ok := s.findAny(MenuPendingXPath); ok {
		_ = s.Page.Press("Escape")
		return ConnectAlready, nil
	}
	item, ok := s.first(MenuConnectXPath)
	if !ok {
		s.diagnose(slug)
		_ = s.Page.Press("Escape")
		return ConnectUnavailable, nil
	}
	return s.invite(ctx, item, slug)
}

// invite clicks btn, a Connect control, and follows the invite to its outcome
// (see Connect). The dialogs and the Pending label are looked for in the
// document and in the open shadow root of #interop-outlet (findAny). Nothing
// conclusive within inviteWait is an error: the page is kept for diagnosis
// (diagnose), then any dialog left open is closed.
func (s *Session) invite(ctx context.Context, btn browser.Element, slug string) (ConnectResult, error) {
	if err := btn.ScriptClick(); err != nil {
		return "", err
	}
	var (
		result   ConnectResult
		dialog   browser.Element
		sentNote bool
	)
	err := poll(ctx, inviteWait, "the invite did not show as pending", func() bool {
		if s.exists(TopCardPendingXPath) {
			result = ConnectSent
			return true
		}
		if _, ok := s.findAny(MenuPendingXPath); ok {
			result = ConnectSent
			return true
		}
		if d, ok := s.findAny(InviteEmailDialogXPath); ok {
			result, dialog = ConnectUnavailable, d
			return true
		}
		if d, ok := s.findAny(InviteLimitDialogXPath); ok {
			result, dialog = ConnectLimited, d
			return true
		}
		if b, ok := s.findAny(InviteSendWithoutNoteXPath); ok && !sentNote {
			sentNote = true
			_ = b.ScriptClick()
		}
		return false
	})
	if err != nil {
		s.diagnose(slug)
		s.closeInviteDialogs()
		return "", err
	}
	if dialog != nil {
		dismiss, err := dialog.Element(InviteDismissXPath)
		if err != nil {
			return "", fmt.Errorf("connect: no way to dismiss the dialog: %w", err)
		}
		if err := dismiss.ScriptClick(); err != nil {
			return "", err
		}
	}
	return result, nil
}

// closeInviteDialogs dismisses every dialog on the page that can be, in the
// document and in the outlet's shadow root, best effort.
func (s *Session) closeInviteDialogs() {
	dialogs, _ := s.Page.Elements(InviteDialogXPath)
	if host, ok := s.outlet(); ok {
		inside, _ := host.ShadowElements(InviteDialogXPath)
		dialogs = append(dialogs, inside...)
	}
	for _, d := range dialogs {
		if dismiss, err := d.Element(InviteDismissXPath); err == nil {
			_ = dismiss.ScriptClick()
		}
	}
}

// first is the first element matching xpath, if there is one.
func (s *Session) first(xpath string) (browser.Element, bool) {
	els, _ := s.Page.Elements(xpath)
	if len(els) == 0 {
		return nil, false
	}
	return els[0], true
}

// outlet is the shadow host of LinkedIn's overlays (MessageOutletXPath), if the
// page has one.
func (s *Session) outlet() (browser.Element, bool) {
	els, _ := s.Page.Elements(MessageOutletXPath)
	if len(els) == 0 {
		return nil, false
	}
	return els[0], true
}

// findAny is the first element matching xpath in the document or, failing that,
// in the open shadow root of #interop-outlet, where LinkedIn draws the invite
// dialogs. The xpath is absolute, as ShadowElements needs.
func (s *Session) findAny(xpath string) (browser.Element, bool) {
	if el, ok := s.first(xpath); ok {
		return el, true
	}
	host, ok := s.outlet()
	if !ok {
		return nil, false
	}
	els, err := host.ShadowElements(xpath)
	if err != nil || len(els) == 0 {
		return nil, false
	}
	return els[0], true
}

// diagnose keeps a Connect that ended with nothing it can name for diagnosis:
// a screenshot and the HTML of the page, as invite-<slug>-<unix>.png and .html
// in ScreenshotDir (nothing when it is unset). The HTML is the document, then
// the children of the outlet's shadow root after a marker comment. Best effort;
// a failure is logged, never returned.
func (s *Session) diagnose(slug string) {
	if s.ScreenshotDir == "" {
		return
	}
	if err := paths.EnsurePrivateDir(s.ScreenshotDir); err != nil {
		s.Logger.Warn("connect: no diagnostics directory", "error", err)
		return
	}
	base := filepath.Join(s.ScreenshotDir, fmt.Sprintf("invite-%s-%d", slug, time.Now().Unix()))
	shot, htmlPath := base+".png", base+".html"
	if err := s.Page.Screenshot(shot); err != nil {
		s.Logger.Warn("connect: screenshot failed", "error", err)
		shot = ""
	}
	if err := os.WriteFile(htmlPath, []byte(s.pageHTML()), paths.PrivateFileMode); err != nil {
		s.Logger.Warn("connect: HTML dump failed", "error", err)
		htmlPath = ""
	}
	s.Logger.Warn("connect: the invite did not show as pending; the page was kept", "screenshot", shot, "html", htmlPath)
}

// pageHTML is the document's outerHTML, then the outlet's shadow root's
// top-level elements after a marker comment ("" for either part that cannot
// be read). Text nodes at the top of the shadow root are not included.
func (s *Session) pageHTML() string {
	var b strings.Builder
	if html, err := s.Page.Element("//html"); err == nil {
		if doc, err := html.Property("outerHTML"); err == nil {
			b.WriteString(doc)
		}
	}
	b.WriteString("\n<!-- #interop-outlet shadowRoot -->\n")
	if host, ok := s.outlet(); ok {
		if kids, err := host.ShadowElements("/*"); err == nil {
			for _, k := range kids {
				if h, err := k.Property("outerHTML"); err == nil {
					b.WriteString(h)
					b.WriteString("\n")
				}
			}
		}
	}
	return b.String()
}

// profileSlug is the member's profile slug, the last path segment of a
// normalised profile URL, with anything but a-z, 0-9 and "-" turned into "-".
func profileSlug(profile string) string {
	slug := regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(strings.ToLower(path.Base(profile)), "-")
	return strings.Trim(slug, "-")
}

// exists is whether anything matches xpath on the page.
func (s *Session) exists(xpath string) bool {
	_, ok := s.first(xpath)
	return ok
}

// profileAt is the page a member profile URL opens at: the same URL, unless
// the tests serve profiles from profileBase.
func (s *Session) profileAt(u string) string {
	if s.profileBase == "" {
		return u
	}
	return strings.Replace(u, linkedInProfileBase, s.profileBase, 1)
}

// profileHome is the LinkedIn URL of a page profileAt opened.
func (s *Session) profileHome(u string) string {
	if s.profileBase == "" {
		return u
	}
	return strings.Replace(u, s.profileBase, linkedInProfileBase, 1)
}
