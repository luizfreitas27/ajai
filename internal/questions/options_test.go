package questions

import "testing"

func TestPickOptionTakesAnOptionOrTheBeginningOfOneNeverAGuess(t *testing.T) {
	sponsorship := []string{"Yes, with sponsorship", "Yes, without sponsorship", "No"}
	for _, tc := range []struct {
		text    string
		options []string
		want    int
	}{
		{"yes", []string{"Yes", "No"}, 0},
		{"  YES ", []string{"Yes", "No"}, 0},
		{"y", []string{"Yes", "No"}, 0},
		{"hyb", []string{"Remote", "On-site", "Hybrid"}, 2},
		{"no,   not now", []string{"Yes", "No, not now"}, 1},
		{"No", []string{"No, not now", "No"}, 1}, // the option itself beats a longer one it begins
		{"n", []string{"No", "Not sure"}, -1},    // the beginning of two
		{"yes", sponsorship, -1},
		{"yes, without", sponsorship, 1},
		{"not remote", []string{"Remote", "On-site", "Hybrid"}, -1},
		{"sim", []string{"Yes", "No"}, -1},
		{"", []string{"Yes", "No"}, -1},
	} {
		if got := PickOption(tc.text, tc.options); got != tc.want {
			t.Errorf("PickOption(%q, %q) = %d, want %d", tc.text, tc.options, got, tc.want)
		}
	}
}
