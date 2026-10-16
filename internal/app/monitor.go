package app

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func (a *App) monitor(ctx context.Context, args []string) error {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "help" || args[0] == "-h") {
		_, e := fmt.Fprintln(a.Out, "topo monitor configure|run|show|path|preview|export NAME [--monitor-dir DIR]\ntopo monitor metrics\nConfigure imports a template; run starts a client panel; preview reads the current output. Changes apply after restart.")
		return e
	}
	if len(args) == 1 && args[0] == "metrics" {
		return printJSON(a.Out, monitor.Catalog())
	}
	if len(args) < 2 {
		return errors.New("usage: topo monitor configure|run|show|path|preview|export NAME [options]; topo monitor metrics")
	}
	action, name := args[0], args[1]
	if action == "configure" {
		return a.configureMonitor(name, args[2:])
	}
	f := a.flags("monitor " + action)
	root := f.String("monitor-dir", "", "explicit monitor configuration root (also for service accounts)")
	if e := parsed(f, args[2:]); e != nil {
		return e
	}
	path, e := monitor.ConfigPath(*root, name)
	if e != nil {
		return e
	}
	c, e := monitor.Load(path)
	if e != nil {
		return e
	}
	switch action {
	case "path":
		_, e = fmt.Fprintln(a.Out, c.Output)
		return e
	case "export":
		return printJSON(a.Out, c.Template)
	case "show", "preview":
		var p monitor.Panel
		b, e := secure.ReadBounded(c.Output, 1<<20)
		state := "unavailable"
		if e == nil {
			e = secure.Decode(bytes.NewReader(b), &p, 1<<20)
		}
		if e == nil && p.Name == c.Name {
			p = monitor.Current(p, a.now())
			state = p.Lifecycle
		} else {
			p = monitor.Build(c, monitor.Input{Unavailable: true}, a.now())
		}
		if action == "preview" {
			p.Lifecycle = state
			return printJSON(a.Out, p)
		}
		runningRevision := ""
		if state == "running" {
			runningRevision = p.ConfigRevision
		}
		return printJSON(a.Out, struct {
			Config          monitor.Config `json:"config"`
			ConfigPath      string         `json:"config_path"`
			RunningRevision string         `json:"running_revision"`
			State           string         `json:"state"`
			RequiresRestart bool           `json:"requires_restart"`
		}{c, path, runningRevision, state, state == "running" && p.ConfigRevision != c.Revision})
	case "run":
		if c.Source != "client" {
			return errors.New("monitor run requires source client; attach collector/worker panels to their process")
		}
		if !c.Enabled {
			return errors.New("monitor disabled; enable it before starting")
		}
		cl, e := monitorClient(c)
		if e != nil {
			return e
		}
		defer cl.Close()
		out, e := monitor.OpenOutput(c.Output, c.Name, filepath.Dir(path), c.Credentials)
		if e != nil {
			return e
		}
		defer out.Close()
		monitor.Run(ctx, c, func(ctx context.Context) (monitor.Input, error) {
			s, _, e := cl.Snapshot(ctx, c.Observer)
			return monitor.Input{Snapshot: s}, e
		}, out, nil, func(s string) { fmt.Fprintln(a.Err, s) })
		return nil
	default:
		return errors.New("unknown monitor command")
	}
}
func monitorClient(c monitor.Config) (*client.Client, error) {
	creds, e := client.Load(c.Credentials)
	if e != nil {
		return nil, e
	}
	p, ok := creds.Profiles[c.Profile]
	if !ok {
		return nil, errors.New("monitor credential profile not found")
	}
	if c.Project != "" && p.Project != c.Project {
		return nil, errors.New("monitor profile project changed; reconfigure explicitly")
	}
	return client.New(p)
}

