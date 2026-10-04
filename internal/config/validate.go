package config

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// FieldError is one validation problem, phrased for the user: the key, what is
// wrong, and the valid values when the set is closed.
type FieldError struct {
	Key     string
	Problem string
	Valid   []string
}

// Error formats "key: problem; valid values: ...".
func (e *FieldError) Error() string {
	msg := e.Key + ": " + e.Problem
	if len(e.Valid) > 0 {
		quoted := make([]string, len(e.Valid))
		for i, v := range e.Valid {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		msg += "; valid values: " + strings.Join(quoted, ", ")
	}
	return msg
}

// ValidationError bundles every FieldError found.
type ValidationError struct{ Errors []*FieldError }

// Error lists every problem, one per line.
func (e *ValidationError) Error() string {
	lines := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		lines[i] = fe.Error()
	}
	return fmt.Sprintf("%d configuration problem(s):\n  %s", len(e.Errors), strings.Join(lines, "\n  "))
}

// AsValidation unwraps a ValidationError when err is one.
func AsValidation(err error) (*ValidationError, bool) {
	var ve *ValidationError
	ok := errors.As(err, &ve)
	return ve, ok
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Validate checks every tag-driven rule (closed option sets, minimums) and the
// cross-field rules the Python validator had. It returns nil or a
// *ValidationError listing everything wrong at once.
func (c *Config) Validate() error {
	var errs []*FieldError
	add := func(key, problem string, valid ...string) {
		errs = append(errs, &FieldError{Key: key, Problem: problem, Valid: valid})
	}

	walk(reflect.ValueOf(c).Elem(), "", func(r fieldRef) {
		switch r.value.Kind() {
		case reflect.String:
			if r.Options != nil && !contains(r.Options, r.value.String()) {
				add(r.Key, fmt.Sprintf("%q is not a valid value", r.value.String()), r.Options...)
			}
		case reflect.Slice:
			if r.Options != nil {
				for i := 0; i < r.value.Len(); i++ {
					if v := r.value.Index(i).String(); !contains(r.Options, v) {
						add(r.Key, fmt.Sprintf("%q is not a valid value", v), r.Options...)
					}
				}
			}
		case reflect.Int:
			if r.Min != nil && int(r.value.Int()) < *r.Min {
				add(r.Key, fmt.Sprintf("must be at least %d (got %d)", *r.Min, r.value.Int()))
			}
		}
	})

	if strings.TrimSpace(c.Personals.FirstName) == "" {
		add("personals.first_name", "must not be blank")
	}
	if strings.TrimSpace(c.Personals.LastName) == "" {
		add("personals.last_name", "must not be blank")
	}
	if strings.TrimSpace(c.Questions.Resume) == "" {
		add("questions.resume", "must not be blank")
	}
	for _, entry := range c.Questions.SkillYears {
		if _, ok := ParseSkill(entry); !ok {
			add("questions.skill_years", fmt.Sprintf("%q is not \"name: years\" with a whole number of years, 1 or more", entry))
		}
	}
	if len(c.Search.Terms) == 0 && len(c.ActiveMarket().Terms) == 0 {
		add("search.terms", "needs at least one search term (or set terms on the active market)")
	}
	for _, name := range MarketNames {
		m, _ := c.Markets.Market(string(name))
		if !currencyRe.MatchString(m.Currency) {
			add("markets."+string(name)+".currency", fmt.Sprintf("%q is not a three-letter ISO 4217 code such as BRL or USD", m.Currency))
		}
	}
	p := c.Pacing
	for _, pair := range []struct {
		lo, hi int
		key    string
	}{
		{p.BetweenJobsMin, p.BetweenJobsMax, "pacing.between_jobs_max_seconds"},
		{p.BetweenActionsMin, p.BetweenActionsMax, "pacing.between_actions_max_seconds"},
		{p.TypingDelayMinMs, p.TypingDelayMaxMs, "pacing.typing_delay_max_ms"},
	} {
		if pair.hi < pair.lo {
			add(pair.key, fmt.Sprintf("must not be below its minimum (%d < %d)", pair.hi, pair.lo))
		}
	}
	if c.AI.Enabled && strings.TrimSpace(c.AI.Model) == "" {
		add("ai.model", "must be set when ai.enabled is true")
	}
	if c.AI.Provider == "custom" && strings.TrimSpace(c.AI.BaseURL) == "" {
		add("ai.base_url", "must be set when ai.provider is custom")
	}

	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Errors: errs}
}

// AIBaseURL is the endpoint in use: the explicit one or the provider default.
func (c *Config) AIBaseURL() string {
	if c.AI.BaseURL != "" {
		return c.AI.BaseURL
	}
	return ProviderBaseURLs[c.AI.Provider]
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
