// Package markets resolves the active Market: the currency, salaries, search
// location, search terms and resume that differ between applying in Brazil
// (monthly salaries) and abroad (annual), with every blank value replaced by the
// general setting it falls back to. Ported from the Python modules/markets.py.
package markets

import (
	"math"
	"strconv"
	"strings"

	"github.com/luizfreitas27/aja/internal/config"
)

// Period is how the user entered salaries in a market.
type Period string

// Salary periods.
const (
	Monthly Period = "monthly"
	Annual  Period = "annual"
)

// Resolved is one market with salaries carried both ways, so a form may ask for either.
type Resolved struct {
	Name           config.MarketName
	Currency       string
	SalaryPeriod   Period
	DesiredAnnual  float64
	DesiredMonthly float64
	CurrentAnnual  float64
	CurrentMonthly float64
	Location       string
	Terms          []string
	Resume         string // the market's own resume, else questions.resume
}

// DesiredAsEntered is the desired salary in the period the user typed it in.
func (r Resolved) DesiredAsEntered() float64 {
	if r.SalaryPeriod == Monthly {
		return r.DesiredMonthly
	}
	return r.DesiredAnnual
}

// CurrentAsEntered is the current salary in the period the user typed it in.
func (r Resolved) CurrentAsEntered() float64 {
	if r.SalaryPeriod == Monthly {
		return r.CurrentMonthly
	}
	return r.CurrentAnnual
}

// Resolve builds the active market from a validated config: markets.active names
// it (case and surrounding spaces ignored); zero salaries, blank
// currency/location/terms/resume fall back to the general settings. For Brazil a
// blank salary is the general ANNUAL figure divided by 12. Money is rounded to
// two decimals, as the Python version did.
func Resolve(c config.Config) Resolved {
	active := strings.ToLower(strings.TrimSpace(c.Markets.Active))
	name := config.MarketName(active)
	m, ok := c.Markets.Market(active)
	if !ok {
		name, m = config.MarketInternacional, c.Markets.Internacional
	}
	legacyDesired := float64(c.Questions.DesiredSalary)
	legacyCurrent := float64(c.Questions.CurrentSalary)
	r := Resolved{
		Name:     name,
		Currency: firstNonBlank(m.Currency, defaultCurrency(name)),
		Location: firstNonBlank(m.Location, c.Search.Location),
		Terms:    firstNonEmpty(m.Terms, c.Search.Terms),
		Resume:   firstNonBlank(m.Resume, c.Questions.Resume),
	}
	if name == config.MarketBrasil {
		r.SalaryPeriod = Monthly
		r.DesiredMonthly = tidy(or(float64(m.DesiredSalary), legacyDesired/12))
		r.CurrentMonthly = tidy(or(float64(m.CurrentSalary), legacyCurrent/12))
		r.DesiredAnnual = tidy(r.DesiredMonthly * 12)
		r.CurrentAnnual = tidy(r.CurrentMonthly * 12)
		return r
	}
	r.SalaryPeriod = Annual
	r.DesiredAnnual = tidy(or(float64(m.DesiredSalary), legacyDesired))
	r.CurrentAnnual = tidy(or(float64(m.CurrentSalary), legacyCurrent))
	r.DesiredMonthly = tidy(r.DesiredAnnual / 12)
	r.CurrentMonthly = tidy(r.CurrentAnnual / 12)
	return r
}

// defaultCurrency is the market's currency when the config leaves it blank.
func defaultCurrency(name config.MarketName) string {
	if name == config.MarketBrasil {
		return "BRL"
	}
	return "USD"
}

// tidy rounds money to two decimals.
func tidy(v float64) float64 { return math.Round(v*100) / 100 }

// FormatMoney renders an amount as it should be typed into a form: whole
// amounts whole ("8000", never "8000.0"), otherwise two decimals.
func FormatMoney(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func or(v, fallback float64) float64 {
	if v != 0 {
		return v
	}
	return fallback
}

func firstNonBlank(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstNonEmpty(a, b []string) []string {
	if len(a) > 0 {
		return append([]string(nil), a...)
	}
	return append([]string(nil), b...)
}
