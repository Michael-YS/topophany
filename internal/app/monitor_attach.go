package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/WhitenWhiten/topophany/internal/monitor"
)

type monitorFlags struct{ name, root *string }

func addMonitorFlags(f *flag.FlagSet) monitorFlags {
	return monitorFlags{f.String("monitor", "", "saved panel to attach to this process"), f.String("monitor-dir", "", "explicit monitor configuration root")}
}
func (f monitorFlags) load(source, project, observer string) (*monitor.Config, string, error) {
	if *f.name == "" {
		return nil, "", nil
	}
	path, e := monitor.ConfigPath(*f.root, *f.name)
	if e != nil {
		return nil, "", e
	}
	c, e := monitor.Load(path)
	if e != nil {
		return nil, "", e
	}
	if c.Source != source {
		return nil, "", errors.New("monitor source does not match process role")
	}
	if c.Project != "" && c.Project != project {
		return nil, "", errors.New("monitor project does not match process")
	}
	if c.Observer != "" && c.Observer != observer {
		return nil, "", errors.New("monitor observer does not match process view")
	}
	if !c.Enabled {
		return nil, "", nil
	}
	return &c, path, nil
}
func (a *App) attachMonitor(ctx context.Context, c *monitor.Config, path string, fetch monitor.Fetch, protected ...string) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	protected = append(protected, filepath.Dir(path), c.Credentials)
	out, e := monitor.OpenOutput(c.Output, c.Name, protected...)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { monitor.Run(ctx, *c, fetch, out, nil, func(s string) { fmt.Fprintln(a.Err, s) }) })
	return func() { cancel(); wg.Wait(); out.Close() }, nil
}
