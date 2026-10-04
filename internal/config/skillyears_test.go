package config

import (
	"strings"
	"testing"
)

func TestSkillYearsAreNameColonWholeYears(t *testing.T) {
	for entry, want := range map[string]SkillYears{
		"java: 3":                 {"java", 3},
		" .net :5 ":               {".net", 5},
		"computação em nuvem: 12": {"computação em nuvem", 12},
		"c#: 1":                   {"c#", 1},
		"node.js: v2: 2":          {"node.js: v2", 2}, // the years follow the last colon
	} {
		if got, ok := ParseSkillYears(entry); !ok || got != want {
			t.Errorf("%q: %+v %v, want %+v", entry, got, ok, want)
		}
	}
	for _, entry := range []string{"java", "java: ", ": 3", "java: 0", "java: -2", "java: 2.5", "java: three"} {
		if got, ok := ParseSkillYears(entry); ok {
			t.Errorf("%q passed as %+v", entry, got)
		}
	}
}

func TestSkillYearsAreSetAsAListAndValidated(t *testing.T) {
	c := Defaults()
	c.Personals.FirstName, c.Personals.LastName, c.Search.Terms = "A", "B", []string{"x"}
	if err := c.Set("questions.skill_years", "java: 3, .net: 5"); err != nil || strings.Join(c.Questions.SkillYears, "|") != "java: 3|.net: 5" {
		t.Fatalf("%v %q", err, c.Questions.SkillYears)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("valid entries refused: %v", err)
	}
	if _, err := c.SetChecked("questions.skill_years", "java: 3, angular"); err == nil || !strings.Contains(err.Error(), `"angular" is not "name: years"`) {
		t.Errorf("an entry without years: %v", err)
	}
}
