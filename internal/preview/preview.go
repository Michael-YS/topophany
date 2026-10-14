// Package preview feeds the TUI a built-in topology with synthetic evidence so
// rendering can be exercised without collectors, workers or network access.
// Every snapshot is built by the real engine from a simulated observation
// history; scenarios change what that history says, never how it is judged.
package preview

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/rollup"
)

//go:embed topology.json
var preset []byte

// Preset returns the built-in complex topology (Changsha home, Hangzhou
// relay, LA line + residential exits, Hawaii exit, New York, Tokyo, Frankfurt,
// and Cloudflare, Vercel and Supabase at the edge), written in lang.
func Preset(lang string) (model.Config, error) {
	b, e := PresetJSON(lang)
	if e != nil {
		return model.Config{}, e
	}
	return config.Decode(bytes.NewReader(b))
}

// PresetJSON is the reviewed source of Preset in lang, for `topo preview
// --print-topology`.
func PresetJSON(lang string) ([]byte, error) { return localize(preset, lang) }

// Scenario is one simulated world state. Node and observer IDs that are not in
// the topology make a scenario unavailable rather than silently empty.
type Scenario struct {
	ID          string            // its name and detail are scenario.ID and scenario.ID.detail in the catalogs
	Down        []string          // nodes that answer nothing (power/host failure)
	Blocked     []string          // nodes unreachable over public networks from mainland-China observers
	Offline     []string          // observers whose worker stops reporting
	Maintenance []string          // nodes whose checks are put in maintenance
	WrongExit   map[string]string // exit node → observed exit IP that violates policy
	Lossy       float64           // failure probability on cross-border samples
	Slow        float64           // latency multiplier on cross-border samples
	Chaos       bool              // random 30-second outage windows everywhere
	Skew        map[string]time.Duration
	Unreachable bool // the collector itself cannot be reached
}

var scenarios = []Scenario{
	{ID: "normal"},
	{ID: "dmit-blocked", Blocked: []string{"dmit"}},
	{ID: "dmit-down", Down: []string{"dmit"}},
	{ID: "aaitr-down", Down: []string{"aaitr"}},
	{ID: "aaitr-exit-drift", WrongExit: map[string]string{"aaitr": "203.0.113.99"}},
	{ID: "cross-border-loss", Lossy: .35, Slow: 2.6},
	{ID: "home-offline", Down: []string{"win", "mac", "router"}, Offline: []string{"cs-win", "cs-mac"}},
	{ID: "relay-down", Down: []string{"hz"}, Offline: []string{"obs-hz"}},
	{ID: "s2-maintenance", Maintenance: []string{"s2"}},
	{ID: "tokyo-clock-skew", Skew: map[string]time.Duration{"obs-jp": 40 * time.Second}},
	{ID: "s1-origin-down", Down: []string{"s1"}, Offline: []string{"obs-s1"}},
	{ID: "edge-blocked", Blocked: []string{"cf"}},
	{ID: "chaos", Chaos: true},
	{ID: "collector-unreachable", Unreachable: true},
}

type change struct {
	at time.Time
	sc int
}

// Sim is safe for concurrent use; the TUI fetches from a goroutine.
type Sim struct {
	mu        sync.Mutex
	base      model.Config
	available []int // indexes into scenarios usable with this topology
	timeline  []change
	where     map[string]*model.Location
	nodes     map[string]model.Node
	ends      map[string]model.Endpoint
	paths     map[string]model.Path
	services  map[string]model.Service
	obsNode   map[string]string
	ledger    *ledger.Ledger    // what a collector watching all along would remember
	observed  time.Time         // when the ledger last judged the evidence
	text      map[string]string // the catalog the scenarios are named from
	rolled    *rollup.Store     // what its rollups would hold, as of rolledAt
	rolledAt  time.Time
	contracts map[string]string // of the checks; scenarios change none
}

