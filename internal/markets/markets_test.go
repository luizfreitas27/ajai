package markets

import (
	"reflect"
	"testing"

	"github.com/luizfreitas27/ajai/internal/config"
)

// legacy is the general config every market falls back to.
func legacy() config.Config {
	c := config.Defaults()
	c.Questions.DesiredSalary, c.Questions.CurrentSalary = 120000, 96000
	c.Search.Terms, c.Search.Location = []string{"Software Engineer"}, "United States"
	c.Questions.Resume = "resume.pdf"
	return c
}

func TestTheDefaultMarketReproducesTheLegacySettingsExactly(t *testing.T) {
	m := Resolve(legacy())
	want := Resolved{Name: "internacional", Currency: "USD", SalaryPeriod: Annual,
		DesiredAnnual: 120000, DesiredMonthly: 10000, CurrentAnnual: 96000, CurrentMonthly: 8000,
		Location: "United States", Terms: []string{"Software Engineer"}, Resume: "resume.pdf",
		MessageTemplate: config.Defaults().Markets.Internacional.MessageTemplate}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %+v\nwant %+v", m, want)
	}
}

func TestBrazilSalariesAreMonthlyInReaisAndCarriedBothWays(t *testing.T) {
	c := legacy()
	c.Markets.Active = "brasil"
	c.Markets.Brasil.DesiredSalary, c.Markets.Brasil.CurrentSalary = 8000, 6500
	m := Resolve(c)
	if m.Currency != "BRL" || m.SalaryPeriod != Monthly || m.DesiredMonthly != 8000 || m.DesiredAnnual != 96000 ||
		m.CurrentMonthly != 6500 || m.CurrentAnnual != 78000 || m.DesiredAsEntered() != 8000 || m.CurrentAsEntered() != 6500 {
		t.Errorf("%+v", m)
	}
	if m.Location != "Brasil" {
		t.Errorf("the Brazil default location should win over the legacy one: %q", m.Location)
	}
}

func TestInternationalValuesWinOverTheLegacyOnesWhenSet(t *testing.T) {
	c := legacy()
	c.Markets.Internacional = config.Market{Currency: "EUR", DesiredSalary: 90000, Location: "Portugal", Terms: []string{"Backend Developer"}, Resume: "cv-en.pdf"}
	m := Resolve(c)
	if m.Currency != "EUR" || m.DesiredAnnual != 90000 || m.DesiredMonthly != 7500 || m.CurrentAnnual != 96000 ||
		m.Location != "Portugal" || !reflect.DeepEqual(m.Terms, []string{"Backend Developer"}) || m.Resume != "cv-en.pdf" {
		t.Errorf("%+v", m)
	}
}

func TestBlankBrazilianValuesFallBackToTheLegacySettings(t *testing.T) {
	c := legacy()
	c.Markets.Active = "brasil"
	c.Markets.Brasil.Location, c.Markets.Brasil.Terms = "", nil
	m := Resolve(c)
	if m.DesiredMonthly != 10000 || m.DesiredAnnual != 120000 || m.Location != "United States" ||
		!reflect.DeepEqual(m.Terms, []string{"Software Engineer"}) || m.Resume != "resume.pdf" {
		t.Errorf("%+v", m)
	}
}

func TestTheMarketNameIsCaseInsensitiveAndUnknownNamesResolveAsInternational(t *testing.T) {
	c := legacy()
	c.Markets.Active = " Brasil "
	if m := Resolve(c); m.Name != config.MarketBrasil || m.SalaryPeriod != Monthly {
		t.Errorf("%+v", m)
	}
	c.Markets.Active = "europa" // Validate reports this; Resolve stays usable
	if m := Resolve(c); m.Name != config.MarketInternacional {
		t.Errorf("%+v", m)
	}
}

func TestBlankCurrenciesFallBackAndMoneyIsRoundedToCents(t *testing.T) {
	c := legacy()
	c.Markets.Brasil.Currency, c.Markets.Internacional.Currency = "", ""
	c.Questions.DesiredSalary = 100000
	if m := Resolve(c); m.Currency != "USD" || m.DesiredMonthly != 8333.33 {
		t.Errorf("%+v", m)
	}
	c.Markets.Active = "brasil"
	if m := Resolve(c); m.Currency != "BRL" || m.DesiredMonthly != 8333.33 || m.DesiredAnnual != 99999.96 {
		t.Errorf("%+v", m)
	}
}

func TestAnEmptyConfigStillResolves(t *testing.T) {
	m := Resolve(config.Defaults())
	if m.Name != "internacional" || m.DesiredAnnual != 0 || len(m.Terms) != 0 {
		t.Errorf("%+v", m)
	}
}

func TestFormatMoneyIsWholeWhenWhole(t *testing.T) {
	cases := map[float64]string{8000: "8000", 10000: "10000", 8333.333333: "8333.33", 0: "0", 1200000.0 / 12: "100000"}
	for in, want := range cases {
		if got := FormatMoney(in); got != want {
			t.Errorf("FormatMoney(%v) = %q, want %q", in, got, want)
		}
	}
}
