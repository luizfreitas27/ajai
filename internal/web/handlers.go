package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/memory"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/questions"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/store"
)

// stopReason is the ending reason of a run stopped from the web front.
const stopReason = "stopped from the web front"

type holderJSON struct {
	PID     int    `json:"pid"`
	Origin  string `json:"origin"`
	From    string `json:"from"`
	Window  int64  `json:"window,omitempty"`
	Started string `json:"started"`
}

func holderOf(h run.Holder) *holderJSON {
	return &holderJSON{PID: h.PID, Origin: string(h.Origin), From: h.From(), Window: h.Window,
		Started: h.Started.Format(time.RFC3339Nano)}
}

type stateJSON struct {
	RunID      int64  `json:"run_id"`
	Market     string `json:"market"`
	DryRun     bool   `json:"dry_run"`
	Term       string `json:"term"`
	Applied    int    `json:"applied"`
	External   int    `json:"external"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	WouldApply int    `json:"would_apply"`
	NextRun    string `json:"next_run,omitempty"`
}

type runJSON struct {
	ID             int64  `json:"id"`
	StartedAt      string `json:"started_at"`
	EndedAt        string `json:"ended_at"`
	Market         string `json:"market"`
	DryRun         bool   `json:"dry_run"`
	Applied        int    `json:"applied"`
	External       int    `json:"external"`
	Failed         int    `json:"failed"`
	Skipped        int    `json:"skipped"`
	PendingAnswers int    `json:"pending_answers"`
	Reason         string `json:"reason"`
}

func runsOf(rs []store.Run) []runJSON {
	out := make([]runJSON, 0, len(rs))
	for _, r := range rs {
		out = append(out, runJSON{ID: r.ID, StartedAt: r.StartedAt, EndedAt: r.EndedAt, Market: r.Market,
			DryRun: r.DryRun, Applied: r.Applied, External: r.External, Failed: r.Failed, Skipped: r.Skipped,
			PendingAnswers: r.PendingAnswers, Reason: r.Reason})
	}
	return out
}

// limitParam reads ?limit=, def when absent, capped at 200.
func limitParam(w http.ResponseWriter, r *http.Request, def int) (int, bool) {
	text := r.URL.Query().Get("limit")
	if text == "" {
		return def, true
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 {
		writeErr(w, http.StatusBadRequest, "limit must be a positive whole number")
		return 0, false
	}
	return min(n, 200), true
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.statusNow(r.Context()))
}

func (s *server) runs(w http.ResponseWriter, r *http.Request) {
	limit, ok := limitParam(w, r, 20)
	if !ok {
		return
	}
	rs, err := s.d.Store.ListRuns(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runsOf(rs))
}

func (s *server) startRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Market string `json:"market"`
		DryRun bool   `json:"dry_run"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Market != "" && !slices.Contains(config.MarketNameList(), body.Market) {
		writeErr(w, http.StatusBadRequest, "market must be one of "+strings.Join(config.MarketNameList(), ", ")+" (or blank for the config's)")
		return
	}
	args := []string{"run", "--unattended", "--origin", string(run.OriginWeb)}
	if body.Market != "" {
		args = append(args, "--market", body.Market)
	}
	if body.DryRun {
		args = append(args, "--dry-run")
	}
	s.startAjai(w, args, "run started from the web front")
}

// startAjai launches ajai args in a process of its own, refused with 409 while a run holds the lock.
func (s *server) startAjai(w http.ResponseWriter, args []string, msg string) {
	if act, _ := run.ReadActivity(s.d.DataDir); act.Held {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  act.Holder.String() + " is in progress",
			"holder": holderOf(act.Holder)})
		return
	}
	pid, _, err := s.d.Launcher.Start(args)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start ajai: "+err.Error())
		return
	}
	s.log.Info(msg, "pid", pid, "args", strings.Join(args[1:], " "))
	writeJSON(w, http.StatusAccepted, map[string]any{"pid": pid, "args": args})
}

