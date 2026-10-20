package messages

import (
	"strings"
	"unicode"
)

// Recipient is the hirer a message is written to: the name as the job page
// shows it (may be empty) and the profile URL it is keyed by.
type Recipient struct {
	Name string
	URL  string
}

// Job is the part of a job a template can name.
type Job struct {
	Title   string
	Company string
}

// Render fills the placeholders of a market's message template: {first_name}
// (the first word of the recipient's name, capitalised), {title} and {company}.
// With no name, the greeting loses its name and keeps its punctuation ("Hi, I...").
// The result is trimmed; a blank template renders as "" and nothing is queued.
// Config validation rejects every other placeholder, so Render does not look for them.
func Render(template string, r Recipient, job Job) string {
	first := ""
	if fields := strings.Fields(r.Name); len(fields) > 0 {
		first = capitalise(fields[0])
	}
	text := template
	if first != "" {
		text = strings.ReplaceAll(text, "{first_name}", first)
	} else {
		// Only the greeting's name goes: "Hi {first_name}, I" becomes "Hi, I", and
		// a name at the very start takes its comma with it. Any other comma stays.
		text = strings.ReplaceAll(text, " {first_name},", ",")
		text = strings.TrimLeft(strings.TrimPrefix(text, "{first_name},"), " ")
		text = strings.ReplaceAll(text, "{first_name}", "")
	}
	text = strings.NewReplacer("{title}", job.Title, "{company}", job.Company).Replace(text)
	return strings.TrimSpace(text)
}

// capitalise lower-cases a name and upper-cases the first letter of each part
// split on hyphens, so "aDRIana" becomes "Adriana" and "ana-maria" "Ana-Maria".
// Apostrophes are not word breaks: "o'neil" stays "O'neil".
func capitalise(name string) string {
	parts := strings.Split(strings.ToLower(name), "-")
	for i, part := range parts {
		runes := []rune(part)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			parts[i] = string(runes)
		}
	}
	return strings.Join(parts, "-")
}
