// Package memory is the answer memory: every answer given to an unrecognised
// question, keyed by normalised label and control kind, with a source (ai, user,
// form), a review state (pending, approved), a use count and the last job it was
// used on. Ported from the Python modules/answers_memory.py and backed by the
// SQLite answers table. The memory knows nothing about sensitive questions; the
// questions package gates before calling it.
package memory

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/luizfreitas27/aja/internal/store"
	"github.com/luizfreitas27/aja/internal/textmatch"
)

// Vocabularies, the same strings the Python memory used.
var (
	Kinds   = []string{"text", "textarea", "select", "radio", "checkbox"}
	Sources = []string{"ai", "user", "form"}
	States  = []string{"pending", "approved"}
)

// SimilarityThreshold is the difflib ratio at or above which a reworded label
// still hits a remembered answer of the same kind.
const SimilarityThreshold = 0.92

// Remembered is one entry, the store row under a domain name.
type Remembered = store.Answer

// Memory is the answer memory over a store.
type Memory struct {
	st     *store.Store
	logger *slog.Logger
	now    func() time.Time
}

// New wraps a store. logger may be nil.
func New(st *store.Store, logger *slog.Logger) *Memory {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Memory{st: st, logger: logger, now: time.Now}
}

// Lookup returns the remembered answer for label in a control of kind and
// whether the match was approximate: exact match on the normalised label first;
// otherwise, when approximate is allowed, the most similar entry of the same
// kind at or above SimilarityThreshold. (nil, false) when nothing fits or the
// label normalises to nothing.
func (m *Memory) Lookup(label, kind string, approximate bool) (*Remembered, bool, error) {
	key := textmatch.Normalise(label)
	if key == "" {
		return nil, false, nil
	}
	if exact, err := m.st.FindAnswer(kind, key); err != nil || exact != nil {
		return exact, false, err
	}
	if !approximate {
		return nil, false, nil
	}
	all, err := m.st.ListAnswers("")
	if err != nil {
		return nil, false, err
	}
	var best *Remembered
	bestRatio := 0.0
	for i := range all {
		if all[i].Kind != kind {
			continue
		}
		if r := textmatch.Ratio(key, all[i].Normalised); r > bestRatio {
			best, bestRatio = &all[i], r
		}
	}
	if best != nil && bestRatio >= SimilarityThreshold {
		m.logger.Info("answer memory: approximate match", "ratio", fmt.Sprintf("%.2f", bestRatio), "asked", label, "remembered", best.Label)
		return best, true, nil
	}
	return nil, false, nil
}

// Remember stores an answer. An answer the user gave is approved; anything else
// is pending. An existing entry for the same question is updated instead of
// duplicated, except that only the user can replace an approved answer. uses
// starts at 0: remembering is not using.
func (m *Memory) Remember(label, kind, answer, source, jobLink string) (*Remembered, error) {
	if !contains(Kinds, kind) {
		return nil, fmt.Errorf("unknown control kind %q", kind)
	}
	if !contains(Sources, source) {
		return nil, fmt.Errorf("unknown answer source %q", source)
	}
	state := "pending"
	if source == "user" {
		state = "approved"
	}
	key := textmatch.Normalise(label)
	existing, err := m.st.FindAnswer(kind, key)
	if err != nil {
		return nil, err
	}
	now := store.Timestamp(m.now())
	if existing != nil {
		if source != "user" && existing.State == "approved" {
			return existing, nil // the user's word stands
		}
		existing.Answer, existing.Source, existing.State, existing.UpdatedAt = answer, source, state, now
		if jobLink != "" {
			existing.LastJobLink = jobLink
		}
		return existing, m.st.UpsertAnswer(*existing)
	}
	entry := Remembered{ID: store.NewAnswerID(), Label: label, Normalised: key, Kind: kind, Answer: answer,
		Source: source, State: state, LastJobLink: jobLink, CreatedAt: now, UpdatedAt: now}
	return &entry, m.st.UpsertAnswer(entry)
}

// RecordUse counts one more use of an entry, on jobLink if given. The live row
// is re-read by id: the review queue may have deleted it meanwhile, in which
// case there is nothing to count.
func (m *Memory) RecordUse(id, jobLink string) error {
	live, err := m.st.GetAnswer(id)
	if err != nil || live == nil {
		return err
	}
	live.Uses++
	if jobLink != "" {
		live.LastJobLink = jobLink
	}
	live.UpdatedAt = store.Timestamp(m.now())
	return m.st.UpsertAnswer(*live)
}

// Get returns the entry with this id, or nil.
func (m *Memory) Get(id string) (*Remembered, error) { return m.st.GetAnswer(id) }

// List returns entries newest first; state "" means all.
func (m *Memory) List(state string) ([]Remembered, error) { return m.st.ListAnswers(state) }

// PendingCount is how many entries await review.
func (m *Memory) PendingCount() (int, error) { return m.st.CountAnswers("pending") }

// ErrUnknownID is returned by Approve and Delete for an id that is not in the memory.
var ErrUnknownID = errors.New("no remembered answer with that id")

// Approve marks an entry approved, optionally replacing its answer first (newAnswer
// "" keeps it). Approving as-is keeps who gave the answer; a corrected answer is
// the user's.
func (m *Memory) Approve(id, newAnswer string) (*Remembered, error) {
	entry, err := m.st.GetAnswer(id)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrUnknownID
	}
	if newAnswer != "" && newAnswer != entry.Answer {
		entry.Answer, entry.Source = newAnswer, "user"
	}
	entry.State = "approved"
	entry.UpdatedAt = store.Timestamp(m.now())
	return entry, m.st.UpsertAnswer(*entry)
}

// Delete forgets an entry.
func (m *Memory) Delete(id string) error {
	removed, err := m.st.DeleteAnswer(id)
	if err != nil {
		return err
	}
	if !removed {
		return ErrUnknownID
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
