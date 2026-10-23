package direct

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A full profile retains 256 report-ID hashes per object as well as the
// latest report. At 256 objects, the supported adapters' 71-byte maximum
// sample IDs alone need over 10 MiB serialized. Reserve up to 128 KiB per
// object (64 KiB report plus cursor/history), while retaining hard caps.
const MaxStateBytes = 32 << 20
const MaxSourceStateBytes = 4 << 20
const MaxBatchBytes = 2 << 20
const MaxRetryNonces = 1024

type Collector func(context.Context, model.Config, model.Source, []source.Request) ([]source.Reading, time.Time, error)
type Options struct {
	Now                 func() time.Time
	Collect             Collector
	Backoff             func(int) time.Duration
	MaxStateBytes       int
	MaxSourceStateBytes int
}
type sourceLoop struct {
	renewed401 bool
	gate       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	generation string
	wake       chan struct{}
	attempted  bool
	failures   int
	next       time.Time
	lastRetry  time.Time
	state      string
	code       string
}

// Session is one profile's owned acquisition lifetime. Each source has one
// serial loop; at most four network collections run together. There is no
// result queue: the bounded atomic admission transaction finishes before
// that source is scheduled again. Only display frames may be coalesced.
type Session struct {
	workers    sync.WaitGroup
	mu         sync.Mutex
	profile    Profile
	config     model.Config
	store      *Store
	opts       Options
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	first      chan struct{}
	firstOnce  sync.Once
	loops      map[string]*sourceLoop
	external   *model.ExternalState
	prepared   *engine.Prepared
	plan       *engine.Plan
	generation string
	retrySeen  map[string]bool
	closed     bool
	high       time.Time
	slots      chan struct{}
}

func NewSession(ctx context.Context, store *Store, p Profile, opts Options) (*Session, error) {
	c, e := p.Config()
	if e != nil {
		return nil, e
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Backoff == nil {
		opts.Backoff = desktopBackoff
	}
	if opts.MaxStateBytes == 0 {
		opts.MaxStateBytes = MaxStateBytes
	}
	if opts.MaxSourceStateBytes == 0 {
		opts.MaxSourceStateBytes = MaxSourceStateBytes
	}
	if e := validateLocalTLS(c); e != nil {
		return nil, e
	}
	rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	if e = rt.Validate(c); e != nil {
		return nil, fail("profile_unavailable")
	}
	life, cancel := context.WithCancel(ctx)
	s := &Session{profile: cloneProfile(p), config: c, store: store, opts: opts, ctx: life, cancel: cancel, done: make(chan struct{}), first: make(chan struct{}), loops: map[string]*sourceLoop{}, retrySeen: map[string]bool{}, external: model.NewExternalState(), plan: engine.NewPlan(c), generation: newID(), slots: make(chan struct{}, 4)}
	s.prepared = s.plan.PrepareExternal(nil, nil, nil, s.external)
	if opts.Collect == nil {
		s.opts.Collect = s.collect
	}
	s.mu.Lock()
	for _, src := range c.Sources {
		s.startLoopLocked(src, s.generation, c, nil)
	}
	s.mu.Unlock()
	if len(c.Sources) == 0 {
		s.firstOnce.Do(func() { close(s.first) })
	}
	go func() {
		<-s.ctx.Done()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.workers.Wait()
		close(s.done)
	}()
	return s, nil
}
func desktopBackoff(failures int) time.Duration {
	base := min(5<<min(max(failures-1, 0), 4), 60)
	return time.Duration(float64(time.Duration(base)*time.Second) * (0.9 + rand.Float64()*0.1))
}
func (s *Session) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	s.mu.Unlock()
	<-s.done
}
func (s *Session) Wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-s.first:
	}
}
func (s *Session) Profile() Profile { s.mu.Lock(); defer s.mu.Unlock(); return cloneProfile(s.profile) }
func cloneProfile(p Profile) Profile {
	b, _ := json.Marshal(p)
	var out Profile
	_ = json.Unmarshal(b, &out)
	return out
}
func sourceKey(e Entry) string {
	src := e.Source
	src.Name = ""
	objects := append([]string(nil), e.Selected...)
	sort.Strings(objects)
	b, _ := json.Marshal([]any{src, e.Subject, objects})
	return string(b)
}
func (s *Session) UpdateProjection(p Profile) error { return s.Reconfigure(p) }