func (a *App) configureMonitor(name string, args []string) error {
	f := a.flags("monitor configure")
	root := f.String("monitor-dir", "", "explicit monitor configuration root")
	template := f.String("template", "", "template JSON to import; - reads stdin")
	source := f.String("source", "client", "client, collector or worker")
	credentials := f.String("credentials", "", "saved client credential file")
	profile := f.String("profile", "", "saved client profile (resolved once at configuration)")
	project := f.String("project", "", "expected project")
	observer := f.String("observer", "", "client view or worker observer")
	output := f.String("output", "", "absolute or relative output JSON path")
	interval := f.Duration("interval", 5*time.Second, "display interval (1s..1h); freshness changes and 10s publisher renewal may write sooner")
	enabled := f.Bool("enabled", true, "enable subsequent starts; changes require restart")
	if e := parsed(f, args); e != nil {
		return e
	}
	path, e := monitor.ConfigPath(*root, name)
	if e != nil {
		return e
	}
	if e = secure.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	unlock, e := secure.Lock(filepath.Join(filepath.Dir(path), "configure.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	c, e := monitor.Load(path)
	fresh := errors.Is(e, os.ErrNotExist)
	if e != nil && !fresh {
		return e
	}
	if fresh {
		c = monitor.Config{Schema: 1, Name: name, Enabled: true, Source: "client", Interval: "5s"}
		c.Output, e = monitor.OutputPath(name)
		if e != nil {
			return e
		}
		c.Credentials, e = client.DefaultPath()
		if e != nil {
			return e
		}
	}
	set := map[string]bool{}
	f.Visit(func(v *flag.Flag) { set[v.Name] = true })
	if set["source"] {
		c.Source = *source
	}
	if set["interval"] {
		c.Interval = interval.String()
	}
	if set["enabled"] {
		c.Enabled = *enabled
	}
	if set["credentials"] {
		c.Credentials, e = filepath.Abs(*credentials)
		if e != nil {
			return e
		}
	}
	if set["profile"] {
		c.Profile = *profile
	}
	if set["project"] {
		c.Project = *project
	}
	if set["observer"] {
		c.Observer = *observer
	}
	if set["output"] {
		c.Output = *output
	}
	if *template != "" {
		var r io.Reader = a.In
		if *template != "-" {
			b, err := secure.ReadBounded(*template, monitor.MaxConfigBytes)
			if err != nil {
				return err
			}
			r = bytes.NewReader(b)
		}
		if e = secure.Decode(r, &c.Template, monitor.MaxConfigBytes); e != nil {
			return e
		}
	} else if fresh {
		return errors.New("first configuration requires --template FILE or --template -")
	}
	if c.Source == "client" {
		c.Credentials, e = filepath.Abs(c.Credentials)
		if e != nil {
			return e
		}
		creds, err := client.Load(c.Credentials)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if c.Profile == "" {
			c.Profile = creds.Active
			if c.Profile == "" {
				c.Profile = "default"
			}
		}
		if p, ok := creds.Profiles[c.Profile]; ok {
			if c.Project == "" || set["profile"] || set["credentials"] {
				if !set["project"] {
					c.Project = p.Project
				}
			}
			if c.Project != p.Project {
				return errors.New("monitor profile project mismatch")
			}
		}
	} else {
		c.Credentials = ""
		c.Profile = ""
	}
	c.Output, e = monitor.PrepareOutput(c.Output, c.Name, filepath.Dir(path), c.Credentials)
	if e != nil {
		return e
	}
	if e = c.Validate(); e != nil {
		return e
	}
	// Lock before creating a new output. Existing output belongs to its running
	// writer and is never reset by a configuration update.
	var out *monitor.Output
	if _, e = os.Lstat(c.Output); errors.Is(e, os.ErrNotExist) {
		out, e = monitor.OpenOutput(c.Output, c.Name, filepath.Dir(path), c.Credentials)
		if e != nil {
			return e
		}
		defer out.Close()
	} else if e != nil {
		return e
	}
	c, e = monitor.Save(path, c)
	if e != nil {
		return e
	}
	if out != nil {
		p := monitor.Build(c, monitor.Input{Unavailable: true}, a.now())
		p.Lifecycle = "not_started"
		if e = out.Write(p, c.Every(), true); e != nil {
			return fmt.Errorf("configuration saved, initial output unavailable: %w", e)
		}
	}
	return printJSON(a.Out, struct {
		ConfigPath string `json:"config_path"`
		Template   string `json:"template"`
		Output     string `json:"output"`
		Interval   string `json:"interval"`
		State      string `json:"state"`
	}{path, "embedded; topo monitor export " + name, c.Output, c.Interval, "saved; restart required for running instances"})
}
