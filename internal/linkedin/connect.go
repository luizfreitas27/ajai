package linkedin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/messages"
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
	if offered {
		return s.invite(ctx, btn)
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
	if s.exists(MenuPendingXPath) {
		_ = s.Page.Press("Escape")
		return ConnectAlready, nil
	}
	item, ok := s.first(MenuConnectXPath)
	if !ok {
		_ = s.Page.Press("Escape")
		return ConnectUnavailable, nil
	}
	return s.invite(ctx, item)
}

// invite clicks btn, a Connect control, and follows the invite to its outcome
// (see Connect). Nothing conclusive within inviteWait is an error, and any
// dialog left open is closed first.
func (s *Session) invite(ctx context.Context, btn browser.Element) (ConnectResult, error) {
	if err := btn.ScriptClick(); err != nil {
		return "", err
	}
	var (
		result   ConnectResult
		dialog   browser.Element
		sentNote bool
	)
	err := poll(ctx, inviteWait, "the invite did not show as pending", func() bool {
		if s.exists(TopCardPendingXPath) || s.exists(MenuPendingXPath) {
			result = ConnectSent
			return true
		}
		if d, ok := s.first(InviteEmailDialogXPath); ok {
			result, dialog = ConnectUnavailable, d
			return true
		}
		if d, ok := s.first(InviteLimitDialogXPath); ok {
			result, dialog = ConnectLimited, d
			return true
		}
		if b, ok := s.first(InviteSendWithoutNoteXPath); ok && !sentNote {
			sentNote = true
			_ = b.ScriptClick()
		}
		return false
	})
	if err != nil {
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

// closeInviteDialogs dismisses every dialog on the page that can be, best effort.
func (s *Session) closeInviteDialogs() {
	dialogs, _ := s.Page.Elements(InviteDialogXPath)
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
