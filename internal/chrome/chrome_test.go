package chrome

import "testing"

func TestTheEverydayProfileFollowsTheBrowserFound(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/u/.config")
	cases := map[string]string{
		"/usr/bin/chromium":               "/home/u/.config/chromium",
		"/usr/bin/chromium-browser":       "/home/u/.config/chromium",
		"/usr/bin/google-chrome-stable":   "/home/u/.config/google-chrome",
		"/usr/bin/google-chrome":          "/home/u/.config/google-chrome",
		"/usr/bin/google-chrome-beta":     "/home/u/.config/google-chrome-beta",
		"/usr/bin/google-chrome-unstable": "/home/u/.config/google-chrome-unstable",
		"":                                "/home/u/.config/google-chrome", // Chrome not found
	}
	for bin, want := range cases {
		if got := EverydayProfileDir(bin); got != want {
			t.Errorf("%q: %q, want %q", bin, got, want)
		}
	}
}
