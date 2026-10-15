package alert

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// The alerts file names secrets, so it is read like auth.json: private,
// strict, and checked against the topology before anything is sent.
func TestAlertsFileIsCheckedLikeAuth(t *testing.T) {
	c := testkit.Config()
	good := Webhook{ID: "phone", Format: "ntfy", URL: "https://ntfy.example/topic?auth=abc", Events: []string{"incidents"}, Observers: []string{"home"}, Bearer: "tk_123"}
	if e := Validate(Config{Schema: 1, Webhooks: []Webhook{good, {ID: "local", Format: "json", URL: "http://127.0.0.1:9000/hook"}}}, c); e != nil {
		t.Fatal("valid alerts refused:", e)
	}
	for name, change := range map[string]func(*Config){
		"schema":         func(a *Config) { a.Schema = 2 },
		"none":           func(a *Config) { a.Webhooks = nil },
		"too_many":       func(a *Config) { a.Webhooks = make([]Webhook, 17) },
		"id":             func(a *Config) { a.Webhooks[0].ID = "bad id" },
		"duplicate":      func(a *Config) { a.Webhooks = append(a.Webhooks, a.Webhooks[0]) },
		"format":         func(a *Config) { a.Webhooks[0].Format = "email" },
		"plain_http":     func(a *Config) { a.Webhooks[0].URL = "http://ntfy.example/topic" },
		"credentials":    func(a *Config) { a.Webhooks[0].URL = "https://user:pass@ntfy.example/topic" },
		"fragment":       func(a *Config) { a.Webhooks[0].URL = "https://ntfy.example/topic#x" },
		"scheme":         func(a *Config) { a.Webhooks[0].URL = "ftp://ntfy.example/topic" },
		"bearer":         func(a *Config) { a.Webhooks[0].Bearer = "two words" },
		"event":          func(a *Config) { a.Webhooks[0].Events = []string{"everything"} },
		"repeated_event": func(a *Config) { a.Webhooks[0].Events = []string{"incidents", "incidents"} },
		"observer":       func(a *Config) { a.Webhooks[0].Observers = []string{"nowhere"} },
	} {
		a := Config{Schema: 1, Webhooks: []Webhook{good}}
		change(&a)
		if Validate(a, c) == nil {
			t.Fatalf("%s: invalid alerts accepted", name)
		}
	}
	insecure := good
	insecure.URL, insecure.AllowInsecure = "http://ntfy.lan/topic", true
	if e := Validate(Config{Schema: 1, Webhooks: []Webhook{insecure}}, c); e != nil {
		t.Fatal("plain HTTP with consent refused:", e)
	}
	path := filepath.Join(t.TempDir(), "alerts.json")
	b, _ := json.Marshal(Config{Schema: 1, Webhooks: []Webhook{good}})
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if a, e := Load(path, c); e != nil || a.Webhooks[0].ID != "phone" {
		t.Fatal("private alerts file refused:", e)
	}
	if runtime.GOOS != "windows" {
		os.Chmod(path, 0644)
		if _, e := Load(path, c); e == nil {
			t.Fatal("an alerts file others can read accepted")
		}
		os.Chmod(path, 0600)
	}
	os.WriteFile(path, []byte(`{"schema":1,"webhooks":[],"extra":true}`), 0600)
	if _, e := Load(path, c); e == nil {
		t.Fatal("unknown fields accepted")
	}
}

// received is what a webhook was sent.
type received struct {
	header http.Header
	body   []byte
}

// endpoint is a webhook that answers with the statuses given, in turn, then
// 200, and keeps what it was sent.
type endpoint struct {
	mu       sync.Mutex
	got      []received
	statuses []int
	hits     atomic.Int32
	srv      *httptest.Server
}

func newEndpoint(t *testing.T, statuses ...int) *endpoint {
	e := &endpoint{statuses: statuses}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		defer e.mu.Unlock()
		status := 200
		if len(e.statuses) > 0 {
			status, e.statuses = e.statuses[0], e.statuses[1:]
		}
		if status == 302 {
			w.Header().Set("Location", "/elsewhere")
		}
		if status == 200 {
			e.got = append(e.got, received{r.Header.Clone(), b})
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *endpoint) received() []received {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]received(nil), e.got...)
}

