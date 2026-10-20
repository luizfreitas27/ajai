package messages

import "strings"

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
// (the first word of the recipient's name), {title} and {company}. With no name,
// the greeting loses its name and keeps its punctuation ("Hi, I..."). The result
// is trimmed; a blank template renders as "" and nothing is queued. Config
// validation rejects every other placeholder, so Render does not look for them.
func Render(template string, r Recipient, job Job) string {
	first := ""
	if fields := strings.Fields(r.Name); len(fields) > 0 {
		first = fields[0]
	}
	text := template
	if first != "" {
		text = strings.ReplaceAll(text, "{first_name}", first)
	} else {
		// "Hi {first_name}, I" becomes "Hi, I"; a name at the very start
		// leaves its comma to be stripped below.
		text = strings.ReplaceAll(text, " {first_name}", "")
		text = strings.ReplaceAll(text, "{first_name} ", "")
		text = strings.ReplaceAll(text, "{first_name}", "")
		text = strings.TrimLeft(text, ", ")
	}
	text = strings.NewReplacer("{title}", job.Title, "{company}", job.Company).Replace(text)
	return strings.TrimSpace(text)
}