// New simulates c from now on, naming the scenarios in lang (one of
// Languages; any other names them in DefaultLanguage).
func New(c model.Config, now time.Time, lang string) *Sim {
	s := &Sim{base: c, where: map[string]*model.Location{}, nodes: map[string]model.Node{}, ends: map[string]model.Endpoint{}, paths: map[string]model.Path{}, services: map[string]model.Service{}, obsNode: map[string]string{}, ledger: ledger.New(), contracts: config.Contracts(c)}
	var e error
	if s.text, e = catalog(lang); e != nil {
		s.text, _ = catalog(DefaultLanguage)
	}
	for _, n := range c.Nodes {
		s.nodes[n.ID] = n
	}
	for _, n := range c.Nodes {
		for p, seen := n, 0; seen < len(c.Nodes); seen++ {
			if p.Location != nil {
				s.where[n.ID] = p.Location
				break
			}
			if p = s.nodes[p.Parent]; p.ID == "" {
				break
			}
		}
	}
	for _, e := range c.Endpoints {
		s.ends[e.ID] = e
	}
	for _, p := range c.Paths {
		s.paths[p.ID] = p
	}
	for _, v := range c.Services {
		s.services[v.ID] = v
	}
	observers := map[string]bool{}
	for _, o := range c.Observers {
		s.obsNode[o.ID] = o.Node
		observers[o.ID] = true
	}
	for i, sc := range scenarios {
		ok := true
		for _, id := range append(append(append(append([]string{}, sc.Down...), sc.Blocked...), sc.Maintenance...), keys(sc.WrongExit)...) {
			ok = ok && s.nodes[id].ID != ""
		}
		for _, id := range append(append([]string{}, sc.Offline...), keysD(sc.Skew)...) {
			ok = ok && observers[id]
		}
		if ok {
			s.available = append(s.available, i)
		}
	}
	s.timeline = []change{{now.Add(-24 * time.Hour), 0}}
	return s
}

func keys(m map[string]string) []string {
	r := []string{}
	for k := range m {
		r = append(r, k)
	}
	return r
}
func keysD(m map[string]time.Duration) []string {
	r := []string{}
	for k := range m {
		r = append(r, k)
	}
	return r
}

// Scenarios lists the scenarios usable with this topology as "name\tdetail".
func (s *Sim) Scenarios() []string {
	r := []string{}
	for _, i := range s.available {
		id := scenarios[i].ID
		r = append(r, s.say("scenario."+id, id)+"\t"+s.say("scenario."+id+".detail", ""))
	}
	return r
}

// ScenarioIDs lists the same scenarios by the IDs that name them in every
// language.
func (s *Sim) ScenarioIDs() []string {
	r := []string{}
	for _, i := range s.available {
		r = append(r, scenarios[i].ID)
	}
	return r
}

// say is the text of key in the simulator's language, or otherwise.
func (s *Sim) say(key, otherwise string) string {
	if t, ok := s.text[key]; ok {
		return t
	}
	return otherwise
}

// Scenario is the index (into Scenarios) currently in effect.
func (s *Sim) Scenario() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.timeline[len(s.timeline)-1].sc
	for i, v := range s.available {
		if v == cur {
			return i
		}
	}
	return 0
}

// SetScenario switches the simulated world at time at. History before at
// keeps the previous scenario, so thresholds, staleness and recovery play out
// exactly as they would after a real change.
func (s *Sim) SetScenario(i int, at time.Time) {
	if i < 0 || i >= len(s.available) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.timeline) > 1 && !s.timeline[len(s.timeline)-1].at.Before(at) {
		s.timeline = s.timeline[:len(s.timeline)-1]
	}
	s.timeline = append(s.timeline, change{at, s.available[i]})
	if at.Before(s.observed) {
		// The past the ledger judged is rewritten; it judges it again.
		s.ledger, s.observed = ledger.New(), time.Time{}
	}
	if at.Before(s.rolledAt) {
		s.rolled = nil
	}
	if len(s.timeline) > 64 {
		s.timeline = s.timeline[len(s.timeline)-64:]
	}
}

func (s *Sim) at(t time.Time) Scenario {
	for i := len(s.timeline) - 1; i >= 0; i-- {
		if !s.timeline[i].at.After(t) {
			return scenarios[s.timeline[i].sc]
		}
	}
	return scenarios[s.timeline[0].sc]
}

// Snapshot builds what a collector would serve for view at now, with what
// its ledger remembers of the incidents in it.
func (s *Sim) Snapshot(view string, now time.Time) (model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remember(now)
	snap, e := s.build("", now)
	if e != nil {
		return snap, e
	}
	if now.After(s.observed) {
		s.ledger.Observe(snap, now, true)
		s.observed = now
	}
	if view != "" {
		if snap, e = s.build(view, now); e != nil {
			return snap, e
		}
	}
	snap.Ledger = s.ledger.Recent(view, now)
	r := s.rollups(now)
	for i := range snap.Checks {
		snap.Checks[i].Availability = r.Availability(snap.Checks[i].ID, now)
	}
	return snap, nil
}