// failing judges three failures of home-http, as a collector would.
func failing(t *testing.T) (model.Snapshot, []ledger.Change) {
	t.Helper()
	c := testkit.Config()
	c.Observers[0].Name = "Home <!channel> @everyone"
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	rows := []model.Observation{}
	for i := uint64(1); i <= 3; i++ {
		rows = append(rows, testkit.Observation(c, "home-http", i, now.Add(-time.Duration(4-i)*time.Second), "fail"))
	}
	snap := engine.Build(c, "primary", "", rows, nil, now)
	changes, e := ledger.New().Observe(snap, now, true)
	if e != nil || len(changes) != 1 {
		t.Fatal(changes, e)
	}
	return snap, changes
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func wait(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

// Each format says the same thing as its service expects it: JSON as
// data, ntfy as text with an encoded title and a priority, Slack with its
// markup escaped, Discord mentioning no one; a bearer goes along.
func TestWebhookFormats(t *testing.T) {
	snap, changes := failing(t)
	endpoints := map[string]*endpoint{}
	hooks := []Webhook{}
	for _, f := range Formats {
		endpoints[f] = newEndpoint(t)
		hooks = append(hooks, Webhook{ID: f, Format: f, URL: endpoints[f].srv.URL + "/hook?token=secret", Bearer: "tk_" + f})
	}
	n := &Notifier{Project: "lab", Instance: "primary"}
	n.Configure(Config{Schema: 1, Webhooks: hooks})
	defer n.Close()
	n.Observe(snap, changes, false)
	for _, f := range Formats {
		wait(t, func() bool { return len(endpoints[f].received()) == 1 })
		got := endpoints[f].received()[0]
		if got.header.Get("Authorization") != "Bearer tk_"+f || !strings.HasPrefix(got.header.Get("User-Agent"), "Topophany/") {
			t.Fatalf("%s: headers %v", f, got.header)
		}
		body := string(got.body)
		switch f {
		case "json":
			var e struct {
				Schema    int
				Event     string
				Project   string
				Collector string
				Title     string
				Incident  model.IncidentRecord
			}
			if json.Unmarshal(got.body, &e) != nil || e.Schema != 1 || e.Event != "incident.opened" || e.Project != "lab" || e.Collector != "primary" || e.Incident.ID != changes[0].Record.ID || !strings.HasPrefix(e.Title, "✖ Incident: Home") {
				t.Fatalf("json: %s", body)
			}
		case "ntfy":
			title, err := new(mime.WordDecoder).DecodeHeader(got.header.Get("Title"))
			if err != nil || !strings.HasPrefix(title, "✖ Incident: Home <!channel>") || got.header.Get("Priority") != "high" || got.header.Get("Tags") != "rotating_light" || !strings.Contains(body, "Checks: home-http") || !strings.Contains(body, "Collector: primary · lab") {
				t.Fatalf("ntfy: %q %v\n%s", title, got.header, body)
			}
		case "slack":
			var m map[string]string
			if json.Unmarshal(got.body, &m) != nil || strings.Contains(m["text"], "<!channel>") || !strings.Contains(m["text"], "&lt;!channel&gt;") {
				t.Fatalf("slack: %s", body)
			}
		case "discord":
			var m struct {
				Content  string
				Mentions struct{ Parse []string } `json:"allowed_mentions"`
			}
			if json.Unmarshal(got.body, &m) != nil || m.Mentions.Parse == nil || len(m.Mentions.Parse) != 0 || !strings.Contains(m.Content, "**✖ Incident: Home") {
				t.Fatalf("discord: %s", body)
			}
		}
	}
}

// An alert a webhook fails to take is tried again; one it refuses, or
// answers with a redirect, which is not followed, is dropped; one it never
// takes is dropped once it has waited too long. The log names the
// webhook, never its URL.
func TestDeliveryRetriesAndDrops(t *testing.T) {
	snap, changes := failing(t)
	flaky := newEndpoint(t, 503, 500, 429)
	refusing := newEndpoint(t, 404)
	moved := newEndpoint(t, 302)
	never := make([]int, 10000)
	for i := range never {
		never[i] = 503
	}
	down := newEndpoint(t, never...)
	l := &logs{}
	n := &Notifier{Project: "lab", Instance: "primary", Log: l.log, Backoff: func(int) time.Duration { return time.Millisecond }, MaxAge: 300 * time.Millisecond}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{
		{ID: "flaky", Format: "json", URL: flaky.srv.URL + "/a?key=hidden"},
		{ID: "refusing", Format: "json", URL: refusing.srv.URL},
		{ID: "moved", Format: "json", URL: moved.srv.URL},
		{ID: "down", Format: "json", URL: down.srv.URL + "/b?key=hidden"},
	}})
	defer n.Close()
	n.Observe(snap, changes, false)
	wait(t, func() bool { return len(flaky.received()) == 1 })
	wait(t, func() bool { return strings.Count(l.String(), "\n") >= 2 })
	if flaky.hits.Load() != 4 || refusing.hits.Load() != 1 || moved.hits.Load() != 1 || down.hits.Load() < 2 {
		t.Fatalf("tries: flaky %d, refusing %d, moved %d, down %d", flaky.hits.Load(), refusing.hits.Load(), moved.hits.Load(), down.hits.Load())
	}
	log := l.String()
	for _, want := range []string{"webhook refusing refused with HTTP 404; dropped", "webhook moved refused with HTTP 302; dropped", "to webhook down dropped after waiting", "last HTTP 503"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "hidden") || strings.Contains(log, "127.0.0.1") {
		t.Fatalf("log leaks a webhook URL:\n%s", log)
	}
}

