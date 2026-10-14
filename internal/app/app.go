// Package app wires the independent server, executor and remote client roles.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/runner"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

type App struct {
	In                      io.Reader
	Out, Err                io.Writer
	TerminalIn, TerminalOut *os.File
	Now                     func() time.Time // the clock the read commands judge freshness by
}

func New() *App {
	return &App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, TerminalIn: os.Stdin, TerminalOut: os.Stdout, Now: time.Now}
}

const help = `Topophany — network evidence, topology and service monitoring

Usage:
  topo                              connect using saved credentials; open TUI
  topo --credential TOKEN           override the saved bearer token for this run
  topo --json                       print one authenticated remote snapshot
  topo status                       print the Overview: verdict, counts, what needs attention
  topo show TAB [ID]                print a tab, or the evidence behind one entry in it
  topo history CHECK                print a check's recent samples
  topo login                        save a verified remote connection
  topo serve                        run a collector (optional local checks)
  topo worker                       execute a reviewed topology from this location
  topo init                         create a safe local demo with fresh credentials
  topo edit                         add/edit/delete topology entities in a local TUI
  topo preview                      open the TUI on synthetic evidence (built-in or --config)
  topo validate                     validate a topology; print its revision
  topo once                         run local checks once; write observations as JSON
  topo export                       export a redacted topology
  topo token                        generate a random bearer token and its hash
  topo version                      show version

status, show and history print what the TUI shows, as text or with --json;
TAB is overview, topology, servers, services, paths, checks, incidents,
observers or map. topo status exits 0 when all is clear, 2 when something
needs attention, 3 when nothing current is known and 4 when no collector
answers. 'topo preview [--scenario N] status' (or show, history) reads the
synthetic evidence instead; 'topo preview scenarios' lists the scenarios.

Use 'topo COMMAND --help' for command options. TUI: 1–9 tabs (9/? About),
v observer, ↑↓/jk navigation, Enter details, / filter, r refresh, m map
(virtual geo/geo/network), z zoom, mouse clicks and wheel, q quit. In 'topo edit': a add, e edit,
d delete, u undo, Ctrl-S save. In 'topo preview': x scenario, D debug overlay;
evidence is synthetic and the topology is read-only.
Credentials default to the OS user config directory / topophany / credentials.json.
No routes, proxy policies, TUN settings or services are changed by monitoring.
`