// Rollups is what a collector would return for a check's rollups at now,
// laid out in slots of step over span (see rollup.Store.Buckets), with
// their total and the earliest moment they hold.
func (s *Sim) Rollups(check string, step, span time.Duration, now time.Time) (model.Rollup, []model.Rollup, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at(now).Unreachable {
		return model.Rollup{}, nil, time.Time{}, errors.New("preview: simulated collector outage (connection refused)")
	}
	if _, ok := config.Check(s.base, check); !ok {
		return model.Rollup{}, nil, time.Time{}, fmt.Errorf("preview: no check %q", check)
	}
	r := s.rollups(now)
	from := now.Add(-span).Truncate(step)
	total, _ := r.Window(check, from, now, now)
	return total, r.Buckets(check, from, now, step, now), r.Since(check), nil
}

// kept is how long before now the simulated collector has kept rollups:
// all of the longest window.
const kept = 31 * 24 * time.Hour

// The rollups stand for a collector's, not for every sample it took: they
// sample each check every five minutes for two days and hourly before
// that, never more often than it runs, on a grid fixed in time, so that
// simulators of one topology share every sample a scenario leaves alone.
const (
	recentEvery = 5 * time.Minute
	olderEvery  = time.Hour
)

// rolls are what rollups were built from timelines: simulators of one
// topology and one timeline, read in one minute, share them.
var rolls = struct {
	sync.Mutex
	m map[string]*rollup.Store
}{m: map[string]*rollup.Store{}}

// rollups are what a collector watching all along would hold at now, as of
// the minute; s.mu is held. Each sample stands until the next or until it
// goes stale after its scenario ends, and the newest through the minute;
// an observer offline and a check in maintenance take none.
func (s *Sim) rollups(now time.Time) *rollup.Store {
	at := now.Truncate(time.Minute)
	if s.rolled != nil && s.rolledAt.Equal(at) {
		return s.rolled
	}
	var key strings.Builder
	key.WriteString(config.Revision(s.base))
	for _, c := range s.timeline {
		fmt.Fprintf(&key, ";%d@%d", c.sc, c.at.UnixNano())
	}
	fmt.Fprintf(&key, ";%d", at.UnixNano())
	rolls.Lock()
	r, ok := rolls.m[key.String()]
	rolls.Unlock()
	if !ok {
		r = s.roll(at)
		rolls.Lock()
		if len(rolls.m) >= 16 {
			clear(rolls.m)
		}
		rolls.m[key.String()] = r
		rolls.Unlock()
	}
	s.rolled, s.rolledAt = r, at
	return r
}

func (s *Sim) roll(at time.Time) *rollup.Store {
	r := rollup.New(rollup.MaxLimit)
	bounds := []time.Time{at.Add(-kept)}
	for _, c := range s.timeline {
		if c.at.After(bounds[0]) && c.at.Before(at) {
			bounds = append(bounds, c.at)
		}
	}
	bounds = append(bounds, at)
	for _, x := range s.base.Checks {
		every := time.Duration(x.IntervalSeconds) * time.Second
		stale := time.Duration(x.StaleSeconds) * time.Second
		offset := time.Duration(hash(x.ID)%uint32(x.IntervalSeconds*1000)) * time.Millisecond
		step := func(t time.Time) time.Duration {
			if at.Sub(t) < 2*24*time.Hour {
				return max(every, recentEvery)
			}
			return max(every, olderEvery)
		}
		for k := 0; k+1 < len(bounds); k++ {
			from, to := bounds[k], bounds[k+1]
			sc := s.at(from)
			c := s.configFor(sc)
			y, _ := config.Check(c, x.ID)
			if has(sc.Offline, x.Observer) || y.Disabled || y.Maintenance {
				continue
			}
			t := from.Add(-offset).Truncate(step(from)).Add(offset)
			if t.Before(from) {
				t = t.Add(step(from))
			}
			for ; t.Before(to); t = t.Add(step(t)) {
				stands := min(max(step(t), stale), to.Sub(t)+stale)
				if to.Equal(at) && !t.Add(step(t)).Before(to) {
					// The newest sample is current through the minute read.
					stands = at.Add(time.Minute).Sub(t)
				}
				o := s.observe(c, y, t, sc)
				r.Add(x.ID, t, o.Outcome, o.DurationMS, stands, at)
			}
		}
	}
	r.Settle(at)
	return r
}

