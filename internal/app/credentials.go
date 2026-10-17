package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

// connection registers the flags that choose a collector and returns how
// to connect with them once they are parsed.
func (a *App) connection(f *flag.FlagSet) func() (*client.Client, error) {
	defaultPath, pathErr := client.DefaultPath()
	path := f.String("credentials", defaultPath, "saved client credentials")
	profile := f.String("profile", "", "saved profile (default active)")
	tokenFile := f.String("credential-file", "", "private file containing a temporary bearer token")
	tokenStdin := f.Bool("token-stdin", false, "read a temporary bearer token from stdin")
	server := f.String("server", "", "temporary collector URL override")
	project := f.String("project", "", "expected project ID; required without saved credentials")
	insecure := f.Bool("allow-insecure", false, "explicit consent for plaintext non-loopback URL")
	ca := f.String("ca-file", "", "custom trusted CA for an explicit server override")
	return func() (*client.Client, error) {
		if *tokenFile != "" && *tokenStdin {
			return nil, errors.New("--credential-file and --token-stdin are mutually exclusive")
		}
		var token string
		if *tokenFile != "" {
			b, e := secure.PrivateRead(*tokenFile)
			if e != nil {
				return nil, fmt.Errorf("temporary credential file unavailable: %w", e)
			}
			token = strings.TrimSpace(string(b))
		} else if *tokenStdin {
			var e error
			token, e = readToken(a.In)
			if e != nil {
				return nil, e
			}
		}
		if *tokenFile != "" || *tokenStdin {
			if e := secure.CheckToken(token); e != nil {
				return nil, e
			}
		}
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
		} else if *server == "" || *project == "" || token == "" {
			if errors.Is(e, os.ErrNotExist) {
				// The first run: say where to go from here.
				return nil, fmt.Errorf("no saved connection to a collector yet (%s)\n\n"+
					"  topo preview                            see the whole UI on synthetic evidence\n"+
					"  topo init --dir ./demo                  make a local demo with a collector to run\n"+
					"  topo login --server URL --project NAME  save a connection to your collector", *path)
			}
			return nil, fmt.Errorf("credentials unavailable; run topo login first (%w)", e)
		}
		if *project != "" {
			p.Project = *project
		}
		if *server != "" {
			p.Servers = []client.Endpoint{{URL: *server, Token: token, CAFile: *ca, AllowInsecure: *insecure}}
		}
		if token != "" {
			if len(p.Servers) != 1 {
				return nil, errors.New("temporary credentials require --server for a multi-server profile")
			}
			for i := range p.Servers {
				p.Servers[i].Token = token
				p.Servers[i].TokenEnv = ""
			}
		}
		return client.New(p)
	}
}

func readToken(in io.Reader) (string, error) {
	b, e := io.ReadAll(io.LimitReader(in, 1025))
	if e != nil {
		return "", e
	}
	if len(b) > 1024 {
		return "", errors.New("token input too long")
	}
	return strings.TrimSpace(string(b)), nil
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
		token, e = readToken(a.In)
		if e != nil {
			return e
		}
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