func (a *App) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(a.Err)
	return f
}
func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.view(ctx, args)
	}
	switch args[0] {
	case "help", "--help", "-h":
		_, e := io.WriteString(a.Out, help)
		return e
	case "version", "--version":
		_, e := fmt.Fprintln(a.Out, "Topophany "+model.Version)
		return e
	case "serve":
		return a.serve(ctx, args[1:])
	case "worker":
		return a.worker(ctx, args[1:])
	case "login":
		return a.login(ctx, args[1:])
	case "init":
		return a.init(args[1:])
	case "edit":
		return a.edit(ctx, args[1:])
	case "preview":
		return a.preview(ctx, args[1:])
	case "status", "show", "history":
		return a.read(ctx, args[0], args[1:], nil)
	case "validate", "export", "once":
		return a.topologyCommand(ctx, args[0], args[1:])
	case "token":
		return a.token(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			return a.view(ctx, args)
		}
		return fmt.Errorf("unknown command %q; use topo --help", secure.Text(args[0]))
	}
}
func parsed(f *flag.FlagSet, args []string) error {
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() > 0 {
		return errors.New("unexpected positional arguments")
	}
	return nil
}
func printJSON(w io.Writer, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	var out strings.Builder
	for _, r := range string(raw) {
		if unicode.Is(unicode.Bidi_Control, r) {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.WriteRune(r)
		}
	}
	out.WriteByte('\n')
	_, err = io.WriteString(w, out.String())
	return err
}
func (a *App) token(args []string) error {
	f := a.flags("token")
	if e := parsed(f, args); e != nil {
		return e
	}
	t, e := secure.Token()
	if e != nil {
		return e
	}
	return printJSON(a.Out, map[string]string{"token": t, "token_sha256": secure.Hash(t)})
}
func (a *App) topologyCommand(ctx context.Context, cmd string, args []string) error {
	f := a.flags(cmd)
	path := f.String("config", "topology.json", "reviewed local topology JSON")
	observer := f.String("observer", "", "local execution context (required for once)")
	if e := parsed(f, args); e != nil {
		return e
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	switch cmd {
	case "validate":
		return printJSON(a.Out, map[string]any{"project": c.Project, "revision": config.Revision(c), "contracts": config.Contracts(c), "nodes": len(c.Nodes), "checks": len(c.Checks), "valid": true})
	case "export":
		return printJSON(a.Out, config.Public(c))
	case "once":
		r, e := runner.New(c, *observer, 1, probe.Runner{}.Run, func(v model.Observation) error { return json.NewEncoder(a.Out).Encode(v) })
		if e != nil {
			return e
		}
		return r.Once(ctx)
	}
	return nil
}
func (a *App) serve(parent context.Context, args []string) error {
	f := a.flags("serve")
	path := f.String("config", "topology.json", "reviewed local topology JSON")
	authPath := f.String("auth", "auth.json", "0600 collector token-hash configuration")
	dir := f.String("data", "./collector-data", "exclusive local journal directory")
	listen := f.String("listen", "127.0.0.1:7447", "collector listen address")
	instance := f.String("instance", "collector", "visible collector instance ID")
	observer := f.String("observer", "", "optional local observer ID; otherwise collect only")
	workers := f.Int("concurrency", 4, "bounded local probe concurrency (1..32)")
	cert := f.String("tls-cert", "", "PEM server certificate")
	key := f.String("tls-key", "", "PEM server private key")
	insecure := f.Bool("allow-insecure", false, "explicitly allow plaintext non-loopback HTTP (unsafe without a secure tunnel)")
	retention := f.Duration("retention", 24*time.Hour, "raw history/receipt retention and maximum accepted sample age")
	maxHistory := f.Int("max-history", 20000, "maximum retained raw history rows; per-check state tails retained separately")
	if e := parsed(f, args); e != nil {
		return e
	}
	if !config.ID(*instance) {
		return errors.New("invalid instance ID")
	}
	if (*cert == "") != (*key == "") {
		return errors.New("both tls-cert and tls-key required")
	}
	host, _, e := net.SplitHostPort(*listen)
	if e != nil {
		return errors.New("listen must be host:port")
	}
	ip := net.ParseIP(host)
	if *cert == "" && !*insecure && (ip == nil || !ip.IsLoopback()) {
		return errors.New("non-loopback collectors require TLS, or explicit --allow-insecure")
	}
	if *retention < time.Minute || *retention > 30*24*time.Hour {
		return errors.New("retention must be 1m..720h")
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	auth, e := api.LoadAuth(*authPath, c)
	if e != nil {
		return e
	}
	st, e := store.Open(*dir, store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Observers: observerIDs(c), Retention: *retention, MaxHistory: *maxHistory})
	if e != nil {
		return e
	}
	defer st.Close()
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	failures := make(chan error, 2)
	backend := api.New(c, *instance, st, auth)
	backend.MaxSampleAge = *retention
	srv := &http.Server{Addr: *listen, Handler: backend.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	var scheduler *runner.Scheduler
	if *observer != "" {
		scheduler, e = runner.New(c, *observer, *workers, probe.Runner{}.Run, func(v model.Observation) error {
			return st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}})
		})
		if e != nil {
			return e
		}
	}
	ln, e := net.Listen("tcp", *listen)
	if e != nil {
		return e
	}
	defer ln.Close()
	wg.Add(1)
	go func() {
		defer wg.Done()
		var e error
		if *cert != "" {
			e = srv.ServeTLS(api.LimitListener(ln, 128), *cert, *key)
		} else {
			e = srv.Serve(api.LimitListener(ln, 128))
		}
		if e != nil && !errors.Is(e, http.ErrServerClosed) {
			select {
			case failures <- e:
			default:
			}
		}
	}()
	if scheduler != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := scheduler.Run(ctx); e != nil {
				select {
				case failures <- e:
				default:
				}
			}
		}()
	}
	fmt.Fprintf(a.Err, "Topophany %s collector %s listening on %s; project=%s revision=%s\n", model.Version, *instance, ln.Addr(), c.Project, config.Revision(c)[:12])
	if *insecure && *cert == "" {
		fmt.Fprintln(a.Err, "WARNING: plaintext HTTP explicitly enabled; use only inside a trusted encrypted tunnel")
	}
	var result error
	select {
	case <-ctx.Done():
	case result = <-failures:
	}
	cancel()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if e = srv.Shutdown(shutdown); e != nil {
		_ = srv.Close()
	}
	return result
}
func (a *App) worker(parent context.Context, args []string) error {
	f := a.flags("worker")
	path := f.String("config", "topology.json", "reviewed LOCAL topology; never downloaded as executable work")
	observer := f.String("observer", "", "this executor's observer identity")
	credPath := f.String("credentials", "worker-credentials.json", "0600 worker credential file")
	profileName := f.String("profile", "", "credential profile (default active)")
	dir := f.String("data", "./worker-data", "exclusive durable outbox directory")
	budget := f.Int64("outbox-bytes", 32<<20, "hard outbox budget including quarantine")
	workers := f.Int("concurrency", 4, "bounded probe concurrency (1..32)")
	if e := parsed(f, args); e != nil {
		return e
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	creds, e := client.Load(*credPath)
	if e != nil {
		return e
	}
	name := *profileName
	if name == "" {
		name = creds.Active
	}
	p, ok := creds.Profiles[name]
	if !ok || p.Project != c.Project {
		return errors.New("worker credentials project/profile mismatch")
	}
	d, e := outbox.NewDelivery(p, config.Revision(c))
	if e != nil {
		return e
	}
	defer d.Close()
	q, e := outbox.Open(*dir, c.Project, *budget)
	if e != nil {
		return e
	}
	defer q.Close()
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	s, e := runner.New(c, *observer, *workers, probe.Runner{}.Run, func(v model.Observation) error {
		err := q.Add(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}}, d.Recipients())
		var degraded *outbox.DegradedError
		if errors.As(err, &degraded) {
			fmt.Fprintln(a.Err, degraded)
			return nil
		}
		return err
	})
	if e != nil {
		return e
	}
	failed := make(chan error, 1)
	wg.Add(1)
	go func() { defer wg.Done(); failed <- s.Run(ctx) }()
	fmt.Fprintf(a.Err, "Topophany worker observer=%s project=%s revision=%s; %d collectors\n", *observer, c.Project, config.Revision(c)[:12], len(p.Servers))
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-failed:
			return e
		case <-tick.C:
			hb := &model.Heartbeat{Observer: *observer, Boot: s.Boot, SampledAt: time.Now().UTC()}
			for _, r := range d.Tick(ctx, q, hb) {
				if r.Error != "" {
					fmt.Fprintf(a.Err, "collector=%s accepted=%d quarantined=%d: %s; %s\n", r.Recipient, r.Accepted, r.Quarantined, r.Error, q)
				}
			}
		}
	}
}

