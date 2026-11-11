package browser

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-rod/rod/lib/cdp"
)

func TestNavigatedAway(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"chrome's answer to a replaced page", &cdp.Error{Code: -32000, Message: "Inspected target navigated or closed"}, true},
		{"the same, wrapped", fmt.Errorf("wait: %w", &cdp.Error{Code: -32000, Message: "Inspected target navigated or closed"}), true},
		{"another error with the same code", &cdp.Error{Code: -32000, Message: "Cannot find context with specified id"}, false},
		{"the same message under another code", &cdp.Error{Code: -32602, Message: "Inspected target navigated or closed"}, false},
		{"not a CDP error", errors.New("Inspected target navigated or closed"), false},
		{"no error", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := navigatedAway(c.err); got != c.want {
				t.Fatalf("navigatedAway(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
