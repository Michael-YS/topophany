package app

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/direct"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

type directFlags struct {
	source, root *string
	wait         *time.Duration
}

func addDirectFlags(f *flag.FlagSet) directFlags {
	return directFlags{f.String("source", "client", "current-state source: client or direct (synthetic: topo preview)"), f.String("direct-root", "", "absolute private direct configuration root"), f.Duration("wait", 15*time.Second, "overall one-shot direct first-attempt wait (0..5m)")}
}
func (d directFlags) validate(f *flag.FlagSet) error {
	if *d.source != "client" && *d.source != "direct" {
		return errors.New("--source must be client or direct; use topo preview for synthetic data")
	}
	if *d.wait < 0 || *d.wait > 5*time.Minute {
		return errors.New("--wait must be 0..5m")
	}
	if *d.source == "direct" {
		bad := false
		f.Visit(func(v *flag.Flag) {
			switch v.Name {
			case "credentials", "credential-file", "server", "ca-file":
				bad = bad || v.Value.String() != ""
			case "token-stdin", "allow-insecure":
				bad = bad || v.Value.String() != "false"
			}
		})
		if bad {
			return errors.New("direct reads use a named profile; client connection overrides do not apply")
		}
		if !config.ID(f.Lookup("profile").Value.String()) {
			return direct.AsError(&direct.Error{Code: "invalid_request", Message: "Direct reads require --profile ID."})
		}
	}
	if *d.root != "" {
		if _, e := direct.NewStore(*d.root); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) openDirect(ctx context.Context, f *flag.FlagSet, d directFlags) (*direct.Session, *direct.Store, error) {
	if e := d.validate(f); e != nil {
		return nil, nil, e
	}
	st, e := direct.NewStore(*d.root)
	if e != nil {
		return nil, nil, e
	}
	p, e := st.Load(f.Lookup("profile").Value.String())
	if e != nil {
		return nil, nil, e
	}
	if project := f.Lookup("project"); project != nil && project.Value.String() != "" && project.Value.String() != p.Project {
		return nil, nil, errors.New("direct profile project mismatch")
	}
	if observer := f.Lookup("observer"); observer != nil && observer.Value.String() != "" && observer.Value.String() != direct.Observer {
		return nil, nil, errors.New("unknown direct observer")
	}
	s, e := direct.NewSession(ctx, st, p, direct.Options{Now: a.now})
	return s, st, e
}
func (a *App) waitDirect(ctx context.Context, s *direct.Session, d directFlags) {
	wait, cancel := context.WithTimeout(ctx, *d.wait)
	defer cancel()
	s.Wait(wait)
}
func (a *App) directReader(s *direct.Session) reader {
	return reader{snapshot: func(ctx context.Context, observer string) (model.Snapshot, string, error) {
		if e := ctx.Err(); e != nil {
			return model.Snapshot{}, "", e
		}
		snap, e := s.Snapshot(observer, a.now())
		return snap, "direct", e
	}, history: func(context.Context, string, int) (client.History, string, error) {
		return client.History{}, "direct", &direct.Error{Code: "history_unsupported_in_direct_mode", Message: "History is not supported in direct mode."}
	}, rollups: func(context.Context, string, string, string) (client.Rollups, string, error) {
		return client.Rollups{}, "direct", &direct.Error{Code: "history_unsupported_in_direct_mode", Message: "History is not supported in direct mode."}
	}}
}

func (a *App) viewDirect(ctx context.Context, f *flag.FlagSet, d directFlags, observer string, jsonOut, noColor bool, device string, panelFlags monitorFlags, startupFlags *preferenceFlags) error {
	s, st, e := a.openDirect(ctx, f, d)
	if e != nil {
		return e
	}
	defer s.Close()
	if jsonOut {
		if *panelFlags.name != "" {
			return errors.New("--monitor requires a running TUI")
		}
		a.waitDirect(ctx, s, d)
		snap, _, e := a.directReader(s).snapshot(ctx, observer)
		if e != nil {
			return e
		}
		return printJSON(a.Out, snap)
	}
	if a.TerminalIn == nil || a.TerminalOut == nil {
		return errors.New("interactive terminal unavailable; use --json")
	}
	startup, e := startupFlags.startup()
	if e != nil {
		return e
	}
	startup, e = withDevice(startup, device)
	if e != nil {
		return e
	}
	p := s.Profile()
	panel, path, e := panelFlags.load("direct", p.Project, observer)
	if e != nil {
		return e
	}
	if panel != nil && panel.Profile != p.Profile {
		return errors.New("monitor direct profile differs from TUI profile")
	}
	fetch := func(ctx context.Context) (monitor.Input, error) {
		snap, _, e := a.directReader(s).snapshot(ctx, observer)
		return monitor.Input{Snapshot: snap}, e
	}
	stop, e := a.attachMonitor(ctx, panel, path, fetch, st.Root)
	if e != nil {
		return e
	}
	defer stop()
	return tui.RunDirect(ctx, a.directReader(s).snapshot, observer, noColor, a.TerminalIn, a.TerminalOut, startup)
}
