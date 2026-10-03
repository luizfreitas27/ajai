package config

// MarketName identifies one of the two markets.
type MarketName string

// The markets. The `opts` tag on Markets.Active must list exactly these.
const (
	MarketBrasil        MarketName = "brasil"
	MarketInternacional MarketName = "internacional"
)

// MarketNames lists the valid names in display order.
var MarketNames = []MarketName{MarketBrasil, MarketInternacional}

// MarketNameList is MarketNames as plain strings.
func MarketNameList() []string {
	out := make([]string, len(MarketNames))
	for i, m := range MarketNames {
		out[i] = string(m)
	}
	return out
}

// IsValidMarket reports whether name is one of MarketNames.
func IsValidMarket(name string) bool {
	for _, m := range MarketNames {
		if string(m) == name {
			return true
		}
	}
	return false
}

// Market returns the values of the named market; ok is false for an unknown name.
func (m Markets) Market(name string) (Market, bool) {
	switch MarketName(name) {
	case MarketBrasil:
		return m.Brasil, true
	case MarketInternacional:
		return m.Internacional, true
	}
	return Market{}, false
}

// ActiveMarket returns the values of the market named in markets.active (the
// international one when the name is unknown; Validate reports the name).
func (c *Config) ActiveMarket() Market {
	if m, ok := c.Markets.Market(c.Markets.Active); ok {
		return m
	}
	return c.Markets.Internacional
}

// Essentials are the settings `ajai init` collects; everything else keeps its default.
type Essentials struct {
	FirstName, LastName, Phone, City, Country string
	Terms                                     string // comma-separated
	Location                                  string
	Market                                    string
	Resume                                    string
	YearsOfExperience                         string
	DesiredSalary                             int // in the chosen market's period
	MaxPerHour, MaxPerRun                     int // pacing; 0 keeps the default
}

// ApplyEssentials folds the essentials into a config. Salary and location go to
// the chosen market when it is Brazil (monthly, BRL) and to the general settings
// otherwise, mirroring how the market falls back to them.
func (c *Config) ApplyEssentials(e Essentials) error {
	c.Personals.FirstName = e.FirstName
	c.Personals.LastName = e.LastName
	c.Personals.Phone = e.Phone
	c.Personals.City = e.City
	c.Personals.Country = e.Country
	c.Questions.Resume = e.Resume
	c.Questions.YearsOfExperience = e.YearsOfExperience
	if err := c.Set("search.terms", e.Terms); err != nil {
		return err
	}
	c.Markets.Active = e.Market
	if e.MaxPerHour > 0 {
		c.Pacing.MaxPerHour = e.MaxPerHour
	}
	if e.MaxPerRun > 0 {
		c.Pacing.MaxPerRun = e.MaxPerRun
	}
	if MarketName(e.Market) == MarketBrasil {
		c.Markets.Brasil.DesiredSalary = e.DesiredSalary
		if e.Location != "" {
			c.Markets.Brasil.Location = e.Location
		}
		return nil
	}
	c.Questions.DesiredSalary = e.DesiredSalary
	c.Search.Location = e.Location
	return nil
}
