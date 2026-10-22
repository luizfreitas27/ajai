// Package web is the local HTTP API behind `ajai serve`, which a web front
// follows and drives. It holds no logic of its own: each handler reads or
// writes through the packages the TUI and the CLI use (store, memory, messages,
// run, logging), with the same checks.
//
// The server listens on loopback only and has no authentication: anything on
// this machine that can reach the port can act as the user. Requests whose Host
// or Origin is not loopback are refused (a DNS-rebinding guard). Every body and
// response is JSON, except the event stream and the front's files.
//
// Endpoints (times are RFC 3339; a list is a JSON array unless noted):
//
//	GET  /api/status
//	     {"running": bool, "kind": "idle|starting|running|waiting|stale",
//	      "holder": {"pid", "origin", "from", "window", "started"} | null,
//	      "state": {"run_id", "market", "dry_run", "term", "applied", "external",
//	                "failed", "skipped", "would_apply", "next_run"} | null,
//	      "last_runs": [run], "error": "only when the lock or state could not be read"}
//	GET  /api/runs?limit=20
//	     [run] where run = {"id", "started_at", "ended_at", "market", "dry_run",
//	     "applied", "external", "failed", "skipped", "pending_answers", "reason"}
//	POST /api/runs  {"market": "brasil|internacional" (optional), "dry_run": bool}
//	     202 {"pid", "args"} | 400 bad market | 409 {"error", "holder"} while a run holds the lock
//	POST /api/runs/stop  {"pid": int, "started": RFC 3339}
//	     200 {"holder"} | 409 {"error", "holder"} when that run is not the one in progress
//	GET  /api/jobs?text=&offset=0&limit=50
//	     [job] where job = {"job_id", "outcome", "reason", "title", "company",
//	     "work_location", "work_style", "job_link", "external_link", "hr_name",
//	     "hr_link", "date_applied", "recorded_at", "run_id"}, newest first
//	GET  /api/messages?state=
//	     {"messages": [message], "queue_digest": string, "queued": int}
//	     message = {"id", "state", "job_id", "title", "company", "recipient_name",
//	     "recipient_url", "text", "digest", "error", "sent_at", "created_at"};
//	     queue_digest is the list digest of the queued messages (see approve-all)
//	POST /api/messages/{id}/approve  {"digest": string}
//	     200 {"id", "state"} | 404 | 409 when the text changed or the message is not queued
//	POST /api/messages/approve-all  {"digest": queue_digest}
//	     200 {"approved": int} | 409 when the queue changed since it was read
//	GET  /api/invites?state=&limit=50
//	     [invite] where invite = {"id", "state", "job_id", "title", "company",
//	     "recipient_name", "recipient_url", "error", "sent_at", "created_at"}
//	GET  /api/answers?state=pending|approved|  (or pending=1)
//	     [answer] where answer = {"id", "label", "kind", "answer", "source",
//	     "state", "uses", "options", "last_job_link", "updated_at", "digest"}
//	POST /api/answers/{id}/approve  {"digest": string, "answer": string (optional)}
//	     200 answer | 400 when the answer is not one of the options, or blank for a
//	     question with no answer | 404 | 409 when the answer changed since it was read
//	GET  /api/events
//	     text/event-stream: "event: log" with one log line per event, and
//	     "event: status" with the /api/status body every 2 seconds
//
// Any other path serves the front from its embedded files, falling back to
// index.html; an unknown /api path is a 404 {"error"}.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/store"
)

//go:embed dist
var dist embed.FS

// Launcher runs ajai args in a process of its own, which outlives the request.
type Launcher interface {
	Start(args []string) (pid int, exited <-chan error, err error)
}

// Deps is what the handlers read and write.
type Deps struct {
	DataDir  string // the run lock, the state file and the stop request
	LogDir   string // the day's log file, which the event stream follows
	Store    *store.Store
	Launcher Launcher
	Logger   *slog.Logger // nil: discard
	Now      func() time.Time
	Every    time.Duration // the status event interval; 0 means 2 seconds
}

type server struct {
	d     Deps
	every time.Duration
	log   *slog.Logger
}

// New returns the handler for the API and the front.
func New(d Deps) http.Handler {
	s := &server{d: d, every: d.Every, log: d.Logger}
	if s.every <= 0 {
		s.every = 2 * time.Second
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.d.Now == nil {
		s.d.Now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/runs", s.runs)
	mux.HandleFunc("POST /api/runs", s.startRun)
	mux.HandleFunc("POST /api/runs/stop", s.stopRun)
	mux.HandleFunc("GET /api/jobs", s.jobs)
	mux.HandleFunc("GET /api/messages", s.messages)
	mux.HandleFunc("POST /api/messages/approve-all", s.approveAll)
	mux.HandleFunc("POST /api/messages/{id}/approve", s.approveMessage)
	mux.HandleFunc("GET /api/invites", s.invites)
	mux.HandleFunc("GET /api/answers", s.answers)
	mux.HandleFunc("POST /api/answers/{id}/approve", s.approveAnswer)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	})
	mux.HandleFunc("/", s.front)
	return guard(mux)
}

// ValidAddr accepts only a loopback address: 127.0.0.0/8, ::1, or localhost.
func ValidAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("the address must be host:port, for example 127.0.0.1:7077")
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("ajai serve listens on loopback only (127.0.0.1, ::1 or localhost), not " + host)
}

// Serve listens on addr until ctx ends, then shuts down: open event streams end
// with it, and in-flight requests get five seconds.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	if err := ValidAddr(addr); err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
	}
	<-errc // ListenAndServe returns http.ErrServerClosed once Shutdown has begun
	return nil
}

// guard refuses a request whose Host or Origin is not loopback: a page on
// another site cannot reach the API through the user's browser.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopback(r.Host) {
			writeErr(w, http.StatusForbidden, "the Host must be a loopback address")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !loopback(u.Host) {
				writeErr(w, http.StatusForbidden, "the Origin must be a loopback address")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// loopback reports whether a host or host:port names this machine.
func loopback(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// front serves the embedded files; any other path gets index.html, so the
// front's own routes work on reload.
func (s *server) front(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	files := http.FileServerFS(sub)
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if fi, err := fs.Stat(sub, name); err != nil || fi.IsDir() {
		r = r.Clone(r.Context())
		r.URL.Path = "/"
	}
	files.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decode reads a JSON body into v; an empty body leaves v as it is.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "the body must be a JSON object: "+err.Error())
		return false
	}
	return true
}
