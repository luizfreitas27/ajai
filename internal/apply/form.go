// Package apply drives the Easy Apply modal: it reads every control on a page
// into questions.Question values, asks the Answerer what to do, applies the
// decisions, uploads the resume, walks Next/Review/Submit and discards when a
// dry run reaches Review or questions stay unanswered. Ported from the Python
// answer_questions / apply_to_jobs modal section.
package apply

import (
	"strings"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/questions"
)

// Control is one form control with the DOM handles needed to act on it.
type Control struct {
	Question questions.Question
	// Element is the <select>, <input> or <textarea>; for a radio group, the fieldset.
	Element browser.Element
	// RadioInputs are the group's <input type="radio"> in option order.
	RadioInputs []browser.Element
	// FollowCompany marks LinkedIn's own "Follow <company>" box, which the applier
	// sets from the follow_companies setting instead of answering.
	FollowCompany bool
}

// ReadForm reads every question block of the modal's current page.
func ReadForm(modal browser.Element) ([]Control, error) {
	blocks, err := modal.Elements(linkedin.FormQuestionXPath)
	if err != nil {
		return nil, err
	}
	var out []Control
	for _, block := range blocks {
		if c, ok := readBlock(block); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func readBlock(block browser.Element) (Control, bool) {
	if sel, err := block.Element(linkedin.SelectXPath); err == nil {
		q := questions.Question{Kind: questions.Select, Label: titleOf(block, linkedin.SelectTitleXPath, false)}
		if opts, err := sel.Elements(linkedin.SelectOptionsXPath); err == nil {
			for _, o := range opts {
				t, _ := o.Text()
				q.Options = append(q.Options, strings.TrimSpace(t))
				if selected, _ := o.Property("selected"); selected == "true" {
					q.Current = strings.TrimSpace(t)
				}
			}
		}
		return Control{Question: q, Element: sel}, true
	}
	if fieldset, err := block.Element(linkedin.RadioFieldsetXPath); err == nil {
		q := questions.Question{Kind: questions.Radio, Label: titleOf(fieldset, linkedin.RadioTitleXPath, true)}
		inputs, _ := fieldset.Elements(linkedin.RadioInputsXPath)
		for _, in := range inputs {
			label := ""
			if id, ok := in.Attr("id"); ok {
				if l, err := fieldset.Element(linkedin.OptionLabelXPath(id)); err == nil {
					label, _ = l.Text()
				}
			}
			if label == "" {
				label, _ = in.Attr("value")
			}
			label = strings.TrimSpace(label)
			q.Options = append(q.Options, label)
			if checked, _ := in.Property("checked"); checked == "true" {
				q.Current = label
			}
		}
		return Control{Question: q, Element: fieldset, RadioInputs: inputs}, true
	}
	if text, err := block.Element(linkedin.TextInputXPath); err == nil {
		value, _ := text.Property("value")
		return Control{Question: questions.Question{Kind: questions.Text, Label: titleOf(block, linkedin.LabelForXPath, true), Current: strings.TrimSpace(value)}, Element: text}, true
	}
	if area, err := block.Element(linkedin.TextareaXPath); err == nil {
		value, _ := area.Property("value")
		return Control{Question: questions.Question{Kind: questions.Textarea, Label: titleOf(block, linkedin.LabelForXPath, false), Current: strings.TrimSpace(value)}, Element: area}, true
	}
	if box, err := block.Element(linkedin.CheckboxXPath); err == nil {
		c := Control{Element: box}
		if id, _ := box.Attr("id"); id == linkedin.FollowCompanyCheckbox {
			c.FollowCompany = true
			return c, true
		}
		q := questions.Question{Kind: questions.Checkbox, GroupTitle: questions.UnknownTitle, Label: questions.UnknownTitle}
		if title, err := block.Element(linkedin.HiddenTitleXPath); err == nil {
			if t, _ := title.Text(); strings.TrimSpace(t) != "" {
				q.GroupTitle = strings.TrimSpace(t)
			}
		}
		if visible, err := block.Element(linkedin.LabelForXPath); err == nil {
			if t, _ := visible.Text(); strings.TrimSpace(t) != "" {
				q.Label = strings.TrimSpace(t)
			}
		}
		if checked, _ := box.Property("checked"); checked == "true" {
			q.Current = "checked"
		}
		c.Question = q
		return c, true
	}
	return Control{}, false
}

// titleOf reads a control's visible title, preferring the visually-hidden copy
// when preferHidden is set (LinkedIn puts the full text there), else "Unknown".
func titleOf(scope browser.Element, xpath string, preferHidden bool) string {
	label, err := scope.Element(xpath)
	if err != nil {
		return questions.UnknownTitle
	}
	if preferHidden {
		if hidden, err := label.Element(linkedin.VisuallyHiddenAnyXPath); err == nil {
			if t, _ := hidden.Text(); strings.TrimSpace(t) != "" {
				return strings.TrimSpace(t)
			}
		}
	}
	t, _ := label.Text()
	if strings.TrimSpace(t) == "" {
		return questions.UnknownTitle
	}
	return strings.TrimSpace(t)
}
