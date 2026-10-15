package bootstrap

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Options struct {
	Role, Platform, User, InstallDir, DataDir, Server, Observer, Binary string
	TLSHosts                                                            []string
	Config                                                              model.Config
	Profile                                                             client.Profile
	CA                                                                  []byte
}
type File struct {
	Source  string `json:"source"`
	Name    string `json:"name"`
	SHA256  string `json:"sha256"`
	Mode    uint32 `json:"mode"`
	Private bool   `json:"private"`
}
type Manifest struct {
	Server string `json:"server,omitempty"`

	Schema      int    `json:"schema"`
	Version     string `json:"version"`
	Role        string `json:"role"`
	Platform    string `json:"platform"`
	User        string `json:"user,omitempty"`
	Project     string `json:"project"`
	Observer    string `json:"observer,omitempty"`
	InstallDir  string `json:"install_dir"`
	DataDir     string `json:"data_dir,omitempty"`
	Files       []File `json:"files"`
	Service     string `json:"service,omitempty"`
	ServiceFile string `json:"service_file,omitempty"`
}

func targetPath(dir, name string) string { return strings.TrimRight(dir, "/\\") + "/" + name }
func cleanTarget(p string, platform string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	absolute := path.IsAbs(p)
	if platform == "windows" {
		absolute = len(p) > 3 && p[1] == ':' && p[2] == '/' && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z'))
	}
	return absolute && path.Clean(p) == p && secure.Text(p) == p && !strings.ContainsAny(p, "\n\r\x00")
}
func Generate(dir string, o Options) (Manifest, error) {
	m := Manifest{Schema: 1, Version: model.Version, Role: o.Role, Server: o.Server, Platform: o.Platform, User: o.User, Project: o.Config.Project, Observer: o.Observer, InstallDir: o.InstallDir, DataDir: o.DataDir}
	if o.Platform == "" {
		m.Platform = runtime.GOOS
		o.Platform = m.Platform
	}
	if o.DataDir == o.InstallDir && o.Role != "client" {
		return m, errors.New("data and installation directories must differ")
	}
	if m.Platform != "linux" && m.Platform != "darwin" && m.Platform != "windows" {
		return m, errors.New("platform must be linux, darwin or windows")
	}
	if o.Role != "server" && o.Role != "worker" && o.Role != "client" {
		return m, errors.New("role must be server, worker or client")
	}
	if !cleanTarget(o.InstallDir, o.Platform) || o.Role != "client" && !cleanTarget(o.DataDir, o.Platform) {
		return m, errors.New("installation and data paths must be clean absolute target-platform paths")
	}
	if o.Role != "client" && o.Platform != "windows" && (!config.ID(o.User) || o.User == "root") {
		return m, errors.New("service requires an explicit unprivileged user")
	}
	if o.Role != "client" {
		if err := config.Validate(o.Config); err != nil {
			return m, err
		}
		if o.Role == "worker" || o.Observer != "" {
			found := false
			for _, ob := range o.Config.Observers {
				found = found || ob.ID == o.Observer
			}
			if !found {
				return m, errors.New("observer must be declared in reviewed topology")
			}
		}
	}
	if !config.ID(m.Project) {
		return m, errors.New("valid project required")
	}
	if err := secure.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return m, err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return m, fmt.Errorf("choose a new bundle directory: %w", err)
	}
	if runtime.GOOS == "windows" {
		if e := restrictWindowsDirectory(context.Background(), dir); e != nil {
			os.RemoveAll(dir)
			return m, e
		}
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(dir)
		}
	}()
	write := func(name string, b []byte, mode os.FileMode) error {
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := secure.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		return secure.AtomicWrite(dst, b, mode)
	}
	add := func(name string, b []byte, mode os.FileMode, private bool) error {
		src := "payload/" + name
		if err := write(src, b, mode); err != nil {
			return err
		}
		m.Files = append(m.Files, File{src, name, secure.Hash(string(b)), uint32(mode), private})
		return nil
	}
	jadd := func(name string, v any, private bool) error {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			return e
		}
		mode := os.FileMode(0644)
		if private {
			mode = 0600
		}
		return add(name, append(b, '\n'), mode, private)
	}
	exeName := "bin/topo"
	if o.Platform == "windows" {
		exeName += ".exe"
	}
	binary, e := secure.ReadBounded(o.Binary, 64<<20)
	if e != nil {
		return m, e
	}
	if err := validBinary(binary, o.Platform); err != nil {
		return m, err
	}
	if e = add(exeName, binary, 0755, false); e != nil {
		return m, e
	}
	profile := o.Profile
	if o.Role == "server" {
		u, e := url.Parse(o.Server)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return m, errors.New("server needs an HTTPS origin URL")
		}
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1024 || port > 65535 {
			return m, errors.New("server URL needs an explicit unprivileged port (1024..65535)")
		}
		for _, host := range o.TLSHosts {
			if host == "" || len(host) > 253 || strings.ContainsAny(host, " /@\r\n\t") {
				return m, errors.New("invalid TLS host")
			}
		}
		ca, key, cert, serverKey, e := certificates(append([]string{u.Hostname()}, o.TLSHosts...))
		if e != nil {
			return m, e
		}
		for name, b := range map[string][]byte{"ca.pem": ca, "server.pem": cert, "server-key.pem": serverKey} {
			mode := os.FileMode(0644)
			if name == "server-key.pem" {
				mode = 0600
			}
			if e = add(name, b, mode, name == "server-key.pem"); e != nil {
				return m, e
			}
		}
		// The signing key stays in the administrator's bundle, outside service runtime.
		if e = write("authority/ca-key.pem", key, 0600); e != nil {
			return m, e
		}
		viewer, e := secure.Token()
		if e != nil {
			return m, e
		}
		auth := api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(viewer), Role: "viewer"}}}
		profile = client.Profile{Project: m.Project, Servers: []client.Endpoint{{URL: o.Server, Token: viewer, CAFile: targetPath(o.InstallDir, "ca.pem")}}}
		for _, ob := range o.Config.Observers {
			if ob.ID == o.Observer {
				continue
			}
			token, e := secure.Token()
			if e != nil {
				return m, e
			}
			auth.Tokens = append(auth.Tokens, api.Principal{TokenSHA256: secure.Hash(token), Role: "worker", Observer: ob.ID})
			p := client.Profile{Project: m.Project, Servers: []client.Endpoint{{URL: o.Server, Token: token, CAFile: "ca.pem"}}}
			b, _ := json.MarshalIndent(credentials(p), "", "  ")
			if e = write("enrollment/"+ob.ID+"/credentials.json", b, 0600); e != nil {
				return m, e
			}
			if e = write("enrollment/"+ob.ID+"/ca.pem", ca, 0600); e != nil {
				return m, e
			}
		}
		if e = jadd("auth.json", auth, true); e != nil {
			return m, e
		}
	} else {
		if profile.Project != m.Project {
			return m, errors.New("credential project mismatch")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(o.CA) {
			return m, errors.New("valid trusted CA PEM required")
		}
		for i := range profile.Servers {
			ep := &profile.Servers[i]
			if !strings.HasPrefix(ep.URL, "https://") || ep.AllowInsecure {
				return m, errors.New("installation bundles require HTTPS")
			}
			ep.CAFile = targetPath(o.InstallDir, "ca.pem")
		}
		if e = profile.Validate(); e != nil {
			return m, e
		}
		if e = add("ca.pem", o.CA, 0644, false); e != nil {
			return m, e
		}
	}
	credentialName := "credentials.json"
	if o.Role == "worker" {
		credentialName = "worker-credentials.json"
	}
	if e = jadd(credentialName, credentials(profile), true); e != nil {
		return m, e
	}
	if o.Role != "client" {
		if e = jadd("topology.json", o.Config, true); e != nil {
			return m, e
		}
		serviceName, serviceFile, raw, e := service(o)
		if e != nil {
			return m, e
		}
		m.Service = serviceName
		m.ServiceFile = serviceFile
		if e = write("service", raw, 0600); e != nil {
			return m, e
		}
	}
	raw, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return m, e
	}
	if e = write("manifest.json", raw, 0600); e != nil {
		return m, e
	}
	instructions := fmt.Sprintf("Topophany %s %s bundle (%s)\n\nReview manifest.json, payload/topology.json (when present), and service.\nInstall locally: topo install --bundle %q\nPreview paths: topo install --bundle %q --dry-run\nService installation on Linux/macOS needs an administrator; the service runs unprivileged.\nClient installation does not start a background process.\nThe installer prints a private backup directory; rollback with topo install --rollback DIR.\nWorker endpoint changes require draining/archiving pending outboxes first.\nWindows services are current-user logon tasks.\nKeep authority/ca-key.pem with the administrator; it is never installed.\nKeep enrollment bundles private; each has one observer-scoped worker token.\n", model.Version, o.Role, o.Platform, dir, dir)
	if e = write("README.txt", []byte(instructions), 0600); e != nil {
		return m, e
	}
	success = true
	return m, nil
}
func credentials(p client.Profile) client.Credentials {
	return client.Credentials{Schema: 1, Active: "primary", Profiles: map[string]client.Profile{"primary": p}}
}
func validBinary(b []byte, platform string) error {
	ok := len(b) > 4 && (platform == "linux" && string(b[:4]) == "\x7fELF" || platform == "windows" && string(b[:2]) == "MZ" || platform == "darwin" && (string(b[:4]) == "\xcf\xfa\xed\xfe" || string(b[:4]) == "\xfe\xed\xfa\xcf" || string(b[:4]) == "\xca\xfe\xba\xbe"))
	if !ok {
		return errors.New("binary does not match target platform")
	}
	return nil
}