// startBackfill starts ajai messages backfill --unattended --origin web, with --limit when one is given.
func (s *server) startBackfill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Limit *int `json:"limit"`
	}
	if !decode(w, r, &body) {
		return
	}
	args := []string{"messages", "backfill", "--unattended", "--origin", "web"}
	if body.Limit != nil {
		if *body.Limit < 0 {
			writeErr(w, http.StatusBadRequest, "limit must be 0 (all) or a positive number of jobs")
			return
		}
		args = append(args, "--limit", strconv.Itoa(*body.Limit))
	}
	s.startAjai(w, args, "backfill started from the web front")
}

func (s *server) stopRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
	}
	if !decode(w, r, &body) {
		return
	}
	started, err := time.Parse(time.RFC3339Nano, body.Started)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "started must be the run's start time, RFC 3339")
		return
	}
	h, held, err := run.ReadLock(s.d.DataDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !held || h.PID != body.PID || !h.Started.Equal(started) {
		resp := map[string]any{"error": "that run is not in progress; nothing was asked to stop", "holder": nil}
		if held {
			resp["holder"] = holderOf(h)
		}
		writeJSON(w, http.StatusConflict, resp)
		return
	}
	h, err = run.RequestStop(s.d.DataDir, stopReason)
	if errors.Is(err, run.ErrNoRun) {
		writeErr(w, http.StatusConflict, "no run in progress")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("stop requested from the web front", "run", h.String())
	writeJSON(w, http.StatusOK, map[string]any{"holder": holderOf(h)})
}

type jobJSON struct {
	JobID        string `json:"job_id"`
	Outcome      string `json:"outcome"`
	Reason       string `json:"reason"`
	Title        string `json:"title"`
	Company      string `json:"company"`
	WorkLocation string `json:"work_location"`
	WorkStyle    string `json:"work_style"`
	JobLink      string `json:"job_link"`
	ExternalLink string `json:"external_link"`
	HRName       string `json:"hr_name"`
	HRLink       string `json:"hr_link"`
	DateApplied  string `json:"date_applied"`
	RecordedAt   string `json:"recorded_at"`
	RunID        int64  `json:"run_id"`
}

func (s *server) jobs(w http.ResponseWriter, r *http.Request) {
	limit, ok := limitParam(w, r, 50)
	if !ok {
		return
	}
	offset := 0
	if text := r.URL.Query().Get("offset"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "offset must be a whole number, 0 or more")
			return
		}
		offset = n
	}
	jobs, err := s.d.Store.ListJobs(store.JobFilter{Text: r.URL.Query().Get("text"), Limit: limit, Offset: offset})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]jobJSON, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobJSON{JobID: j.JobID, Outcome: string(j.Outcome), Reason: j.Reason, Title: j.Title,
			Company: j.Company, WorkLocation: j.WorkLocation, WorkStyle: j.WorkStyle, JobLink: j.JobLink,
			ExternalLink: j.ExternalLink, HRName: j.HRName, HRLink: j.HRLink, DateApplied: j.DateApplied,
			RecordedAt: j.RecordedAt, RunID: j.RunID})
	}
	writeJSON(w, http.StatusOK, out)
}

type messageJSON struct {
	ID            int64  `json:"id"`
	State         string `json:"state"`
	JobID         string `json:"job_id"`
	Title         string `json:"title"`
	Company       string `json:"company"`
	RecipientName string `json:"recipient_name"`
	RecipientURL  string `json:"recipient_url"`
	Text          string `json:"text"`
	Digest        string `json:"digest"`
	Error         string `json:"error"`
	SentAt        string `json:"sent_at"`
	CreatedAt     string `json:"created_at"`
}

// jobTitle returns the title and company of a job, empty when it is not recorded.
func (s *server) jobTitle(cache map[string][2]string, jobID string) (string, string, error) {
	if t, ok := cache[jobID]; ok {
		return t[0], t[1], nil
	}
	job, err := s.d.Store.GetJob(jobID)
	if err != nil {
		return "", "", err
	}
	var t [2]string
	if job != nil {
		t = [2]string{job.Title, job.Company}
	}
	cache[jobID] = t
	return t[0], t[1], nil
}

