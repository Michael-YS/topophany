// Package preview feeds the TUI a built-in topology with synthetic evidence so
// rendering can be exercised without collectors, workers or network access.
// Every snapshot is built by the real engine from a simulated observation
// history; scenarios change what that history says, never how it is judged.
package preview

import (
	"bytes"
	_ "embed"
	"errors"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
)

//go:embed topology.json
var preset []byte

// Preset returns the built-in complex topology (Changsha home, Hangzhou
// relay, LA line + residential exits, Hawaii exit, New York, Tokyo, Frankfurt).
func Preset() (model.Config, error) { return config.Decode(bytes.NewReader(preset)) }

// PresetJSON is the reviewed source of Preset, for `topo preview --print-topology`.
func PresetJSON() []byte { return append([]byte(nil), preset...) }

// Scenario is one simulated world state. Node and observer IDs that are not in
// the topology make a scenario unavailable rather than silently empty.
type Scenario struct {
	Name, Detail string
	Down         []string          // nodes that answer nothing (power/host failure)
	Blocked      []string          // nodes unreachable over public networks from mainland-China observers
	Offline      []string          // observers whose worker stops reporting
	Maintenance  []string          // nodes whose checks are put in maintenance
	WrongExit    map[string]string // exit node → observed exit IP that violates policy
	Lossy        float64           // failure probability on cross-border samples
	Slow         float64           // latency multiplier on cross-border samples
	Chaos        bool              // random 30-second outage windows everywhere
	Skew         map[string]time.Duration
	Unreachable  bool // the collector itself cannot be reached
}

var scenarios = []Scenario{
	{Name: "全部正常", Detail: "所有链路可用；偶尔出现单次失败（未达确认阈值，只显示 warning）。"},
	{Name: "DMIT 线路被阻断（仅国内）", Detail: "长沙和杭州到 DMIT 的公网连接超时，ICMP 仍通，tailnet 走 DERP 中继；纽约/东京对照仍可达 → 反证 + 路径特定故障假设。Clash 回落到 S2。", Blocked: []string{"dmit"}},
	{Name: "DMIT 线路机宕机", Detail: "DMIT 对所有观测点都无响应（含 tailnet）；主链全断，S2 备链接管。", Down: []string{"dmit"}},
	{Name: "AAITR 家宽落地故障", Detail: "两条经 AAITR 的链都失败，QQPR 夏威夷出口仍可用 → 出口候选降级。", Down: []string{"aaitr"}},
	{Name: "AAITR 出口 IP 漂移", Detail: "请求成功但出口 IP 不在声明范围 → policy 违规，不算宕机。", WrongExit: map[string]string{"aaitr": "203.0.113.99"}},
	{Name: "跨境高丢包 / 高延迟", Detail: "国内观测点的跨境样本 35% 失败、延迟 ×2.6；状态在 warning/down 之间抖动。", Lossy: .35, Slow: 2.6},
	{Name: "长沙家宽断网", Detail: "Windows/MacBook 的 worker 停止上报（几十秒后转为 stale/unknown）；杭州反代访问家庭面板失败，访客看到 502。", Down: []string{"win", "mac", "router"}, Offline: []string{"cs-win", "cs-mac"}},
	{Name: "杭州中继宕机", Detail: "阿里云杭州整机不可用：DERP 中继、反向代理和杭州观测点同时消失。", Down: []string{"hz"}, Offline: []string{"obs-hz"}},
	{Name: "S2 维护窗口", Detail: "S2 相关检查进入维护（不告警）；备用线路与私有服务暂停判定。", Maintenance: []string{"s2"}},
	{Name: "东京 worker 时钟超前", Detail: "东京执行器时钟快 40 秒；其样本不被当作当前结果。", Skew: map[string]time.Duration{"obs-jp": 40 * time.Second}},
	{Name: "随机混沌", Detail: "每个检查以 30 秒为窗口随机中断（约 15%），延迟随机尖峰。", Chaos: true},
	{Name: "Collector 不可达", Detail: "TUI 拿不到新快照：保留缓存但全部显示为 unknown / CACHED。", Unreachable: true},
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
	obsNode   map[string]string
}

func New(c model.Config, now time.Time) *Sim {
	s := &Sim{base: c, where: map[string]*model.Location{}, nodes: map[string]model.Node{}, ends: map[string]model.Endpoint{}, paths: map[string]model.Path{}, obsNode: map[string]string{}}
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
		r = append(r, scenarios[i].Name+"\t"+scenarios[i].Detail)
	}
	return r
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

// Snapshot builds what a collector would serve for view at now.
func (s *Sim) Snapshot(view string, now time.Time) (model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.at(now)
	if cur.Unreachable {
		return model.Snapshot{}, errors.New("preview: simulated collector outage (connection refused)")
	}
	c := s.configFor(cur)
	rev := config.Revision(c)
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
			o := s.observe(c, rev, x, t, sc)
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
			beats[o.ID] = model.Heartbeat{Observer: o.ID, Boot: "preview", SampledAt: t, ReceivedAt: now}
		}
	}
	return engine.Build(c, "preview", view, rows, beats, now), nil
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
		add(s.ends[x.Endpoint].Node)
	}
	return r
}

// china is a coarse mainland bounding box (excluding Taiwan, Hong Kong and
// Korea); it only decides which simulated samples count as cross-border.
func (s *Sim) china(node string) bool {
	l := s.where[node]
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
		if l := s.where[n]; l != nil {
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

func (s *Sim) observe(c model.Config, rev string, x model.Check, t time.Time, sc Scenario) model.Observation {
	o := model.Observation{ID: x.Observer + ".preview." + strconv.FormatInt(t.Unix(), 10), Project: c.Project, Revision: rev, Check: x.ID, Observer: x.Observer, Boot: "preview", Sequence: uint64(t.Unix()), SampledAt: t, ReceivedAt: t.Add(180 * time.Millisecond), Evidence: "measured", Facts: map[string]string{}, Timings: map[string]int64{}, PathEvidence: "requested_not_route_verified"}
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
		cross = cross || (cn && s.where[n] != nil && !s.china(n))
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

	// Which hop breaks first decides how the failure looks to the probe.
	broken, beyond := "", false
	endpointNode := s.ends[x.Endpoint].Node
	passed := false
	for _, n := range reach {
		down := has(sc.Down, n) || (has(sc.Blocked, n) && cn && s.ends[x.Endpoint].Network != "tailnet" && x.Kind != "icmp" && x.Kind != "tailscale")
		if down && broken == "" {
			broken, beyond = n, passed
		}
		if n == endpointNode {
			passed = true
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
	if broken != "" && !beyond {
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
		o.Stage, o.Detail = "http_assert", "HTTP assertions passed; result applies only to this request"
		if beyond {
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
