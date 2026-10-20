package apply

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
)

// node is a tiny fake DOM element: children are looked up by the exact XPath
// the code uses, so a test states precisely which selectors it expects.
type node struct {
	text     string
	attrs    map[string]string
	props    map[string]string
	children map[string][]*node
	clicks   int
	typed    strings.Builder
	cleared  bool
	selected string
	files    []string
	visible  bool
	onClick  func()
	clickErr error // what Click returns after onClick, as a button that never became clickable
	// failElements makes Elements fail, like a stale DOM after the page changed.
	failElements bool
}

func el(text string) *node {
	return &node{text: text, attrs: map[string]string{}, props: map[string]string{}, children: map[string][]*node{}, visible: true}
}

func (n *node) with(xpath string, kids ...*node) *node {
	n.children[xpath] = append(n.children[xpath], kids...)
	return n
}
func (n *node) attr(k, v string) *node { n.attrs[k] = v; return n }
func (n *node) prop(k, v string) *node { n.props[k] = v; return n }

func (n *node) Text() (string, error)                { return n.text, nil }
func (n *node) Attr(name string) (string, bool)      { v, ok := n.attrs[name]; return v, ok }
func (n *node) Property(name string) (string, error) { return n.props[name], nil }
func (n *node) Click() error {
	n.clicks++
	if n.onClick != nil {
		n.onClick()
	}
	return n.clickErr
}
func (n *node) ClickAtCentre() error                             { return n.Click() }
func (n *node) Input(text string) error                          { n.typed.WriteString(text); return nil }
func (n *node) Clear() error                                     { n.cleared = true; n.typed.Reset(); return nil }
func (n *node) Focus() error                                     { return nil }
func (n *node) ScrollIntoView() error                            { return nil }
func (n *node) Visible() bool                                    { return n.visible }
func (n *node) SelectOption(text string) error                   { n.selected = text; return nil }
func (n *node) SetFiles(paths []string) error                    { n.files = paths; return nil }
func (n *node) ShadowElements(string) ([]browser.Element, error) { return nil, nil }
func (n *node) ScriptClick() error                               { return n.Click() }
func (n *node) Element(xpath string) (browser.Element, error) {
	kids := n.children[xpath]
	if len(kids) == 0 {
		return nil, &browser.ErrNotFound{XPath: xpath}
	}
	return kids[0], nil
}
func (n *node) Elements(xpath string) ([]browser.Element, error) {
	if n.failElements {
		return nil, errors.New("stale element reference")
	}
	out := []browser.Element{}
	for _, k := range n.children[xpath] {
		out = append(out, k)
	}
	return out, nil
}

// page is a fake browser.Page whose document is one root node.
type page struct {
	root    *node
	pressed []string
	shots   []string
}

func (p *page) Navigate(context.Context, string) error { return nil }
func (p *page) WaitElement(ctx context.Context, xpath string, _ time.Duration) (browser.Element, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return p.root.Element(xpath)
}
func (p *page) URL() (string, error)                             { return "https://www.linkedin.com/jobs/view/1", nil }
func (p *page) Element(xpath string) (browser.Element, error)    { return p.root.Element(xpath) }
func (p *page) Elements(xpath string) ([]browser.Element, error) { return p.root.Elements(xpath) }
func (p *page) Screenshot(path string) error                     { p.shots = append(p.shots, path); return nil }
func (p *page) Press(key string) error                           { p.pressed = append(p.pressed, key); return nil }
func (p *page) Close() error                                     { return nil }