// Observers are told of once they settle: one silent, once, and again
// when it reports, only if it was told of as silent. A webhook takes only
// the events and observers it names.
func TestObserverAlertsAndFilters(t *testing.T) {
	c := testkit.Config()
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	all, cloudOnly := newEndpoint(t), newEndpoint(t)
	n := &Notifier{Project: "lab", Instance: "primary"}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{
		{ID: "all", Format: "json", URL: all.srv.URL},
		{ID: "cloud", Format: "json", URL: cloudOnly.srv.URL, Events: []string{"observers"}, Observers: []string{"cloud"}},
	}})
	defer n.Close()
	beat := func(observers ...string) map[string]model.Heartbeat {
		m := map[string]model.Heartbeat{}
		for _, o := range observers {
			m[o] = model.Heartbeat{Project: c.Project, Observer: o, Boot: "b", SampledAt: now.Add(-time.Second)}
		}
		return m
	}
	n.Observe(engine.Build(c, "primary", "", nil, beat(), now), nil, false)
	n.Observe(engine.Build(c, "primary", "", nil, beat("home"), now), nil, true)
	n.Observe(engine.Build(c, "primary", "", nil, beat("home"), now), nil, true)
	n.Observe(engine.Build(c, "primary", "", nil, beat("home", "cloud"), now), nil, true)
	wait(t, func() bool { return len(all.received()) == 2 && len(cloudOnly.received()) == 2 })
	events := func(e *endpoint) string {
		out := []string{}
		for _, r := range e.received() {
			var m struct {
				Event    string
				Observer model.ObserverState
			}
			json.Unmarshal(r.body, &m)
			out = append(out, m.Event+":"+m.Observer.ID)
		}
		return strings.Join(out, " ")
	}
	if got := events(all); got != "observer.silent:cloud observer.back:cloud" {
		t.Fatalf("all: %s", got)
	}
	if got := events(cloudOnly); got != "observer.silent:cloud observer.back:cloud" {
		t.Fatalf("cloud: %s", got)
	}
	snap, changes := failing(t)
	n.Observe(snap, changes, false)
	wait(t, func() bool { return len(all.received()) == 3 })
	time.Sleep(50 * time.Millisecond)
	if len(cloudOnly.received()) != 2 {
		t.Fatal("a webhook for cloud's observers got home's incident")
	}
}

// A reload keeps what an unchanged webhook has queued and drops what a
// removed one has; a webhook that cannot keep up drops what overflows.
func TestReconfigureAndOverflow(t *testing.T) {
	snap, changes := failing(t)
	release := make(chan struct{})
	var hits atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
	}))
	defer slow.Close()
	defer close(release)
	l := &logs{}
	n := &Notifier{Project: "lab", Instance: "primary", Log: l.log}
	hook := Webhook{ID: "slow", Format: "json", URL: slow.URL}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{hook}})
	defer n.Close()
	for range queueSize + 3 {
		n.Observe(snap, changes, false)
	}
	wait(t, func() bool { return strings.Contains(l.String(), "already wait") })
	kept := n.hooks["slow"]
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{hook}})
	if n.hooks["slow"] != kept {
		t.Fatal("an unchanged webhook was replaced")
	}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{{ID: "other", Format: "json", URL: slow.URL}}})
	if n.hooks["slow"] != nil || len(n.hooks) != 1 {
		t.Fatal("a removed webhook is kept")
	}
}

// One observer's alerts cannot crowd another's out of a webhook it shares:
// a worker can make as many incidents as it likes for its own observer,
// so a full queue gives way to an observer with fewer alerts waiting, at
// the cost of the newest of whoever has the most.
func TestAnObserverCannotCrowdOutAnother(t *testing.T) {
	c := testkit.Config()
	snap, changes := failing(t)
	release := make(chan struct{})
	var mu sync.Mutex
	got := []string{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		var m struct {
			Event    string
			Observer model.ObserverState
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		got = append(got, m.Event+":"+m.Observer.ID)
		mu.Unlock()
	}))
	defer hook.Close()
	l := &logs{}
	n := &Notifier{Project: "lab", Instance: "primary", Log: l.log}
	n.Configure(Config{Schema: 1, Webhooks: []Webhook{{ID: "shared", Format: "json", URL: hook.URL}}})
	defer n.Close()
	for range queueSize + 3 {
		n.Observe(snap, changes, false)
	}
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	home := map[string]model.Heartbeat{"home": {Project: c.Project, Observer: "home", Boot: "b", SampledAt: now.Add(-time.Second)}}
	n.Observe(engine.Build(c, "primary", "", nil, home, now), nil, true)
	n.mu.Lock()
	waiting := n.queues["shared"].Waiting
	last := waiting[len(waiting)-1]
	n.mu.Unlock()
	if len(waiting) != queueSize || last.Event.Kind != "observer.silent" || last.Observer != "cloud" {
		t.Fatal("cloud's alert did not get into the queue home filled", len(waiting), last.Event.Kind, last.Observer)
	}
	if !strings.Contains(l.String(), "alert incident.opened from observer home to webhook shared dropped for one from observer cloud") {
		t.Fatal(l.String())
	}
	close(release)
	wait(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == queueSize
	})
	if got[queueSize-1] != "observer.silent:cloud" {
		t.Fatal(got[queueSize-1])
	}
}
