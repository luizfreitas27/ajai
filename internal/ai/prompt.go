package ai

import (
	"fmt"
	"strings"
)

// answerPrompt is the Python ai_answer_prompt, verbatim: applicant information,
// then the question.
const answerPrompt = `
You are helping a job seeker fill in a job-application form. Answer the single question below the way the applicant would, in the first person, using the applicant's information whenever it is relevant.

Formatting rules:
- If the question asks for a number, a count, or years/months of experience, reply with just the number (for example: 3).
- If it is a yes/no question, reply with exactly "Yes" or "No".
- If it asks for a short answer, reply in a single sentence.
- If it asks for a longer or free-text answer, reply with a natural, well-structured response of at most 350 characters.
- Never repeat the question, never add labels, never add commentary. Return only the answer itself.

Applicant information:
%s

Question:
%s
`

// BuildPrompt assembles the prompt for one question: the applicant, the
// question, the job description when known, and the options the answer must
// be one of.
func BuildPrompt(profile, question, jobDescription string, options []string) string {
	if strings.TrimSpace(profile) == "" {
		profile = "N/A"
	}
	prompt := fmt.Sprintf(answerPrompt, profile, question)
	if jd := strings.TrimSpace(jobDescription); jd != "" && jd != "Unknown" {
		prompt += "\n\nJob description:\n" + jd
	}
	if len(options) > 0 {
		prompt += "\n\nAnswer with exactly one of these options:\n"
		for i, o := range options {
			if i > 0 {
				prompt += "\n"
			}
			prompt += "- " + o
		}
	}
	return prompt
}

// SelectOption returns the option the model named: exact text, tolerating only
// case and surrounding quotes or whitespace. No substring guessing: "Yes,
// absolutely" is not an answer to a form offering "Yes" and "No", and a model
// that paraphrases must not be forced into an option it did not choose.
// "" when it named none of them.
func SelectOption(raw string, options []string) string {
	raw = strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), `"'`))
	for _, o := range options {
		if raw == o {
			return o
		}
	}
	for _, o := range options {
		if strings.EqualFold(raw, o) {
			return o
		}
	}
	return ""
}
