package cli

import (
	"strings"
	"testing"
)

func TestServeRefusesANonLoopbackAddressBeforeOpeningAnything(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	code, _, errText := h.run("serve", "--addr", "0.0.0.0:7077")
	if code == 0 || !strings.Contains(errText, "loopback only") {
		t.Errorf("serve on 0.0.0.0: %d %s", code, errText)
	}
}
