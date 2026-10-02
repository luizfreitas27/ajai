// Package browser is the only package that talks to Chrome. It exposes a small
// interface the rest of aja works against (so the run loop and the LinkedIn
// session are testable with fakes) and one implementation on go-rod attached
// to the user's own Chrome profile in a visible window (ADR 0001).
package browser

import (
	"context"
	"time"
)

// Element is one DOM node.
type Element interface {
	Text() (string, error)
	// Attr returns an attribute and whether it is present.
	Attr(name string) (string, bool)
	// Property reads a live DOM property such as "value" or "checked" as text ("true"/"false" for booleans).
	Property(name string) (string, error)
	Click() error
	// Input inserts text at the caret; callers type a character at a time for a human rhythm.
	Input(text string) error
	// Clear selects and deletes the control's text.
	Clear() error
	Focus() error
	ScrollIntoView() error
	Visible() bool
	Element(xpath string) (Element, error)
	Elements(xpath string) ([]Element, error)
	// SelectOption picks an <option> by its visible text.
	SelectOption(text string) error
	// SetFiles sets the files of an <input type="file">.
	SetFiles(paths []string) error
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
	// Press sends a keyboard key to the page, e.g. "Escape", "Enter", "ArrowDown", "Tab".
	Press(key string) error
	Close() error
}

// Browser is the Chrome instance.
type Browser interface {
	// Main is the tab aja works in.
	Main() Page
	// Pages lists open tabs, main first.
	Pages() ([]Page, error)
	Close() error
}

// ErrNotFound is returned when an element does not exist.
type ErrNotFound struct{ XPath string }

func (e *ErrNotFound) Error() string { return "no element matches " + e.XPath }
