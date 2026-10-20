package messages

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// Item is one queued message as a listing shows it: its id and the digest of
// its text.
type Item struct {
	ID     int64
	Digest string
}

// ListDigest fingerprints a whole queue listing: the ids and digests, in id
// order, so an approval of "the list" applies only to the queue it showed.
func ListDigest(items []Item) string {
	sorted := append([]Item(nil), items...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	h := fnv.New32a()
	for _, it := range sorted {
		_, _ = fmt.Fprintf(h, "%d:%s\n", it.ID, it.Digest)
	}
	return fmt.Sprintf("%08x", h.Sum32())
}
