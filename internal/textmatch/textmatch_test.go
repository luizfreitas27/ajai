package textmatch

import (
	"strings"
	"testing"
)

func TestFindWordMatchesWholeWordsOnly(t *testing.T) {
	cases := []struct {
		text  string
		words []string
		want  string
	}{
		{"Senior JavaScript Engineer", []string{"java"}, ""}, // java != javascript
		{"Strong Java and Spring experience", []string{"java"}, "java"},
		{"Backend role, ASP.NET Core", []string{".NET"}, ".NET"}, // leading dot still matches
		{"We use .NETWORKING gear", []string{".NET"}, ""},
		{"No C2C please", []string{"No C2C"}, "No C2C"}, // multi-word phrases
		{"Must be a US Citizen", []string{"us citizen"}, "us citizen"},
		{"Rubyists welcome", []string{"Ruby"}, ""},
		{"are you legally authorized to work in the united states?", []string{"state"}, ""},
		{"what state do you live in?", []string{"state"}, "state"},
		{"what is your linkedin profile?", []string{"link"}, ""},
		{"sexual orientation", []string{"sex"}, ""},
		{"Non-citizen allowed to work", []string{"no"}, ""},
		{"Você é PCD?", []string{"pcd"}, "pcd"},
		{"Pretensão salarial", []string{"pretensão"}, "pretensão"},
		{"Secretary experience in years", []string{"secret"}, ""},
		{"", []string{"x", ""}, ""},
	}
	for _, tc := range cases {
		if got := FindWord(tc.text, tc.words); got != tc.want {
			t.Errorf("FindWord(%q, %v) = %q, want %q", tc.text, tc.words, got, tc.want)
		}
	}
}

func TestNormaliseMatchesThePythonFunction(t *testing.T) {
	cases := map[string]string{
		"How many years of experience do you have with Python?": "how many years of experience do you have with python",
		"  Notice   period (in days)  ":                         "notice period in days",
		"What's your GitHub URL?":                               "what s your github url",
		"Você possui inglês avançado?":                          "voc possui ingl s avan ado",
		"":                                                      "",
	}
	for in, want := range cases {
		if got := Normalise(in); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRatioBehavesLikeDifflib(t *testing.T) {
	cases := []struct {
		a, b     string
		min, max float64
	}{
		{"abc", "abc", 1, 1},
		{"abc", "xyz", 0, 0},
		{"", "", 1, 1},
		// difflib gives 0.9703 for this pair; the memory threshold is 0.92.
		{"how many years of experience do you have with python", "how many years experience do you have with python", 0.9702, 0.9704},
		// ...0.8704 for a different skill, below the threshold...
		{"how many years of experience do you have with python", "how many years of experience do you have with kubernetes", 0.8703, 0.8705},
		// ...and 0.9556 for a near-identical attestation, which is why checkboxes never match approximately.
		{"i agree to the terms and conditions of acme", "i agree to the terms and conditions of acme inc", 0.9555, 0.9557},
	}
	long1 := strings.Repeat("i consent to the processing of my personal data by acme for recruitment purposes ", 4)
	long2 := strings.Repeat("i agree that globex may store my application and contact me about future openings ", 4)
	cases = append(cases, struct {
		a, b     string
		min, max float64
	}{long1, long2, 0, 0.5}) // difflib: 0.18 with autojunk; without it these would look 0.9 alike
	for _, tc := range cases {
		got := Ratio(tc.a, tc.b)
		if got < tc.min || got > tc.max {
			t.Errorf("Ratio(%q, %q) = %.4f, want within [%.2f, %.2f]", tc.a, tc.b, got, tc.min, tc.max)
		}
	}
}

func TestHasFoldedIgnoresAccentsOnBothSides(t *testing.T) {
	for _, tc := range []struct {
		text, word string
		want       bool
	}{
		{"Possui EXPERIÊNCIA com Java?", "experiencia", true},
		{"Possui experiencia com Java?", "experiência", true},
		{"Computação em nuvem", "computacao", true},
		{"Experiências", "experiência", false}, // still whole words
	} {
		if got := HasFolded(tc.text, tc.word); got != tc.want {
			t.Errorf("HasFolded(%q, %q) = %v", tc.text, tc.word, got)
		}
	}
}