// connection registers the flags that choose a collector and returns how
// to connect with them once they are parsed.
func (a *App) connection(f *flag.FlagSet) func() (*client.Client, error) {
	defaultPath, pathErr := client.DefaultPath()
	path := f.String("credentials", defaultPath, "saved client credentials")
	profile := f.String("profile", "", "saved profile (default active)")
	token := f.String("credential", "", "explicit temporary bearer-token override (prefer saved credentials)")
	server := f.String("server", "", "temporary collector URL override")
	project := f.String("project", "", "expected project ID; required without saved credentials")
	insecure := f.Bool("allow-insecure", false, "explicit consent for plaintext non-loopback URL")
	ca := f.String("ca-file", "", "custom trusted CA for an explicit server override")
	return func() (*client.Client, error) {
		if *path == "" && pathErr != nil {
			return nil, pathErr
		}
		var p client.Profile
		creds, e := client.Load(*path)
		if e == nil {
			name := *profile
			if name == "" {
				name = creds.Active
			}
			var ok bool
			p, ok = creds.Profiles[name]
			if !ok {
				return nil, errors.New("credential profile not found")
			}
		} else if *server == "" || *project == "" || *token == "" {
			return nil, fmt.Errorf("credentials unavailable; run topo login first (%w)", e)
		}
		if *project != "" {
			p.Project = *project
		}
		if *server != "" {
			p.Servers = []client.Endpoint{{URL: *server, Token: *token, CAFile: *ca, AllowInsecure: *insecure}}
		}
		if *token != "" {
			if len(p.Servers) != 1 {
				return nil, errors.New("--credential requires --server for a multi-server profile")
			}
			for i := range p.Servers {
				p.Servers[i].Token = *token
				p.Servers[i].TokenEnv = ""
			}
		}
		return client.New(p)
	}
}
func (a *App) view(ctx context.Context, args []string) error {
	f := a.flags("topo")
	connect := a.connection(f)
	observer := f.String("observer", "", "observer view (default all)")
	jsonOut := f.Bool("json", false, "print one snapshot without terminal control")
	noColor := f.Bool("no-color", os.Getenv("NO_COLOR") != "", "disable ANSI colors")
	if e := parsed(f, args); e != nil {
		return e
	}
	c, e := connect()
	if e != nil {
		return e
	}
	defer c.Close()
	if *jsonOut {
		snap, _, e := c.Snapshot(ctx, *observer)
		if e != nil {
			return e
		}
		return printJSON(a.Out, snap)
	}
	if a.TerminalIn == nil || a.TerminalOut == nil {
		return errors.New("interactive terminal unavailable; use --json")
	}
	return tui.Run(ctx, c, *observer, *noColor, a.TerminalIn, a.TerminalOut)
}