// replays are what ledgers concluded from timelines: simulators of one
// topology and one timeline, as every read of topo preview starts, judge
// it once.
var replays = struct {
	sync.Mutex
	m map[string][]model.IncidentRecord
}{m: map[string][]model.IncidentRecord{}}

// remember brings the ledger up to now as a collector watching all along
// would have kept it, judging the evidence in the minutes after each
// scenario change of the last two hours, when incidents open and close.
// A collector outage judges nothing.
func (s *Sim) remember(now time.Time) {
	moments := []time.Time{}
	for _, c := range s.timeline {
		if c.at.Before(now.Add(-2 * time.Hour)) {
			continue
		}
		for _, after := range []time.Duration{20 * time.Second, time.Minute, 3 * time.Minute} {
			if t := c.at.Add(after); t.Before(now) && t.After(s.observed) {
				moments = append(moments, t)
			}
		}
	}
	if len(moments) == 0 {
		return
	}
	slices.SortFunc(moments, func(a, b time.Time) int { return a.Compare(b) })
	key := ""
	if s.observed.IsZero() {
		var b strings.Builder
		b.WriteString(config.Revision(s.base))
		for _, c := range s.timeline {
			fmt.Fprintf(&b, ";%d@%d", c.sc, c.at.UnixNano())
		}
		for _, t := range moments {
			fmt.Fprintf(&b, ";%d", t.UnixNano())
		}
		key = b.String()
		replays.Lock()
		records, ok := replays.m[key]
		replays.Unlock()
		if ok {
			s.ledger, s.observed = ledger.With(records), moments[len(moments)-1]
			return
		}
	}
	for _, t := range moments {
		if snap, e := s.build("", t); e == nil {
			s.ledger.Observe(snap, t, true)
		}
		s.observed = t
	}
	if key != "" {
		replays.Lock()
		if len(replays.m) >= 32 {
			clear(replays.m)
		}
		replays.m[key] = s.ledger.Records()
		replays.Unlock()
	}
}

// build is the snapshot of view at now, without the ledger; s.mu is held.
func (s *Sim) build(view string, now time.Time) (model.Snapshot, error) {
	cur := s.at(now)
	if cur.Unreachable {
		return model.Snapshot{}, errors.New("preview: simulated collector outage (connection refused)")
	}
	c := s.configFor(cur)
	rows := []model.Observation{}
	for _, x := range c.Checks {
		every := time.Duration(x.IntervalSeconds) * time.Second
		offset := time.Duration(hash(x.ID)%uint32(x.IntervalSeconds*1000)) * time.Millisecond
		latest := now.Add(-offset).Truncate(every).Add(offset)
		for k := 7; k >= 0; k-- {
			t := latest.Add(-time.Duration(k) * every)
			sc := s.at(t)
			if has(sc.Offline, x.Observer) {
				continue
			}
			o := s.observe(c, x, t, sc)
			if d := sc.Skew[x.Observer]; d != 0 {
				o.SampledAt = o.SampledAt.Add(d)
			}
			rows = append(rows, o)
		}
	}
	beats := map[string]model.Heartbeat{}
	for _, o := range c.Observers {
		if !has(cur.Offline, o.ID) {
			t := now.Add(-time.Second + cur.Skew[o.ID])
			beats[o.ID] = model.Heartbeat{Project: c.Project, Contracts: config.ObserverContracts(c, o.ID), Observer: o.ID, Boot: "preview", SampledAt: t, ReceivedAt: now}
		}
	}
	return engine.Build(c, "preview", view, rows, beats, now), nil
}

