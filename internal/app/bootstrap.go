package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/bootstrap"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

func (a *App) setup(args []string) error {
	f := a.flags("init --role")
	role := f.String("role", "", "server, worker or client")
	dir := f.String("dir", "", "NEW private deployment bundle directory")
	platform := f.String("platform", runtime.GOOS, "target platform; cross-platform bundles need --binary and absolute target paths")
	cfg := f.String("config", "", "reviewed topology (server/worker)")
	server := f.String("server", "", "collector HTTPS origin URL")
	project := f.String("project", "", "project ID (client)")
	observer := f.String("observer", "", "worker or optional server observer ID")
	creds := f.String("credentials", "", "existing enrollment credentials instead of --token-stdin")
	ca := f.String("ca-file", "", "trusted CA PEM (worker/client)")
	target := f.String("install-dir", "", "absolute runtime directory; client defaults to OS config/topophany")
	data := f.String("data-dir", "", "absolute collector/outbox directory")
	identity := f.String("user", "", "unprivileged service user (Unix)")
	binary := f.String("binary", "", "matching target binary; defaults to this executable on native platform")
	hosts := f.String("tls-hosts", "", "additional comma-separated server certificate names/IPs")
	tokenStdin := f.Bool("token-stdin", false, "read a private enrollment/viewer token from stdin")
	interactive := f.Bool("interactive", false, "prompt for missing non-secret setup values")
	if e := parsed(f, args); e != nil {
		return e
	}
	input := bufio.NewReader(a.In)
	prompt := func(label string, value *string) error {
		if *value != "" {
			return nil
		}
		fmt.Fprint(a.Err, label+": ")
		line, e := input.ReadString('\n')
		if e != nil && e != io.EOF {
			return e
		}
		*value = strings.TrimSpace(line)
		return nil
	}
	if *interactive {
		if e := prompt("Role (server/worker/client)", role); e != nil {
			return e
		}
		for _, item := range []struct {
			name  string
			value *string
		}{{"New bundle directory", dir}, {"Reviewed topology JSON", cfg}} {
			if *role == "client" && item.value == cfg {
				continue
			}
			if e := prompt(item.name, item.value); e != nil {
				return e
			}
		}
		if *role == "client" {
			if e := prompt("Project ID", project); e != nil {
				return e
			}
		}
		if *role == "worker" {
			if e := prompt("Observer ID", observer); e != nil {
				return e
			}
		}
		if *creds == "" {
			if e := prompt("Collector HTTPS URL", server); e != nil {
				return e
			}
		}
		if *role != "server" {
			if e := prompt("Trusted CA PEM path", ca); e != nil {
				return e
			}
		}
	}
	if *dir == "" {
		return errors.New("choose a new --dir for the deployment bundle")
	}
	var c model.Config
	if *role != "client" {
		var e error
		c, e = config.Load(*cfg)
		if e != nil {
			return e
		}
	} else {
		c.Project = *project
	}
	if *target == "" {
		if *platform != runtime.GOOS {
			return errors.New("cross-platform bundles require --install-dir")
		}
		if *role != "client" && runtime.GOOS == "linux" {
			*target = "/etc/topophany"
		} else {
			base, e := os.UserConfigDir()
			if e != nil {
				return e
			}
			*target = filepath.Join(base, "topophany")
		}
	}
	if *data == "" {
		if *role != "client" && *platform == "linux" {
			*data = "/var/lib/topophany-" + *role
		} else {
			*data = strings.TrimRight(*target, "/\\") + "/" + *role + "-data"
		}
	}
	if *identity == "" {
		if *platform == "linux" {
			*identity = "topophany"
		} else if *platform == "darwin" && runtime.GOOS == "darwin" {
			u, e := user.Current()
			if e != nil {
				return e
			}
			*identity = u.Username
		}
	}
	if *binary == "" {
		if *platform != runtime.GOOS {
			return errors.New("cross-platform bundles require --binary")
		}
		var e error
		*binary, e = os.Executable()
		if e != nil {
			return e
		}
	}
	o := bootstrap.Options{Role: *role, Platform: *platform, User: *identity, InstallDir: *target, DataDir: *data, Server: *server, Observer: *observer, Binary: *binary, Config: c}
	if *hosts != "" {
		o.TLSHosts = strings.Split(*hosts, ",")
	}
	if *role != "server" {
		if *ca == "" {
			return errors.New("--ca-file is required")
		}
		var e error
		o.CA, e = secure.ReadBounded(*ca, 1<<20)
		if e != nil {
			return e
		}
		if *creds != "" {
			if *tokenStdin {
				return errors.New("choose credentials or token-stdin")
			}
			saved, e := client.Load(*creds)
			if e != nil {
				return e
			}
			o.Profile = saved.Profiles[saved.Active]
			if *role == "client" && c.Project == "" {
				c.Project = o.Profile.Project
				o.Config.Project = c.Project
			}
		} else {
			if !*tokenStdin {
				return errors.New("supply --credentials or --token-stdin; tokens are never command-line arguments")
			}
			b, e := io.ReadAll(io.LimitReader(input, 4097))
			if e != nil {
				return e
			}
			if len(b) > 4096 {
				return errors.New("token input too large")
			}
			token := strings.TrimSpace(string(b))
			if e = secure.CheckToken(token); e != nil {
				return e
			}
			o.Profile = client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: *server, Token: token}}}
		}
	}
	m, e := bootstrap.Generate(*dir, o)
	if e != nil {
		return e
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}
func (a *App) install(ctx context.Context, args []string) error {
	f := a.flags("install")
	dir := f.String("bundle", "", "reviewed local installation bundle")
	rollback := f.String("rollback", "", "restore a private installer backup")
	dry := f.Bool("dry-run", false, "verify payloads and print installation paths without writing")
	verify := f.Bool("verify", false, "verify TLS, project and role credentials before installing worker/client")
	if e := parsed(f, args); e != nil {
		return e
	}
	if *rollback != "" {
		if *dir != "" || *dry || *verify {
			return errors.New("rollback cannot be combined with bundle, dry-run or verify")
		}
		return bootstrap.Rollback(ctx, *rollback)
	}
	if *dir == "" {
		return errors.New("--bundle is required")
	}
	if *verify {
		if e := bootstrap.Verify(ctx, *dir); e != nil {
			return e
		}
	}
	m, backup, e := bootstrap.Install(ctx, *dir, *dry)
	if e != nil {
		if backup != "" {
			return fmt.Errorf("%w (backup: %s)", e, backup)
		}
		return e
	}
	return json.NewEncoder(a.Out).Encode(struct {
		Manifest bootstrap.Manifest `json:"manifest"`
		Backup   string             `json:"backup,omitempty"`
		Dry      bool               `json:"dry_run"`
	}{m, backup, *dry})
}