// Reconfigure validates the entire candidate locally before installing it.
// Only affected sources receive a new execution generation and cancellation.
func (s *Session) Reconfigure(p Profile) error {
	c, e := p.Config()
	if e != nil {
		return e
	}
	if e := validateLocalTLS(c); e != nil {
		return e
	}
	rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	if rt.Validate(c) != nil {
		return fail("profile_unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil || p.Profile != s.profile.Profile {
		return fail("profile_conflict")
	}
	next := model.NewExternalState()
	gates := map[string]chan struct{}{}
	for id, l := range s.loops {
		gates[id] = l.gate
	}
	loops := map[string]*sourceLoop{}
	for _, entry := range p.Sources {
		old, ok := findEntry(s.profile, entry.Source.ID)
		l := s.loops[entry.Source.ID]
		if ok && l != nil && sourceKey(old) == sourceKey(entry) {
			loops[entry.Source.ID] = l
			next.Sources[entry.Source.ID] = s.external.Sources[entry.Source.ID]
			for _, x := range c.Checks {
				if x.External != nil && x.External.Source == entry.Source.ID {
					if state, ok := s.external.Bindings[x.ID]; ok && state.Contract == config.Contract(c, x) {
						next.Bindings[x.ID] = model.CloneBindingState(state)
					}
				}
			}
			if old.Credential != entry.Credential {
				l.renewed401 = false
			}
			if old.Credential != entry.Credential && !(l.code == "rate_limited" && l.next.After(s.opts.Now())) {
				select {
				case l.wake <- struct{}{}:
				default:
				}
			}
		}
	}
	for id, l := range s.loops {
		if loops[id] != l {
			l.cancel()
		}
	}
	s.profile = cloneProfile(p)
	s.config = c
	s.external = next
	s.plan = engine.NewPlan(c)
	s.prepared = s.plan.PrepareExternal(nil, nil, nil, next)
	s.loops = loops
	for _, src := range c.Sources {
		if s.loops[src.ID] == nil {
			s.startLoopLocked(src, newID(), c, gates[src.ID])
		}
	}
	return nil
}
func (s *Session) Retry(nonce string) error {
	if nonce == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.retrySeen[nonce] {
		return nil
	}
	if len(s.retrySeen) >= MaxRetryNonces {
		return fail("state_budget")
	}
	s.retrySeen[nonce] = true
	now := s.opts.Now()
	for _, l := range s.loops {
		if !l.lastRetry.IsZero() && now.Sub(l.lastRetry) < 5*time.Second {
			continue
		}
		if l.code == "rate_limited" && !l.next.IsZero() && now.Before(l.next) {
			continue
		}
		l.lastRetry = now
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
	return nil
}
func (s *Session) Snapshot(observer string, now time.Time) (model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if observer != "" && observer != Observer {
		return model.Snapshot{}, fail("invalid_request")
	}
	snap := s.prepared.Build("direct-"+s.generation, observer, now)
	info := &model.DirectInfo{Profile: s.profile.Profile, ProfileRevision: s.profile.Revision, DatasetID: s.profile.Dataset, Sources: []model.DirectSource{}}
	for _, e := range s.profile.Sources {
		l := s.loops[e.Source.ID]
		state := s.external.Sources[e.Source.ID]
		v := model.DirectSource{ID: e.Source.ID, Name: e.Source.Name, Adapter: e.Source.Adapter, State: l.state, LastAttemptAt: stamp(state.LastAttemptAt), LastContactAt: stamp(state.LastContact), NextRetryAt: stamp(l.next)}
		if l.code != "" {
			code := l.code
			v.Code = &code
		}
		if !state.LastContact.IsZero() {
			until := state.LastContact.Add(time.Duration(e.Source.Stale) * time.Second)
			v.ContactValidUntil = &until
		}
		info.Sources = append(info.Sources, v)
	}
	snap.Direct = info
	status, _ := (view.View{Snapshot: snap, Now: now}).SourcesStatus()
	snap.SourcesStatus = &status
	return snap, nil
}
func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}
func (s *Session) requests(src model.Source) ([]model.Check, []source.Request) {
	checks := []model.Check{}
	requests := []source.Request{}
	index := map[string]int{}
	for _, x := range s.config.Checks {
		if x.Kind != "external" || x.External == nil || x.External.Source != src.ID || x.Disabled || x.Maintenance {
			continue
		}
		checks = append(checks, x)
		i, ok := index[x.External.Object]
		if !ok {
			index[x.External.Object] = len(requests)
			requests = append(requests, source.Request{Object: x.External.Object, Reports: []string{x.External.Report}})
		} else {
			found := false
			for _, kind := range requests[i].Reports {
				found = found || kind == x.External.Report
			}
			if !found {
				requests[i].Reports = append(requests[i].Reports, x.External.Report)
			}
		}
	}
	return checks, requests
}
func (s *Session) startLoopLocked(src model.Source, generation string, c model.Config, gate chan struct{}) {
	if gate == nil {
		gate = make(chan struct{}, 1)
	}
	ctx, cancel := context.WithCancel(s.ctx)
	l := &sourceLoop{gate: gate, ctx: ctx, cancel: cancel, generation: generation, wake: make(chan struct{}, 1), state: "loading"}
	s.loops[src.ID] = l
	s.workers.Add(1)
	go func() { defer s.workers.Done(); s.run(src, l, c) }()
}
func requestsFor(c model.Config, src model.Source) ([]model.Check, []source.Request) {
	temp := Session{config: c}
	return temp.requests(src)
}
func (s *Session) run(src model.Source, l *sourceLoop, currentConfig model.Config) {
	_, requests := requestsFor(currentConfig, src)
	for {
		select {
		case <-l.ctx.Done():
			return
		case l.gate <- struct{}{}:
		}
		select {
		case <-l.ctx.Done():
			<-l.gate
			return
		case s.slots <- struct{}{}:
		}
		if l.ctx.Err() != nil {
			<-s.slots
			<-l.gate
			return
		}
		s.mu.Lock()
		l.next = time.Time{}
		s.mu.Unlock()
		started := s.opts.Now().UTC()
		ctx, cancel := context.WithTimeout(l.ctx, time.Duration(src.TimeoutSeconds)*time.Second)
		readings, retryAt, err := s.opts.Collect(ctx, currentConfig, src, requests)
		cancel()
		<-s.slots
		<-l.gate
		if l.ctx.Err() != nil {
			return
		}
		finished := s.opts.Now().UTC()
		if finished.Before(started) {
			finished = started
		}
		if e := s.Record(l.generation, src.ID, started, finished, readings, err); e != nil {
			err = e
			s.mu.Lock()
			if s.loops[src.ID] != l {
				s.mu.Unlock()
				return
			}
			s.external.Sources[src.ID] = report.MergeSource(s.external.Sources[src.ID], src.ID, finished, false, AsError(e).Code, 0)
			s.prepared = s.plan.PrepareExternal(nil, nil, nil, s.external)
			s.mu.Unlock()
		}
		s.mu.Lock()
		if s.loops[src.ID] != l || l.ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		if err == nil {
			l.state = "connected"
			l.code = ""
			l.failures = 0
			l.renewed401 = false
			l.next = finished.Add(time.Duration(src.IntervalSeconds) * time.Second)
		} else {
			l.failures++
			e := AsError(err)
			l.code = e.Code
			switch e.Code {
			case "auth_required":
				l.state = "auth_required"
			case "forbidden":
				l.state = "forbidden"
			case "connection_failed", "tls_error", "rate_limited", "credential_renewed":
				l.state = "retrying"
			default:
				l.state = "error"
			}
			if l.state == "retrying" {
				l.next = finished.Add(s.opts.Backoff(l.failures))
				if retryAt.After(l.next) {
					l.next = retryAt
				}
			} else {
				l.next = time.Time{}
			}
		}
		l.attempted = true
		all := true
		for _, v := range s.loops {
			all = all && v.attempted
		}
		if all {
			s.firstOnce.Do(func() { close(s.first) })
		}
		next := l.next
		s.mu.Unlock()
		if next.IsZero() {
			select {
			case <-l.ctx.Done():
				return
			case <-l.wake:
			}
		} else {
			timer := time.NewTimer(max(time.Millisecond, next.Sub(s.opts.Now())))
			select {
			case <-l.ctx.Done():
				timer.Stop()
				return
			case <-l.wake:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

// Record atomically admits a whole successful source batch. Failed adapter
// reads never commit their successful prefix. It validates ownership and the
// execution generation before any state mutation and sets real ReceivedAt.
func (s *Session) Record(generation, id string, started, finished time.Time, readings []source.Reading, collectErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil || s.loops[id] == nil || generation != s.loops[id].generation {
		return fail("profile_conflict")
	}
	src, ok := config.Source(s.config, id)
	if !ok {
		return fail("source_not_found")
	}
	checks, _ := s.requests(src)
	received := s.opts.Now().UTC()
	if received.After(s.high) {
		s.high = received
	}
	next := model.CloneExternalState(s.external)
	code := ""
	if collectErr != nil {
		code = AsError(collectErr).Code
		readings = nil
	}
	if len(readings) > 40 {
		return fail("state_budget")
	}
	reports := map[source.Key]model.ExternalReport{}
	batchBytes := 0
	for _, r := range readings {
		key := source.Key{Object: r.Object, Report: report.PayloadType(r.Report)}
		if _, exists := reports[key]; exists {
			return fail("upstream_incompatible")
		}
		reports[key] = r.Report
		b, e := json.Marshal(r.Report)
		if e != nil {
			return fail("state_budget")
		}
		batchBytes += len(b)
		if batchBytes > MaxBatchBytes {
			return fail("state_budget")
		}
	}
	for _, x := range checks {
		o := model.Observation{Project: s.config.Project, Observer: x.Observer, Check: x.ID, Contract: config.Contract(s.config, x), SampledAt: started, ReceivedAt: received, Outcome: "error", Code: code, Evidence: "reported", Stage: "external_source", PathEvidence: "external_source", DurationMS: max(0, finished.Sub(started).Milliseconds()), External: &model.ExternalObservation{Source: id, SourceGeneration: generation, AttemptedAt: finished, Contact: collectErr == nil}}
		if r, ok := reports[source.Key{Object: x.External.Object, Report: x.External.Report}]; ok && collectErr == nil {
			o.External.Report = &r
			o.Outcome = "ok"
			o.Code = ""
		} else if o.Code == "" {
			o.Code = "external_object_missing"
		}
		if o.Project != s.config.Project || o.Observer != src.Observer || o.Contract != config.Contract(s.config, x) {
			return fail("upstream_incompatible")
		}
		normalized, e := admission.External(o, x, src, admission.Window{Now: received, Future: s.high, MaxAge: 24 * time.Hour, Skew: 30 * time.Second})
		if e != nil {
			return fail("upstream_incompatible")
		}
		if normalized != nil {
			state := next.Bindings[x.ID]
			state.FailureThreshold = x.FailureThreshold
			state.RecoveryThreshold = x.RecoveryThreshold
			state.PolicyThreshold = x.PolicyThreshold
			state, _ = report.MergeBinding(x.ID, config.Contract(s.config, x), *x.External, state, *normalized, received)
			next.Bindings[x.ID] = state
		}
	}
	// Reject undeclared result pairs instead of retaining arbitrary upstream keys.
	for key := range reports {
		found := false
		for _, x := range checks {
			found = found || x.External.Object == key.Object && x.External.Report == key.Report
		}
		if !found {
			return fail("upstream_incompatible")
		}
	}
	next.Sources[id] = report.MergeSource(next.Sources[id], id, finished, collectErr == nil, code, max(0, finished.Sub(started).Milliseconds()))
	b, e := json.Marshal(next)
	sourceBytes := 0
	for _, state := range next.Bindings {
		if state.Source == id {
			encoded, _ := json.Marshal(state)
			sourceBytes += len(encoded)
		}
	}
	limit := s.opts.MaxSourceStateBytes
	if len(s.config.Sources) <= 1 {
		limit = s.opts.MaxStateBytes
	}
	if e != nil || len(b) > s.opts.MaxStateBytes || sourceBytes > limit || len(next.Bindings) > 512 || len(next.Sources) > 64 {
		return fail("state_budget")
	}
	s.external = next
	s.prepared = s.plan.PrepareExternal(nil, nil, nil, next)
	return nil
}

type attemptIO struct {
	now func() time.Time
	source.IO
	failure error
	retry   time.Time
}

func (a *attemptIO) Get(ctx context.Context, s model.Source, u string, headers map[string]string, max int64) (source.Response, error) {
	r, e := a.IO.Get(ctx, s, u, headers, max)
	if e != nil {
		code := "upstream_incompatible"
		if source.Code(e) == "transport_error" || ctx.Err() != nil {
			code = "connection_failed"
		}
		var transport *source.Error
		if source.Code(e) == "source_tls" || errors.As(e, &transport) && transport.Transport == "tls_error" {
			code = "tls_error"
		}
		a.failure = fail(code)
		return r, e
	}
	switch r.Status {
	case 401:
		a.failure = fail("auth_required")
	case 403:
		a.failure = fail("forbidden")
	case 429:
		a.failure = fail("rate_limited")
		a.retry = retryAfter(r.RetryAfter, a.now())
	case 500, 502, 503, 504:
		a.failure = fail("connection_failed")
	}
	return r, nil
}
func (s *Session) collect(ctx context.Context, c model.Config, src model.Source, requests []source.Request) ([]source.Reading, time.Time, error) {
	s.mu.Lock()
	entry, ok := findEntry(s.profile, src.ID)
	profile := s.profile.Profile
	s.mu.Unlock()
	if !ok || entry.Source.URL != src.Target {
		return nil, time.Time{}, fail("source_not_found")
	}
	token, e := s.store.Token(ctx, profile, entry, s.opts.Now())
	if e != nil {
		return nil, AsError(e).RetryAt, e
	}
	var io *attemptIO
	rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	rt.NewIO = func(c model.Config, src model.Source) (source.IO, error) {
		base, e := probe.SourceIO(c, src, func(context.Context, model.Source) (string, error) { return token, nil })
		if e != nil {
			return nil, e
		}
		io = &attemptIO{IO: base, now: s.opts.Now}
		return io, nil
	}
	readings, e := rt.Collect(ctx, c, src, requests)
	if io != nil && io.failure != nil {
		if AsError(io.failure).Code == "auth_required" {
			s.mu.Lock()
			l := s.loops[src.ID]
			canRefresh := l != nil && !l.renewed401
			if canRefresh {
				l.renewed401 = true
			}
			s.mu.Unlock()
			if canRefresh {
				if _, err := s.store.Refresh(ctx, profile, entry, token); err != nil {
					return nil, AsError(err).RetryAt, err
				}
				return nil, time.Time{}, &Error{Code: "credential_renewed", Message: "Credential renewed; retrying the read.", Retryable: true}
			}
		}
		return nil, io.retry, io.failure
	}
	if e != nil {
		if source.Code(e) == "source_tls" {
			return nil, time.Time{}, fail("tls_error")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || source.Code(e) == "transport_error" {
			return nil, time.Time{}, fail("connection_failed")
		}
		return nil, time.Time{}, fail("upstream_incompatible")
	}
	return readings, time.Time{}, nil
}

// LoadingSnapshot prepares topology without waiting for or racing the first read.
func LoadingSnapshot(p Profile, observer string, now time.Time) model.Snapshot {
	c, _ := p.Config()
	snap := engine.NewPlan(c).PrepareExternal(nil, nil, nil, model.NewExternalState()).Build("direct-loading", observer, now)
	info := &model.DirectInfo{Profile: p.Profile, ProfileRevision: p.Revision, DatasetID: p.Dataset, Sources: []model.DirectSource{}}
	for _, entry := range p.Sources {
		info.Sources = append(info.Sources, model.DirectSource{ID: entry.Source.ID, Name: entry.Source.Name, Adapter: entry.Source.Adapter, State: "loading"})
	}
	snap.Direct = info
	status, _ := (view.View{Snapshot: snap, Now: now}).SourcesStatus()
	snap.SourcesStatus = &status
	return snap
}
func (s *Session) RememberNonce(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nonce != "" {
		s.retrySeen[nonce] = true
	}
}
func (s *Session) CheckRetry(nonce string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if nonce != "" && !s.retrySeen[nonce] && len(s.retrySeen) >= MaxRetryNonces {
		return fail("state_budget")
	}
	return nil
}

func validateLocalTLS(c model.Config) error {
	for _, src := range c.Sources {
		if src.CAFile != "" {
			u, e := url.Parse(src.Target)
			if e != nil {
				return fail("profile_unavailable")
			}
			if _, e = probe.TLSConfig(u.Hostname(), src.CAFile); e != nil {
				return fail("tls_error")
			}
		}
	}
	return nil
}
