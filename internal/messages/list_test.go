package messages

import "testing"

func TestListDigestChangesWithTheQueueAndNotItsOrder(t *testing.T) {
	a := Item{ID: 1, Digest: Digest("Oi")}
	b := Item{ID: 2, Digest: Digest("Ola")}
	base := ListDigest([]Item{a, b})
	for _, tc := range []struct {
		name  string
		items []Item
		same  bool
	}{
		{"same items, other order", []Item{b, a}, true},
		{"one message added", []Item{a, b, {ID: 3, Digest: Digest("Hey")}}, false},
		{"one message removed", []Item{a}, false},
		{"a text edited", []Item{a, {ID: 2, Digest: Digest("Olá")}}, false},
		{"a message renumbered", []Item{a, {ID: 9, Digest: b.Digest}}, false},
		{"empty queue", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ListDigest(tc.items) == base; got != tc.same {
				t.Errorf("same digest = %v, want %v", got, tc.same)
			}
		})
	}
}

func TestListDigestOfNothingIsStable(t *testing.T) {
	if ListDigest(nil) != ListDigest([]Item{}) {
		t.Error("nil and empty queues differ")
	}
}