// History is what a collector would return for a check's history at now:
// up to limit samples, newest first, as the scenarios in effect produced
// them.
func (s *Sim) History(check string, limit int, now time.Time) ([]model.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.at(now)
	if cur.Unreachable {
		return nil, errors.New("preview: simulated collector outage (connection refused)")
	}
	c := s.configFor(cur)
	x, ok := config.Check(c, check)
	if !ok {
		return nil, fmt.Errorf("preview: no check %q", check)
	}
	every := time.Duration(x.IntervalSeconds) * time.Second
	offset := time.Duration(hash(x.ID)%uint32(x.IntervalSeconds*1000)) * time.Millisecond
	latest := now.Add(-offset).Truncate(every).Add(offset)
	out := []model.Observation{}
	for k := 0; len(out) < limit && k < 24*3600/x.IntervalSeconds; k++ {
		t := latest.Add(-time.Duration(k) * every)
		sc := s.at(t)
		if has(sc.Offline, x.Observer) {
			continue
		}
		o := s.observe(c, x, t, sc)
		if d := sc.Skew[x.Observer]; d != 0 {
			o.SampledAt = o.SampledAt.Add(d)
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *Sim) configFor(sc Scenario) model.Config {
	c := s.base
	if len(sc.Maintenance) == 0 {
		return c
	}
	c.Checks = append([]model.Check(nil), c.Checks...)
	for i := range c.Checks {
		for _, n := range s.reach(c.Checks[i]) {
			if has(sc.Maintenance, n) {
				c.Checks[i].Maintenance = true
			}
		}
	}
	return c
}

// reach lists the nodes a check's traffic depends on, in order.
func (s *Sim) reach(x model.Check) []string {
	r := []string{}
	add := func(id string) {
		if id != "" && !has(r, id) {
			r = append(r, id)
		}
	}
	p := s.paths[x.Path]
	for i, n := range p.Nodes {
		if i > 0 || n != s.obsNode[x.Observer] {
			add(n)
		}
	}
	switch x.Kind {
	case "tailscale", "wireguard":
		if s.nodes[x.Adapter.Peer].ID != "" {
			add(x.Adapter.Peer)
		}
	case "mihomo":
	default:
		for _, v := range s.ends[x.Endpoint].Via {
			add(v)
		}
		add(s.ends[x.Endpoint].Node)
	}
	return r
}

// pops are a few points of presence per edge network. An observer lands on
// the nearest, except that mainland observers on plans without a China
// network are carried abroad.
type pop struct {
	code     string
	lat, lon float64
}

var pops = map[string][]pop{
	"cloudflare": {{"NRT", 35.77, 140.39}, {"HKG", 22.31, 113.92}, {"SIN", 1.36, 103.99}, {"LAX", 33.94, -118.41}, {"HNL", 21.32, -157.92}, {"EWR", 40.69, -74.17}, {"FRA", 50.04, 8.56}},
	"vercel":     {{"hnd1", 35.55, 139.78}, {"hkg1", 22.31, 113.92}, {"sin1", 1.36, 103.99}, {"sfo1", 37.62, -122.38}, {"iad1", 38.95, -77.45}, {"fra1", 50.04, 8.56}},
}
var mainlandPop = map[string]string{"cloudflare": "LAX", "vercel": "hkg1"}

// edgePop is where an observer's requests to an edge network land.
func (s *Sim) edgePop(provider, observer string) (string, *model.Location) {
	list := pops[provider]
	if len(list) == 0 {
		return "", nil
	}
	best := list[0]
	if code := mainlandPop[provider]; code != "" && s.china(s.obsNode[observer]) {
		for _, p := range list {
			if p.code == code {
				best = p
			}
		}
	} else if from := s.where[s.obsNode[observer]]; from != nil {
		far := math.Inf(1)
		for _, p := range list {
			if d := distance(*from, model.Location{Lat: p.lat, Lon: p.lon}); d < far {
				best, far = p, d
			}
		}
	}
	return best.code, &model.Location{Label: best.code, Lat: best.lat, Lon: best.lon}
}

// loc is where traffic to a node goes: its declared place, or for an edge
// with no single place the point of presence the observer lands on.
func (s *Sim) loc(node, observer string) *model.Location {
	if l := s.where[node]; l != nil || s.nodes[node].Kind != "edge" {
		return l
	}
	_, l := s.edgePop(s.nodes[node].Provider, observer)
	return l
}

// china is a coarse mainland bounding box (excluding Taiwan, Hong Kong and
// Korea); it only decides which simulated samples count as cross-border.
func (s *Sim) china(node string) bool { return mainland(s.where[node]) }
func mainland(l *model.Location) bool {
	if l == nil || l.Lat < 18 || l.Lat > 54 || l.Lon < 73 || l.Lon > 135 {
		return false
	}
	taiwan := l.Lat < 26 && l.Lon > 119.5
	hongKong := l.Lat < 22.6 && l.Lon > 113.8 && l.Lon < 114.5
	korea := l.Lat > 33 && l.Lat < 39 && l.Lon > 124.5
	return !taiwan && !hongKong && !korea
}

// rtt is a plausible round trip for the declared geography: fibre distance
// with a routing detour, plus a little per-sample jitter.
func (s *Sim) rtt(x model.Check, reach []string, t time.Time) float64 {
	from := s.obsNode[x.Observer]
	km := 0.0
	prev := s.where[from]
	for _, n := range reach {
		if l := s.loc(n, x.Observer); l != nil {
			if prev != nil {
				km += distance(*prev, *l)
			}
			prev = l
		}
	}
	base := 1.5 + km/100*1.35
	if s.ends[x.Endpoint].Network == "lan" {
		base = 1.2
	}
	return base * (.92 + .22*unit(x.ID, t.Unix(), 1))
}

func (s *Sim) observe(c model.Config, x model.Check, t time.Time, sc Scenario) model.Observation {
	o := model.Observation{ID: x.Observer + ".preview." + strconv.FormatInt(t.Unix(), 10), Project: c.Project, Contract: s.contracts[x.ID], Check: x.ID, Observer: x.Observer, Boot: "preview", Sequence: uint64(t.Unix()), SampledAt: t, ReceivedAt: t.Add(180 * time.Millisecond), Evidence: "measured", Facts: map[string]string{}, Timings: map[string]int64{}, PathEvidence: "requested_not_route_verified"}
	if x.Maintenance || x.Disabled {
		o.Outcome, o.Stage, o.Detail = "unknown", "setup", "check not executed"
		return o
	}
	if len(s.paths[x.Path].Proxies) > 0 {
		o.Facts["transport_hops"] = strconv.Itoa(len(s.paths[x.Path].Proxies))
		o.PathEvidence = "explicit_proxy_transport"
	}
	reach := s.reach(x)
	cn := s.china(s.obsNode[x.Observer])
	cross := false
	for _, n := range reach {
		l := s.loc(n, x.Observer)
		cross = cross || (cn && l != nil && !mainland(l))
	}
	ms := s.rtt(x, reach, t)
	if cross && sc.Slow > 0 {
		ms *= sc.Slow
	}
	if sc.Chaos && unit(x.ID, t.Unix()/30, 7) < .3 {
		ms *= 1 + 4*unit(x.ID, t.Unix(), 3)
	}
	tunnel := false
	for _, n := range reach {
		if has(sc.Blocked, n) && cn && s.ends[x.Endpoint].Network == "tailnet" {
			tunnel = true
			ms *= 1.7
		}
	}

	// Which hop breaks first decides how the failure looks to the probe. An
	// edge in front answers for whatever breaks behind it.
	broken, beyond, fronted := "", false, false
	endpointNode := s.ends[x.Endpoint].Node
	edge := endpointNode
	if via := s.ends[x.Endpoint].Via; len(via) > 0 {
		edge = via[0]
	}
	passed, atEdge := false, false
	for _, n := range reach {
		down := has(sc.Down, n) || (has(sc.Blocked, n) && cn && s.ends[x.Endpoint].Network != "tailnet" && x.Kind != "icmp" && x.Kind != "tailscale")
		if down && broken == "" {
			broken, beyond, fronted = n, passed, atEdge && x.Kind == "http"
		}
		if n == endpointNode {
			passed = true
		}
		if n == edge && n != endpointNode && !down {
			atEdge = true
		}
	}
	noise := unit(x.ID, t.Unix(), 2)
	switch {
	case broken != "":
	case cross && sc.Lossy > 0 && noise < sc.Lossy:
		broken = "lossy"
	case sc.Chaos && unit(x.ID, t.Unix()/30, 5) < .15:
		broken = "chaos"
	case !sc.Chaos && sc.Lossy == 0 && noise < .015:
		broken = "noise"
	}
	timeout := float64(x.TimeoutSeconds * 1000)

	switch x.Kind {
	case "mihomo":
		o.Evidence, o.Stage = "reported", "adapter"
		o.Outcome, o.Detail = "ok", "read-only runtime telemetry collected; not an end-to-end service check"
		o.Facts["sampling_atomicity"] = "separate_API_reads_not_atomic"
		o.Facts["mode"], o.Facts["tun_enabled"] = "rule", "true"
		o.Facts["provider_parameters"] = "not_verifiable_by_names_alone"
		o.Facts["test_url"] = "https://www.gstatic.com/generate_204"
		if chain, delay := s.selected(x.Observer, sc, t); chain != "" {
			o.Facts["reported_chain"] = x.Adapter.RootProxy + " -> " + chain
			o.Facts["reported_selection"] = chain
			o.Facts["core_delay_ms"] = strconv.Itoa(int(delay))
		} else {
			o.Facts["chain_resolution"] = "unresolved"
			o.Outcome, o.Code, o.Detail = "fail", "runtime_chain_unresolved", "runtime chain unresolved (missing selection, unsupported shape or cycle)"
		}
		o.DurationMS = 2 + int64(noise*4)
		return o
	case "tailscale":
		o.Evidence, o.Stage = "reported", "adapter"
		peer := x.Adapter.Peer
		if broken == peer {
			o.Outcome, o.Code, o.Detail = "fail", "peer_or_backend_not_online", "peer/backend not reported online; host power state and service reachability unknown"
			o.Facts["backend_state"], o.Facts["online_reported"], o.Facts["active_reported"] = "Running", "false", "false"
		} else {
			o.Outcome, o.Detail = "ok", "peer telemetry collected; idle is not offline; probe tailnet service separately"
			o.Facts["backend_state"], o.Facts["online_reported"], o.Facts["active_reported"] = "Running", "true", "true"
			o.Facts["last_seen"] = t.Add(-2 * time.Second).UTC().Format(time.RFC3339)
			o.Facts["transport_hint"] = "direct_endpoint_reported"
			relay := !has(sc.Down, "hz") && (tunnel || has(sc.Blocked, peer) && cn || cross && hash(peer)%3 == 0)
			if relay {
				o.Facts["transport_hint"], o.Facts["relay"] = "relay_preference_reported", "hz"
			}
		}
		o.DurationMS = 3 + int64(noise*6)
		return o
	}

	o.Facts["connection_semantics"] = "new_client_stream; upstream_core_multiplexing_unknown"
	if broken != "" && !beyond && !fronted {
		o.Outcome, o.Connectivity = "fail", false
		o.Stage, o.Code = "connect", "timeout"
		if len(s.paths[x.Path].Proxies) > 0 && s.nodes[broken].ID != "" {
			// The local core answers the SOCKS request once its upstream dial fails.
			o.Stage, o.Code = "proxy_socks", "reply_4"
			ms = timeout * (.4 + .2*noise)
		}
		if x.Kind == "icmp" {
			o.Stage, o.Code = "icmp", "timeout"
		}
		if o.Code == "timeout" {
			ms = timeout
		}
		o.Detail = o.Stage + ": " + o.Code
		o.DurationMS = int64(ms)
		return o
	}

	o.Outcome, o.Connectivity = "ok", true
	connect := ms
	o.Timings["connect_ms"] = int64(connect)
	total := connect
	switch x.Kind {
	case "tcp":
		o.Stage, o.Detail = "tcp", "client-visible TCP connect succeeded; remote application not verified"
		o.Facts["response_proof"] = "none"
	case "tls":
		o.Timings["tls_ms"] = int64(ms)
		total += ms
		o.Stage, o.Detail = "tls", "verified TLS handshake; application and proxy authentication not verified"
		o.Facts["response_proof"], o.Facts["tls_version"] = "verified_tls", "0x304"
	case "ssh-banner":
		total += ms
		o.Stage, o.Detail = "ssh-banner", "SSH protocol banner received; host key and authentication not verified"
		o.Facts["response_proof"] = "ssh_banner"
	case "icmp":
		o.Timings = map[string]int64{}
		o.Stage, o.Detail = "icmp", "IPv4 ICMP echo reply; TCP, UDP and services not inferred"
		o.Facts["response_proof"], o.Facts["rtt_ms"] = "icmp_echo", strconv.Itoa(int(ms))
		delete(o.Facts, "connection_semantics")
	case "dns":
		o.Timings = map[string]int64{}
		o.Stage, o.Detail = "dns_answer", "configured resolver returned addresses satisfying assertions; DNSSEC not validated"
		o.Facts["response_proof"], o.Facts["dns_transport"], o.Facts["addresses"] = "dns_response_not_dnssec", "udp", "192.0.2.10"
		delete(o.Facts, "connection_semantics")
	case "http":
		https := strings.HasPrefix(config.Target(c, x), "https://")
		dns := 2 + 18*unit(x.ID, t.Unix(), 4)
		o.Timings["dns_ms"] = int64(dns)
		total += dns
		if https {
			o.Timings["tls_ms"] = int64(ms * 1.05)
			total += ms * 1.05
		}
		server := 8 + 60*unit(x.ID, t.Unix(), 6)
		o.Timings["first_byte_ms"] = int64(ms + server)
		total += ms + server
		o.Facts["http_status"] = "200"
		o.Facts["response_proof"] = "http_response"
		if https {
			o.Facts["response_proof"] = "verified_https"
		}
		provider := s.nodes[edge].Provider
		if s.nodes[edge].Kind == "edge" && pops[provider] != nil {
			o.Facts["edge_provider"] = provider
			o.Facts["edge_pop"], _ = s.edgePop(provider, x.Observer)
			switch {
			case s.services[x.Service].Runtime == "pages":
				o.Facts["edge_cache"] = "HIT"
			case provider == "vercel":
				o.Facts["edge_cache"] = "MISS"
				o.Facts["edge_region"] = "hnd1"
			case edge != endpointNode:
				o.Facts["edge_cache"] = "DYNAMIC"
			}
		}
		o.Stage, o.Detail = "http_assert", "HTTP assertions passed; result applies only to this request"
		if fronted {
			// Cloudflare's own page for an origin that refuses connections.
			o.Outcome, o.Code, o.Detail = "fail", "unexpected_status", "http_assert: unexpected_status"
			o.Facts["http_status"] = map[bool]string{true: "521", false: "502"}[provider == "cloudflare"]
		} else if beyond {
			o.Outcome = "fail"
			o.Facts["http_status"] = "502"
			o.Code = "unexpected_status"
			o.Detail = "http_assert: unexpected_status"
		} else if len(x.HTTP.ExpectedExit) > 0 {
			exit := x.HTTP.ExpectedExit[0]
			for _, n := range reach {
				if ip, ok := sc.WrongExit[n]; ok {
					exit = ip
					o.Outcome, o.Stage, o.Code = "policy", "exit_assert", "exit_mismatch"
					o.Detail = "request succeeded but observed exit violates the declared policy"
				}
			}
			o.Facts["exit_ip"] = exit
		}
	}
	if broken != "" && beyond && x.Kind != "http" {
		o.Outcome, o.Connectivity, o.Stage, o.Code = "fail", false, "connect", "timeout"
		o.Detail = "connect: timeout"
		delete(o.Facts, "response_proof")
		total = timeout
	}
	o.DurationMS = int64(total)
	return o
}

// selected mimics the proxy core's fallback group: the first candidate whose
// hops are all usable right now.
func (s *Sim) selected(observer string, sc Scenario, t time.Time) (string, float64) {
	cn := s.china(s.obsNode[observer])
	for _, p := range s.base.Paths {
		if p.Observer != observer || len(p.Candidates) == 0 {
			continue
		}
		for _, id := range p.Candidates {
			q := s.paths[id]
			ok, hops := true, []string{}
			for i, n := range q.Nodes {
				ok = ok && !has(sc.Down, n) && !(cn && has(sc.Blocked, n))
				if i > 0 && s.nodes[n].Kind != "external" {
					hops = append(hops, s.nodes[n].Name)
				}
			}
			if ok {
				return strings.Join(hops, " -> "), s.rtt(model.Check{ID: id, Observer: observer}, q.Nodes, t)
			}
		}
		return "", 0
	}
	return "DIRECT", 1
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// unit is a stable pseudo-random number in [0,1) for (id, tick, salt), so the
// same sample looks the same on every refresh.
func unit(id string, tick int64, salt int) float64 {
	return float64(hash(id+"/"+strconv.FormatInt(tick, 10)+"/"+strconv.Itoa(salt))%100000) / 100000
}
func distance(a, b model.Location) float64 {
	const r = 6371.0
	la1, la2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dl := (b.Lon - a.Lon) * math.Pi / 180
	h := math.Sin((la2-la1)/2)*math.Sin((la2-la1)/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}
