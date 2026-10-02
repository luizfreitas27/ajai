package browser

import (
	"strings"
	"testing"
)

func TestTheScrollScriptCentresTheElementSmoothlyOnlyWhenAsked(t *testing.T) {
	if s := ScrollScript(true); !strings.Contains(s, `behavior: "smooth"`) || !strings.Contains(s, `block: "center"`) {
		t.Error(s)
	}
	if s := ScrollScript(false); !strings.Contains(s, `behavior: "instant"`) {
		t.Error(s)
	}
}