func (s *server) messages(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state != "" && !slices.Contains(messageStates, state) {
		writeErr(w, http.StatusBadRequest, "state must be one of "+strings.Join(messageStates, ", "))
		return
	}
	msgs, err := s.d.Store.ListMessages(state)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	queued, err := s.d.Store.ListMessages(store.MessageQueued)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var items []messages.Item
	for _, m := range queued {
		items = append(items, messages.Item{ID: m.ID, Digest: messages.Digest(m.Text)})
	}
	cache := map[string][2]string{}
	out := make([]messageJSON, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- { // newest first
		m := msgs[i]
		title, company, err := s.jobTitle(cache, m.JobID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, messageJSON{ID: m.ID, State: m.State, JobID: m.JobID, Title: title, Company: company,
			RecipientName: m.RecipientName, RecipientURL: m.RecipientURL, Text: m.Text,
			Digest: messages.Digest(m.Text), Error: m.Error, SentAt: m.SentAt, CreatedAt: m.CreatedAt})
	}
	unreached, err := s.d.Store.CountUnreachedApplied()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out,
		"queue_digest": messages.ListDigest(items), "queued": len(items), "unreached": unreached})
}

// messageStates are the states "state" takes, as the store names them.
var messageStates = []string{store.MessageQueued, store.MessageApproved, store.MessageSending, store.MessageSent, store.MessageFailed, store.MessageNoRecipient}

