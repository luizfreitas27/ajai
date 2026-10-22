package browser

import (
	"reflect"
	"testing"
)

// Each window mode as the Chrome launcher is told: the flags it adds, and
// whether Chrome runs headless. Visible adds nothing, so it stays today's launch.
func TestWindowModesBecomeLauncherFlags(t *testing.T) {
	sized := map[string]string{"window-position": "-32000,-32000", "window-size": "1280,900"}
	cases := []struct {
		mode     Window
		flags    map[string]string
		headless bool
	}{
		{WindowOffscreen, sized, false},
		{WindowMinimized, map[string]string{"window-size": "1280,900"}, false},
		{WindowVisible, map[string]string{}, false},
		{WindowHeadless, map[string]string{}, true},
	}
	for _, tc := range cases {
		flags, headless := windowFlags(tc.mode)
		if !reflect.DeepEqual(flags, tc.flags) || headless != tc.headless {
			t.Errorf("windowFlags(%q) = %v, headless %v; want %v, headless %v",
				tc.mode, flags, headless, tc.flags, tc.headless)
		}
	}
}
