// Package apply drives the Easy Apply modal: it reads every control on a page
// into questions.Question values, asks the Answerer what to do, applies the
// decisions, uploads the resume, walks Next/Review/Submit and discards when a
// dry run reaches Review or questions stay unanswered. Ported from the Python
// answer_questions / apply_to_jobs modal section.
package apply

import (
	"strings"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/questions"
)

// Control is one form control with the DOM handles needed to act on it.
type Control struct {
	Question questions.Question
	// Element is the <select>, <input> or <textarea>; for a radio group, the fieldset.
	Element browser.Element
	// RadioInputs are the group's <input type="radio"> (or checkbox, for a
	// multipleChoice question) in option order.
	RadioInputs []browser.Element
	// RadioLabels are each option's <label> in option order, nil where there is none.
	RadioLabels []browser.Element
	// BoxLabel is the <label> of a single checkbox, nil when there is none.
	BoxLabel browser.Element
	// FollowCompany marks LinkedIn's own "Follow <company>" box, which the applier
	// sets from the follow_companies setting instead of answering.
	FollowCompany bool
	// Refusal is the message LinkedIn shows on the question when it refused its
	// answer (after Next or Review), "" when it shows none.
	Refusal string
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
			if e, err := block.Element(linkedin.InlineErrorXPath); err == nil {
				t, _ := e.Text()
				c.Refusal = strings.Join(strings.Fields(t), " ")
			}
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
		inputs, _ := fieldset.Elements(linkedin.RadioInputsXPath)
		return choice(fieldset, titleOf(fieldset, linkedin.RadioTitleXPath, true), inputs), true
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
		if boxes, _ := block.Elements(linkedin.CheckboxXPath); len(boxes) > 1 {
			// Several boxes in one question are a multipleChoice group: one answer, picked by option.
			return choice(block, titleOf(block, linkedin.RadioTitleXPath, true), boxes), true
		}
		q := questions.Question{Kind: questions.Checkbox, GroupTitle: questions.UnknownTitle, Label: questions.UnknownTitle}
		if title, err := block.Element(linkedin.HiddenTitleXPath); err == nil {
			if t, _ := title.Text(); strings.TrimSpace(t) != "" {
				q.GroupTitle = strings.TrimSpace(t)
			}
		}
		if visible, err := block.Element(linkedin.LabelForXPath); err == nil {
			c.BoxLabel = visible
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

// choice reads a group of inputs (a radio group, or the boxes of a multipleChoice
// question) as a choice question. An option's text is its label: the one with the
// input's for id, else the label wrapping the input, else the input's value.
func choice(scope browser.Element, title string, inputs []browser.Element) Control {
	c := Control{Question: questions.Question{Kind: questions.Radio, Label: title}, Element: scope, RadioInputs: inputs}
	for _, in := range inputs {
		var label browser.Element
		if id, ok := in.Attr("id"); ok {
			if l, err := scope.Element(linkedin.OptionLabelXPath(id)); err == nil {
				label = l
			} else if l, err := scope.Element(linkedin.OptionWrapLabelXPath(id)); err == nil {
				label = l
			}
		}
		text := ""
		if label != nil {
			text, _ = label.Text()
		}
		if strings.TrimSpace(text) == "" {
			text, _ = in.Attr("value")
		}
		text = strings.TrimSpace(text)
		c.Question.Options = append(c.Question.Options, text)
		c.RadioLabels = append(c.RadioLabels, label)
		if checked, _ := in.Property("checked"); checked == "true" {
			c.Question.Current = text
		}
	}
	return c
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
