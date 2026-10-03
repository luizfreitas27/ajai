package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/markets"
	"github.com/luizfreitas27/ajai/internal/questions"
)

// fakeModel is an OpenAI-compatible chat endpoint that answers with reply and
// records what it was sent.
type fakeModel struct {
	mu      sync.Mutex
	reply   string
	refusal string
	status  int
	bodies  []map[string]any
	auth    []string
	headers []http.Header
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	f.bodies = append(f.bodies, body)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.headers = append(f.headers, r.Header.Clone())
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = fmt.Fprint(w, `{"error":{"message":"boom","type":"server_error"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": f.reply, "refusal": f.refusal})
	_, _ = fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":%s,"finish_reason":"stop"}]}`, msg)
}

func (f *fakeModel) prompt(t *testing.T) string {
	t.Helper()
	msgs := f.bodies[len(f.bodies)-1]["messages"].([]any)
	return msgs[0].(map[string]any)["content"].(string)
}

func client(t *testing.T, model *fakeModel, key string) (*Client, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)
	var log bytes.Buffer
	c, err := New(Settings{BaseURL: srv.URL + "/v1/", Model: "gpt-test", APIKey: key, Profile: "Name: Jane",
		Logger: slog.New(slog.NewTextHandler(&log, nil)), MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	return c, &log
}

func TestAnOptionIsAcceptedOnlyWhenNamedExactlyCaseAndQuotesTolerated(t *testing.T) {
	options := []string{"Yes", "No"}
	for raw, want := range map[string]string{
		"Yes": "Yes", "yes": "Yes", " YES ": "Yes", `"Yes"`: "Yes", "'No'": "No",
		"Yes, absolutely": "", "Maybe later": "", "Y": "", "": "",
	} {
		model := &fakeModel{reply: raw}
		c, log := client(t, model, "")
		got, err := c.Answer(context.Background(), "Do you have a driver's licence?", questions.Radio, options, "")
		if err != nil || got != want {
			t.Errorf("model said %q: got %q (%v), want %q", raw, got, err, want)
		}
		if want == "" && raw != "" && !strings.Contains(log.String(), "none of the options") {
			t.Errorf("a rejected paraphrase must be logged with the options:\n%s", log.String())
		}
	}
}

func TestATextAnswerIsTrimmedAndThePromptCarriesProfileQuestionOptionsAndJob(t *testing.T) {
	model := &fakeModel{reply: "  Go and Python.  \n"}
	c, _ := client(t, model, "")
	got, err := c.Answer(context.Background(), "Which languages?", questions.Text, nil, "We use Go.")
	if err != nil || got != "Go and Python." {
		t.Fatalf("%q %v", got, err)
	}
	p := model.prompt(t)
	for _, want := range []string{"Applicant information:\nName: Jane", "Question:\nWhich languages?", "Job description:\nWe use Go."} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if model.bodies[0]["model"] != "gpt-test" {
		t.Errorf("model %v", model.bodies[0]["model"])
	}
	if _, err := c.Answer(context.Background(), "Shift?", questions.Select, []string{"Day", "Night"}, ""); err != nil {
		t.Fatal(err)
	}
	if p := model.prompt(t); !strings.HasSuffix(p, "Answer with exactly one of these options:\n- Day\n- Night") {
		t.Errorf("%s", p)
	}
}

func TestARefusalAnEmptyReplyAndAServerErrorGiveNoAnswer(t *testing.T) {
	model := &fakeModel{refusal: "I can't help with that."}
	c, log := client(t, model, "")
	if got, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); got != "" || err != nil || !strings.Contains(log.String(), "refused") {
		t.Errorf("%q %v %s", got, err, log.String())
	}
	model = &fakeModel{reply: "   "}
	c, log = client(t, model, "")
	if got, err := c.Answer(context.Background(), "q?", questions.Select, []string{"Yes", "No"}, ""); got != "" || err != nil || !strings.Contains(log.String(), "empty answer") {
		t.Errorf("%q %v %s", got, err, log.String())
	}
	model = &fakeModel{status: 500}
	c, _ = client(t, model, "")
	if got, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); got != "" || err == nil {
		t.Errorf("%q %v", got, err)
	}
	if len(model.bodies) != 1 {
		t.Errorf("retries were disabled, got %d calls", len(model.bodies))
	}
}