func (s *server) approveMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the message id must be a number")
		return
	}
	var body struct {
		Digest string `json:"digest"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch err := s.d.Store.ApproveMessage(id, body.Digest); {
	case errors.Is(err, store.ErrNoMessage):
		writeErr(w, http.StatusNotFound, fmt.Sprintf("there is no message %d", id))
		return
	case errors.Is(err, store.ErrNotQueued), errors.Is(err, store.ErrTextChanged):
		writeErr(w, http.StatusConflict, err.Error()+"; list the messages again")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("message approved from the web front", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": store.MessageApproved})
}

func (s *server) approveAll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Digest string `json:"digest"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, err := s.d.Store.ApproveAll(body.Digest)
	if errors.Is(err, store.ErrQueueChanged) {
		writeErr(w, http.StatusConflict, "the queue changed since it was read; list the messages again. Nothing was approved")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("messages approved from the web front", "count", n)
	writeJSON(w, http.StatusOK, map[string]any{"approved": n})
}

type inviteJSON struct {
	ID            int64  `json:"id"`
	State         string `json:"state"`
	JobID         string `json:"job_id"`
	Title         string `json:"title"`
	Company       string `json:"company"`
	RecipientName string `json:"recipient_name"`
	RecipientURL  string `json:"recipient_url"`
	Error         string `json:"error"`
	SentAt        string `json:"sent_at"`
	CreatedAt     string `json:"created_at"`
}

var inviteStates = []string{store.InviteQueued, store.InviteSent, store.InviteAlready, store.InviteUnavailable, store.InviteFailed}

func (s *server) invites(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state != "" && !slices.Contains(inviteStates, state) {
		writeErr(w, http.StatusBadRequest, "state must be one of "+strings.Join(inviteStates, ", "))
		return
	}
	limit, ok := limitParam(w, r, 50)
	if !ok {
		return
	}
	invs, err := s.d.Store.ListInvites(state, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cache := map[string][2]string{}
	out := make([]inviteJSON, 0, len(invs))
	for _, v := range invs {
		title, company, err := s.jobTitle(cache, v.JobID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, inviteJSON{ID: v.ID, State: v.State, JobID: v.JobID, Title: title, Company: company,
			RecipientName: v.RecipientName, RecipientURL: v.RecipientURL, Error: v.Error, SentAt: v.SentAt, CreatedAt: v.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type answerJSON struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Kind        string   `json:"kind"`
	Answer      string   `json:"answer"`
	Source      string   `json:"source"`
	State       string   `json:"state"`
	Uses        int      `json:"uses"`
	Options     []string `json:"options"`
	LastJobLink string   `json:"last_job_link"`
	UpdatedAt   string   `json:"updated_at"`
	Digest      string   `json:"digest"`
}

func answerOf(e memory.Remembered) answerJSON {
	return answerJSON{ID: e.ID, Label: e.Label, Kind: e.Kind, Answer: e.Answer, Source: e.Source, State: e.State,
		Uses: e.Uses, Options: e.Options, LastJobLink: e.LastJobLink, UpdatedAt: e.UpdatedAt, Digest: messages.Digest(e.Answer)}
}

func (s *server) answers(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if r.URL.Query().Get("pending") == "1" {
		state = "pending"
	}
	if state != "" && !slices.Contains(memory.States, state) {
		writeErr(w, http.StatusBadRequest, "state must be pending or approved")
		return
	}
	entries, err := memory.New(s.d.Store, s.log).List(state)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]answerJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, answerOf(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) approveAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Digest string `json:"digest"`
		Answer string `json:"answer"`
		Blank  bool   `json:"blank"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Digest == "" {
		writeErr(w, http.StatusBadRequest, "digest is required: the digest of the answer you read")
		return
	}
	mem := memory.New(s.d.Store, s.log)
	id := r.PathValue("id")
	live, err := mem.Get(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if live == nil {
		writeErr(w, http.StatusNotFound, "no remembered answer with that id")
		return
	}
	if body.Digest != messages.Digest(live.Answer) {
		writeErr(w, http.StatusConflict, "the answer changed since it was read; check it again")
		return
	}
	var approved *memory.Remembered
	if body.Blank {
		// A deliberate blank: the question is left empty, and no answer may come with it.
		if strings.TrimSpace(body.Answer) != "" {
			writeErr(w, http.StatusBadRequest, "a blank approval takes no answer: send blank or an answer, not both")
			return
		}
		approved, err = mem.ApproveBlank(id)
	} else {
		text := strings.TrimSpace(body.Answer)
		if text != "" {
			switch {
			case live.Kind == "checkbox":
				writeErr(w, http.StatusBadRequest, "a checkbox takes no text: approve it as it is")
				return
			case len(live.Options) > 0:
				i := questions.PickOption(text, live.Options)
				if i < 0 {
					writeErr(w, http.StatusBadRequest, fmt.Sprintf("%q is not one of the options: %s", text, strings.Join(live.Options, " · ")))
					return
				}
				text = live.Options[i]
			}
		}
		approved, err = mem.Approve(id, text)
	}
	switch {
	case errors.Is(err, memory.ErrCannotBeBlank):
		writeErr(w, http.StatusBadRequest, "a checkbox, select or radio cannot be left blank: answer it instead")
		return
	case errors.Is(err, memory.ErrNothingToApprove):
		writeErr(w, http.StatusBadRequest, "this question has no answer yet; send one as answer")
		return
	case errors.Is(err, memory.ErrUnknownID):
		writeErr(w, http.StatusNotFound, "no remembered answer with that id")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("answer approved from the web front", "id", id, "question", approved.Label)
	writeJSON(w, http.StatusOK, answerOf(*approved))
}

// statusNow is what GET /api/status and the event stream send.
func (s *server) statusNow(ctx context.Context) map[string]any {
	act, err := run.ReadActivity(s.d.DataDir)
	out := map[string]any{"running": act.Held, "kind": kindName(act.Kind), "holder": nil, "state": nil}
	if act.Held {
		out["holder"] = holderOf(act.Holder)
	}
	if act.Kind == run.Running || act.Kind == run.Waiting || act.Kind == run.Stale {
		st := stateJSON{RunID: act.State.RunID, Market: act.State.Market, DryRun: act.State.DryRun, Term: act.State.Term,
			Applied: act.State.Applied, External: act.State.External, Failed: act.State.Failed,
			Skipped: act.State.Skipped, WouldApply: act.State.WouldApply}
		if !act.State.NextRun.IsZero() {
			st.NextRun = act.State.NextRun.Format(time.RFC3339)
		}
		out["state"] = st
	}
	if !act.Held {
		_, _ = act.EndIfStale(ctx, s.d.Store, s.d.DataDir) // the last run, if it died, shows as ended
	}
	rs, lastErr := s.d.Store.ListRuns(5)
	out["last_runs"] = runsOf(rs)
	if err != nil || lastErr != nil {
		out["error"] = errors.Join(err, lastErr).Error()
	}
	return out
}

func kindName(k run.ActivityKind) string {
	switch k {
	case run.Starting:
		return "starting"
	case run.Running:
		return "running"
	case run.Waiting:
		return "waiting"
	case run.Stale:
		return "stale"
	}
	return "idle"
}

// configKeyView is one setting as the Config tab lists it; see GET /api/config.
type configKeyView struct {
	Key           string   `json:"key"`
	Kind          string   `json:"kind"`
	Value         string   `json:"value"`
	Doc           string   `json:"doc"`
	Options       []string `json:"options"`
	EnvOverride   bool     `json:"env_override"`
	DaemonRestart bool     `json:"daemon_restart"`
}

// configKind names a field's type for the front: enum for a string with a
// closed set of values, list for a string list.
func configKind(f config.Field) string {
	switch f.Kind {
	case reflect.Bool:
		return "bool"
	case reflect.Int:
		return "int"
	case reflect.Slice:
		return "list"
	case reflect.String:
		if len(f.Options) > 0 {
			return "enum"
		}
	}
	return "string"
}

// tildePath writes the home directory in p as ~.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home || strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// config lists every setting by section, as the TUI's Config tab does, with the
// value the config file holds. Secrets live in .env and are not listed.
func (s *server) config(w http.ResponseWriter, r *http.Request) {
	file, err := config.LoadFile(s.d.Paths)
	if file == nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	overridden := map[string]bool{}
	if eff, _ := config.Load(s.d.Paths, s.d.Env, nil); eff != nil {
		for _, key := range eff.Overrides {
			overridden[key] = true
		}
	}
	type sectionView struct {
		Name string          `json:"name"`
		Keys []configKeyView `json:"keys"`
	}
	sections := []sectionView{}
	for _, f := range config.Fields() {
		name, _, _ := strings.Cut(f.Key, ".")
		if len(sections) == 0 || sections[len(sections)-1].Name != name {
			sections = append(sections, sectionView{Name: name, Keys: []configKeyView{}})
		}
		value, _ := file.Config.Get(f.Key)
		options := f.Options
		if options == nil {
			options = []string{}
		}
		_, restart := config.ReadAtDaemonStart[f.Key]
		sec := &sections[len(sections)-1]
		sec.Keys = append(sec.Keys, configKeyView{Key: f.Key, Kind: configKind(f), Value: value, Doc: f.Doc,
			Options: options, EnvOverride: overridden[f.Key], DaemonRestart: restart})
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": tildePath(s.d.Paths.ConfigFile), "sections": sections})
}

// putConfig sets one key the way `ajai config set` does: the file alone is
// loaded, the whole file is validated, and it is written back with its comments.
func (s *server) putConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value *string `json:"value"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Value == nil {
		writeErr(w, http.StatusBadRequest, `the body must be {"value": "..."}`)
		return
	}
	key := r.PathValue("key")
	file, err := config.LoadFile(s.d.Paths)
	if file == nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !file.FileExisted {
		writeErr(w, http.StatusConflict, "there is no config file yet; run `ajai init` first")
		return
	}
	if _, err := file.Config.SetChecked(key, *body.Value); err != nil {
		var unknown *config.UnknownKeyError
		if errors.As(err, &unknown) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, "refusing to write: "+err.Error())
		return
	}
	if err := config.Write(s.d.Paths.ConfigFile, file.Config); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not write the config file: "+err.Error())
		return
	}
	saved, _ := file.Config.Get(key)
	s.log.Info("config set from the web front", "key", key, "value", saved)
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": saved, "applies": "next run"})
}
