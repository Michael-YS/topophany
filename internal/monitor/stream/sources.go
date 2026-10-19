package stream

import (
	"context"
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/preview"
)

// OpenSource prepares either saved client credentials or synthetic evidence.
// Embedding callers may supply their own Open function to Run instead.
func OpenSource(ctx context.Context, r Request) (Source, *Error) {
	return openSource(ctx, r, time.Now)
}
func openSource(ctx context.Context, r Request, now func() time.Time) (Source, *Error) {
	if r.Source == "preview" {
		c, err := preview.Preset(r.Lang)
		if err != nil {
			return Source{}, failure("invalid_request", "preview language unavailable")
		}
		sim := preview.New(c, now(), r.Lang)
		if r.Scenario >= len(sim.Scenarios()) {
			return Source{}, failure("invalid_request", "unknown preview scenario")
		}
		if r.Observer != "" {
			found := false
			for _, o := range c.Observers {
				found = found || o.ID == r.Observer
			}
			if !found {
				return Source{}, failure("invalid_request", "unknown preview observer")
			}
		}
		if r.Project != "" && r.Project != c.Project {
			return Source{}, failure("project_mismatch", "preview project does not match request")
		}
		sim.SetScenario(r.Scenario, now().Add(-30*time.Minute))
		fetch := func(ctx context.Context) (monitor.Input, error) {
			if err := ctx.Err(); err != nil {
				return monitor.Input{}, err
			}
			at := now()
			s, err := sim.Snapshot(r.Observer, at)
			in := monitor.Input{Snapshot: s, Unavailable: err != nil}
			if err == nil && r.Observer != "" {
				all, e := sim.Snapshot("", at)
				if e == nil {
					in.AllSnapshot = &all
				}
			}
			return in, err
		}
		initial, err := fetch(ctx)
		if err != nil {
			initial = monitor.Input{Unavailable: true}
		}
		return Source{Project: c.Project, Initial: initial, Fetch: fetch}, nil
	}
	path := r.Credentials
	if path == "" {
		var err error
		path, err = client.DefaultPath()
		if err != nil {
			return Source{}, failure("credentials_unavailable", "credential file unavailable")
		}
	}
	creds, err := client.Load(path)
	if err != nil {
		return Source{}, failure("credentials_unavailable", "credential file unavailable")
	}
	profile := r.Profile
	if profile == "" {
		profile = creds.Active
	}
	p, ok := creds.Profiles[profile]
	if !ok {
		return Source{}, failure("profile_not_found", "credential profile not found")
	}
	if r.Project != "" && p.Project != r.Project {
		return Source{}, failure("project_mismatch", "profile project does not match request")
	}
	cl, err := client.New(p)
	if err != nil {
		return Source{}, failure("credentials_unavailable", "credential profile unavailable")
	}
	sourceErrors := make(chan *Error, 1)
	previous := monitor.Input{Unavailable: true}
	fetch := func(ctx context.Context) (monitor.Input, error) {
		s, all, err := cl.SnapshotViews(ctx, r.Observer)
		if errors.Is(err, client.ErrProjectMismatch) {
			select {
			case sourceErrors <- failure("project_mismatch", "snapshot project does not match request"):
			default:
			}
			return previous, nil
		}
		if err != nil {
			return monitor.Input{}, err
		}
		in := monitor.Input{Snapshot: s, AllSnapshot: all}
		previous = in
		return in, nil
	}
	prepare := func(ctx context.Context) (monitor.Input, *Error) {
		in, err := fetch(ctx)
		select {
		case e := <-sourceErrors:
			return monitor.Input{}, e
		default:
		}
		if err != nil {
			// An unreachable collector is a valid, unavailable source; a
			// positively identified foreign project is a rejected replacement.
			in = monitor.Input{Unavailable: true}
		}
		return in, nil
	}
	return Source{Project: p.Project, Initial: previous, Prepare: prepare, Fetch: fetch, Close: cl.Close, Errors: sourceErrors}, nil
}