func TestTheKeyIsSentOnlyWhenSetAndNeverOverPlainHTTPToAnotherHost(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-the-environment")
	t.Setenv("OPENAI_ORG_ID", "org-env")
	t.Setenv("OPENAI_PROJECT_ID", "proj-env")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Env-Secret: leaked")
	t.Setenv("OPENAI_BASE_URL", "https://elsewhere.example/v1")
	model := &fakeModel{reply: "ok"}
	c, _ := client(t, model, "")
	if _, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); err != nil {
		t.Fatal(err)
	}
	if model.auth[0] != "" {
		t.Errorf("a keyless provider sent %q (the environment's key must not leak)", model.auth[0])
	}
	for _, h := range []string{"Openai-Organization", "Openai-Project", "X-Env-Secret"} {
		if v := model.headers[0].Get(h); v != "" {
			t.Errorf("environment header %s leaked: %q", h, v)
		}
	}
	model = &fakeModel{reply: "ok"}
	c, _ = client(t, model, "sk-mine")
	if _, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); err != nil {
		t.Fatal(err)
	}
	if model.auth[0] != "Bearer sk-mine" {
		t.Errorf("loopback with a key: %q", model.auth[0])
	}
	if _, err := New(Settings{BaseURL: "http://ollama.lan:11434/v1/", Model: "m", APIKey: "sk-mine"}); !errors.Is(err, ErrKeyOverHTTP) {
		t.Errorf("%v", err)
	}
	// Without a key, a LAN server over plain HTTP is fine.
	lan := &fakeModel{reply: "ok"}
	srv := httptest.NewServer(lan)
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}}
	c, err := New(Settings{BaseURL: "http://ollama.lan:11434/v1/", Model: "m", HTTPClient: hc, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Answer(context.Background(), "q?", questions.Text, nil, ""); err != nil || got != "ok" || lan.auth[0] != "" {
		t.Errorf("%q %v %q", got, err, lan.auth)
	}
}

func TestFromConfigNeedsAKeyForHostedProvidersOnly(t *testing.T) {
	cases := []struct {
		enabled  bool
		provider string
		key      string
		baseURL  string
		wantNil  bool
		wantErr  error
		anyErr   bool
	}{
		{enabled: false, provider: "openai", wantNil: true},
		{enabled: true, provider: "openai", wantErr: ErrNoKey},
		{enabled: true, provider: "gemini", wantErr: ErrNoKey},
		{enabled: true, provider: "ollama"},
		{enabled: true, provider: "lmstudio"},
		{enabled: true, provider: "openai", key: "sk-x"},
		{enabled: true, provider: "custom", baseURL: "https://proxy.example/v1"},
		{enabled: true, provider: "custom", anyErr: true}, // no base_url
		{enabled: true, provider: "custom", key: "sk-x", baseURL: "http://ollama.lan/v1", wantErr: ErrKeyOverHTTP},
	}
	for _, tc := range cases {
		c := config.Defaults()
		c.AI.Enabled, c.AI.Provider, c.AI.BaseURL = tc.enabled, tc.provider, tc.baseURL
		cl, err := FromConfig(c, config.Secrets{AIAPIKey: tc.key}, markets.Resolve(c), nil)
		switch {
		case tc.wantErr != nil:
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%+v: %v", tc, err)
			}
		case tc.anyErr:
			if err == nil {
				t.Errorf("%+v: no error", tc)
			}
		case tc.wantNil:
			if cl != nil || err != nil {
				t.Errorf("%+v: %v %v", tc, cl, err)
			}
		default:
			if cl == nil || err != nil {
				t.Errorf("%+v: %v", tc, err)
			}
		}
	}
}

func TestFromConfigGivesTheModelTheCandidateProfile(t *testing.T) {
	model := &fakeModel{reply: "Go"}
	srv := httptest.NewServer(model)
	defer srv.Close()
	c := config.Defaults()
	c.Personals.FirstName, c.Personals.LastName = "Jane", "Applicant"
	c.Questions.Headline = "Backend engineer"
	c.AI.Enabled, c.AI.Provider, c.AI.BaseURL, c.AI.Model = true, "custom", srv.URL+"/v1/", "m"
	cl, err := FromConfig(c, config.Secrets{}, markets.Resolve(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Answer(context.Background(), "Favourite language?", questions.Text, nil, ""); err != nil {
		t.Fatal(err)
	}
	if p := model.prompt(t); !strings.Contains(p, "Name: Jane Applicant") || !strings.Contains(p, "Headline: Backend engineer") {
		t.Errorf("%s", p)
	}
}

func TestSensitiveQuestionsNeverReachTheModelEndToEnd(t *testing.T) {
	model := &fakeModel{reply: "Yes"}
	c, _ := client(t, model, "")
	cfg := config.Defaults()
	cfg.Questions.RequireVisa, cfg.Questions.LegallyAuthorized, cfg.Questions.Citizenship = "", "", ""
	cfg.Questions.DesiredSalary = 0
	cfg.Personals.Veteran, cfg.Personals.Disability = "", ""
	a := questions.New(cfg, nil, c, nil)
	for _, q := range []questions.Question{
		{Label: "Will you require visa sponsorship?", Kind: questions.Select, Options: []string{"Select an option", "Yes", "No"}, Current: "Select an option"},
		{Label: "Are you a protected veteran?", Kind: questions.Radio, Options: []string{"Yes", "No"}},
		{Label: "Do you have a disability?", Kind: questions.Radio, Options: []string{"Yes", "No"}},
		{Label: "Do you hold an active security clearance?", Kind: questions.Text},
		{Label: "Pretensão salarial", Kind: questions.Text},
		{Label: "Email address", Kind: questions.Text},
	} {
		a.Answer(q, questions.Job{})
	}
	if len(model.bodies) != 0 {
		t.Errorf("the model was asked %d sensitive question(s)", len(model.bodies))
	}
	a.Answer(questions.Question{Label: "Preferred shift", Kind: questions.Radio, Options: []string{"Day", "Night"}}, questions.Job{})
	if len(model.bodies) != 1 {
		t.Error("an ordinary question should reach the model")
	}
}