// edit opens the local topology editor. It never contacts a collector: the
// reviewed file is the only source of truth, and changes apply on restart.
func (a *App) edit(ctx context.Context, args []string) error {
	f := a.flags("edit")
	path := f.String("config", "topology.json", "local topology JSON to edit (created on first save if missing)")
	project := f.String("project", "my-network", "project ID for a new topology file")
	noColor := f.Bool("no-color", os.Getenv("NO_COLOR") != "", "disable ANSI colors")
	if e := parsed(f, args); e != nil {
		return e
	}
	c, e := config.Load(*path)
	if errors.Is(e, os.ErrNotExist) {
		if !config.ID(*project) {
			return errors.New("invalid --project for a new topology")
		}
		c, e = model.Config{Schema: model.ConfigSchema, Project: *project}, nil
	}
	if e != nil {
		return fmt.Errorf("%s: %w (fix the JSON, or run topo validate for details)", *path, e)
	}
	if a.TerminalIn == nil || a.TerminalOut == nil {
		return errors.New("interactive terminal unavailable")
	}
	return tui.RunEditor(ctx, *path, c, *noColor, a.TerminalIn, a.TerminalOut)
}

// preview opens the TUI on synthetic evidence. It never dials a collector.
// --scenario is applied in the past, so the view opens already settled;
// switching with x inside the TUI happens at the current time, and health
// follows the real confirmation and staleness rules as new samples arrive.
func (a *App) preview(ctx context.Context, args []string) error {
	f := a.flags("preview")
	path := f.String("config", "", "topology to render with synthetic evidence (default: built-in complex homelab)")
	observer := f.String("observer", "", "initial observer view (default: all observers)")
	scenario := f.Int("scenario", 0, "initial scenario index, already in effect; x in the TUI lists names")
	printTopo := f.Bool("print-topology", false, "write the built-in topology JSON to stdout and exit")
	dump := f.Bool("json", false, "print one synthetic snapshot and exit")
	noColor := f.Bool("no-color", os.Getenv("NO_COLOR") != "", "disable ANSI colors")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *printTopo {
		if f.NArg() > 0 {
			return errors.New("--print-topology takes no command")
		}
		_, e := a.Out.Write(preview.PresetJSON())
		return e
	}
	var c model.Config
	var e error
	if *path == "" {
		c, e = preview.Preset()
	} else {
		c, e = config.Load(*path)
	}
	if e != nil {
		return e
	}
	now := a.now()
	sim := preview.New(c, now)
	names := sim.Scenarios()
	if *scenario < 0 || *scenario >= len(names) {
		return fmt.Errorf("scenario %d out of range (0..%d)", *scenario, len(names)-1)
	}
	if *scenario != 0 {
		sim.SetScenario(*scenario, now.Add(-30*time.Minute))
	}
	if f.NArg() > 0 {
		switch cmd, rest := f.Arg(0), f.Args()[1:]; cmd {
		case "status", "show", "history":
			return a.read(ctx, cmd, rest, &previewed{config: c, sim: sim, now: now, observer: *observer, json: *dump})
		case "scenarios":
			return a.scenarios(rest, names, sim.Scenario(), *dump)
		default:
			return fmt.Errorf("unknown preview command %q; use status, show, history or scenarios", secure.Text(cmd))
		}
	}
	if *dump {
		snap, e := sim.Snapshot(*observer, now)
		if e != nil {
			return e
		}
		return printJSON(a.Out, snap)
	}
	if a.TerminalIn == nil || a.TerminalOut == nil {
		return errors.New("interactive terminal unavailable")
	}
	return tui.RunPreview(ctx, sim, func(_ context.Context, view string) (model.Snapshot, string, error) {
		snap, e := sim.Snapshot(view, time.Now())
		return snap, "synthetic", e
	}, *observer, *noColor, a.TerminalIn, a.TerminalOut)
}
func (a *App) login(ctx context.Context, args []string) error {
	f := a.flags("login")
	defaultPath, e := client.DefaultPath()
	if e != nil {
		return e
	}
	path := f.String("credentials", defaultPath, "destination credential file")
	name := f.String("profile", "default", "profile name to create/update")
	server := f.String("server", "", "HTTPS collector URL")
	project := f.String("project", "", "expected project identity (required)")
	tokenStdin := f.Bool("token-stdin", false, "read bearer token from stdin (no terminal required)")
	ca := f.String("ca-file", "", "custom trusted server CA")
	insecure := f.Bool("allow-insecure", false, "explicitly allow plaintext non-loopback HTTP")
	if e := parsed(f, args); e != nil {
		return e
	}
	if !config.ID(*name) || !config.ID(*project) {
		return errors.New("valid profile and --project required")
	}
	if e := secure.URL(*server, *insecure); e != nil {
		return e
	}
	var token string
	if *tokenStdin {
		b, e := io.ReadAll(io.LimitReader(a.In, 1025))
		if e != nil {
			return e
		}
		if len(b) > 1024 {
			return errors.New("token input too long")
		}
		token = strings.TrimSpace(string(b))
	} else {
		if a.TerminalIn == nil || a.TerminalOut == nil {
			return errors.New("use --token-stdin outside a terminal")
		}
		fmt.Fprint(a.Out, "Bearer token (not echoed): ")
		token, e = tui.ReadSecret(ctx, a.TerminalIn, a.TerminalOut)
		if e != nil {
			return e
		}
	}
	p := client.Profile{Project: *project, Servers: []client.Endpoint{{URL: *server, Token: token, CAFile: *ca, AllowInsecure: *insecure}}}
	c, e := client.New(p)
	if e != nil {
		return e
	}
	defer c.Close()
	if _, _, e = c.Snapshot(ctx, ""); e != nil {
		return e
	}
	creds, e := client.Load(*path)
	if e != nil {
		if !errors.Is(e, os.ErrNotExist) {
			return fmt.Errorf("refusing to replace unreadable/unsafe credentials: %w", e)
		}
		creds = client.Credentials{Schema: 1, Profiles: map[string]client.Profile{}}
	}
	creds.Active = *name
	creds.Profiles[*name] = p
	if e = client.Save(*path, creds); e != nil {
		return e
	}
	fmt.Fprintf(a.Out, "Saved verified project %s as profile %s. Connect with topo.\n", *project, *name)
	return nil
}
func (a *App) init(args []string) error {
	f := a.flags("init")
	dir := f.String("dir", "./topophany-demo", "NEW demo directory (refuses existing files)")
	if e := parsed(f, args); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(filepath.Clean(*dir)), 0700); e != nil {
		return e
	}
	// Claim the final directory exclusively: a separate existence check and
	// MkdirAll permit two initializers to overwrite each other's credentials.
	if e := os.Mkdir(*dir, 0700); e != nil {
		if errors.Is(e, os.ErrExist) {
			return errors.New("init refuses an existing directory; choose a new --dir")
		}
		return e
	}
	viewer, e := secure.Token()
	if e != nil {
		return e
	}
	worker, e := secure.Token()
	if e != nil {
		return e
	}
	c := DemoConfig()
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	if e = secure.AtomicWrite(filepath.Join(*dir, "topology.json"), append(b, '\n'), 0600); e != nil {
		return e
	}
	auth := api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(viewer), Role: "viewer"}, {TokenSHA256: secure.Hash(worker), Role: "worker", Observer: "demo"}}}
	if e = api.SaveAuth(filepath.Join(*dir, "auth.json"), auth); e != nil {
		return e
	}
	for name, token := range map[string]string{"credentials.json": viewer, "worker-credentials.json": worker} {
		cr := client.Credentials{Schema: 1, Active: "demo", Profiles: map[string]client.Profile{"demo": {Project: c.Project, Servers: []client.Endpoint{{URL: "http://127.0.0.1:7447", Token: token}}}}}
		if e = client.Save(filepath.Join(*dir, name), cr); e != nil {
			return e
		}
	}
	fmt.Fprintf(a.Out, "Demo created at %s (only loopback probes; fresh private credentials).\n\nCollector:\n  topo serve --config %s --auth %s --data %s --observer demo\n\nIn another terminal:\n  topo --credentials %s\n\nDo not run a second executor with observer=demo concurrently.\n", *dir, filepath.Join(*dir, "topology.json"), filepath.Join(*dir, "auth.json"), filepath.Join(*dir, "data"), filepath.Join(*dir, "credentials.json"))
	return nil
}
func DemoConfig() model.Config {
	c := model.Config{Schema: 1, Project: "topophany-demo", Nodes: []model.Node{{ID: "collector", Name: "Local collector", Group: "Demo", Kind: "host"}, {ID: "home", Name: "Home workstation", Group: "Demo", Kind: "host", Notes: "Registered identity, deliberately not monitored by this local-only demo."}}, Observers: []model.Observer{{ID: "demo", Name: "Local process", Node: "collector", Context: "native local process; explicit loopback probe", StaleSeconds: 30}}, Endpoints: []model.Endpoint{{ID: "api-loopback", Node: "collector", Address: "http://127.0.0.1:7447/healthz", Network: "loopback", Observer: "demo"}}, Services: []model.Service{{ID: "panel", Name: "Topophany collector", Node: "collector", Endpoints: []string{"api-loopback"}}, {ID: "notes-only", Name: "Notes-only service", Node: "home", Notes: "No probe configured. Unknown is intentional; never counted as uptime."}}, Paths: []model.Path{{ID: "loopback", Name: "Local API access", Observer: "demo", Nodes: []string{"collector"}}}, Checks: []model.Check{{ID: "api-health", Name: "Collector health response", Observer: "demo", Endpoint: "api-loopback", Service: "panel", Path: "loopback", Kind: "http", IntervalSeconds: 5, TimeoutSeconds: 2, StaleSeconds: 15, FailureThreshold: 2, RecoveryThreshold: 2, HTTP: model.HTTPOptions{Status: []int{200}, Contains: `"status":"ok"`}}}}
	config.Defaults(&c)
	return c
}

func observerIDs(c model.Config) []string {
	ids := make([]string, 0, len(c.Observers))
	for _, o := range c.Observers {
		ids = append(ids, o.ID)
	}
	return ids
}
