// Package browser is the only package that talks to Chrome. It exposes a small
// interface the rest of ajai works against (so the run loop and the LinkedIn
// session are testable with fakes) and one implementation on go-rod driving the
// installed Chrome in a visible window, on the ajai profile or the user's own (ADR 0001).
package browser

import (
	"context"
	"fmt"
	"time"
)

// Element is one DOM node.
type Element interface {
	Text() (string, error)
	// Attr returns an attribute and whether it is present.
	Attr(name string) (string, bool)
	// Property reads a live DOM property such as "value" or "checked" as text ("true"/"false" for booleans).
	Property(name string) (string, error)
	// Click waits a short while for the element to be shown, uncovered and
	// enabled, then clicks it; past that it gives up with a *NotReadyError.
	Click() error
	// ClickAtCentre clicks the mouse in the middle of the element, on whatever
	// the page draws there, as a person would: a control the page hides under
	// its own drawing (a switch's input with pointer-events none under a toggle)
	// is reached this way when Click refuses it. It refuses, without clicking,
	// an element that takes no room or one something unrelated is drawn over.
	ClickAtCentre() error
	// Input inserts text at the caret; callers type a character at a time for a human rhythm.
	// Like Click it waits a short while for the control to be shown and enabled.
	Input(text string) error
	// Clear selects and deletes the control's text, waiting as Input does.
	Clear() error
	// Focus waits a short while for the element to be shown, as Click does.
	Focus() error
	ScrollIntoView() error
	Visible() bool
	Element(xpath string) (Element, error)
	Elements(xpath string) ([]Element, error)
	// SelectOption picks an <option> by its visible text, waiting as Focus does.
	SelectOption(text string) error
	// SetFiles sets the files of an <input type="file">.
	SetFiles(paths []string) error
	// ShadowElements finds the elements matching xpath inside the element's open
	// shadow root, which a document-wide XPath never reaches. The xpath is
	// absolute ("//x"): it is evaluated from the root of the shadow tree. An
	// element without a shadow root is an error; an empty one matches nothing.
	ShadowElements(xpath string) ([]Element, error)
	// ScriptClick calls the element's click() in the page: no mouse, no wait for
	// it to be shown. For a control whose real click hangs, as LinkedIn's hirer
	// Message link does.
	ScriptClick() error
}

// Page is one tab.
type Page interface {
	Navigate(ctx context.Context, url string) error
	URL() (string, error)
	Element(xpath string) (Element, error)
	Elements(xpath string) ([]Element, error)
	// WaitElement waits up to timeout for a VISIBLE element matching xpath.
	WaitElement(ctx context.Context, xpath string, timeout time.Duration) (Element, error)
	Screenshot(path string) error
	// Press sends a keyboard key to the page, e.g. "Escape", "Enter", "ArrowDown", "Tab",
	// or "Shift+Enter".
	Press(key string) error
	Close() error
}

// Opener opens Chrome: Open in production, a fake in tests.
type Opener func(ctx context.Context, o Options) (Browser, error)

// Browser is the Chrome instance.
type Browser interface {
	// Main is the tab ajai works in.
	Main() Page
	// Pages lists open tabs, main first.
	Pages() ([]Page, error)
	Close() error
}

// ErrNotFound is returned when an element does not exist.
type ErrNotFound struct{ XPath string }

func (e *ErrNotFound) Error() string { return "no element matches " + e.XPath }

// NotReadyError is returned when an element could not be clicked, typed into or
// focused within the wait: the wait, and why it was not ready.
type NotReadyError struct {
	Wait   time.Duration
	Reason NotReadyReason
}

func (e *NotReadyError) Error() string { return fmt.Sprintf("gave up after %s: %s", e.Wait, e.Reason) }

// NotReadyReason is why an element was still not ready when the wait ran out.
type NotReadyReason string

const (
	// LeftThePage: the page drew the element anew, as LinkedIn redraws a button.
	LeftThePage    NotReadyReason = "it left the page"
	StayedDisabled NotReadyReason = "it stayed disabled"
	StayedHidden   NotReadyReason = "it stayed hidden"
	StayedCovered  NotReadyReason = "something stayed drawn over it"
)
