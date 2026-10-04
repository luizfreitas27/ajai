package config

import (
	"strconv"
	"strings"
)

// Skill is one entry of questions.skill_years: the years the user has with one
// technology or role.
type Skill struct {
	Name  string
	Years int
}

// ParseSkill reads one "name: years" entry; ok is false unless the name
// is not blank and the years are a whole number of 1 or more. The name may hold
// a colon of its own: the years follow the last one.
func ParseSkill(entry string) (Skill, bool) {
	cut := strings.LastIndex(entry, ":")
	if cut < 0 {
		return Skill{}, false
	}
	name := strings.TrimSpace(entry[:cut])
	years, err := strconv.Atoi(strings.TrimSpace(entry[cut+1:]))
	if name == "" || err != nil || years < 1 {
		return Skill{}, false
	}
	return Skill{Name: name, Years: years}, true
}
