package bootstrap

import (
	"errors"
	"github.com/WhitenWhiten/topophany/internal/config"
	"runtime"
	"strconv"
)

func validateRollback(r installation) error {
	m := r.Manifest
	if m.Schema != 1 || m.Platform != runtime.GOOS || !config.ID(m.Project) || !cleanTarget(m.InstallDir, m.Platform) || m.Role != "server" && m.Role != "worker" && m.Role != "client" {
		return errors.New("invalid rollback manifest")
	}
	serviceName, serviceFile := "", ""
	if m.Role != "client" {
		switch m.Platform {
		case "linux":
			serviceName = "topophany-" + m.Role + ".service"
			serviceFile = "/etc/systemd/system/" + serviceName
		case "darwin":
			serviceName = "com.topophany." + m.Role
			serviceFile = "/Library/LaunchDaemons/" + serviceName + ".plist"
		case "windows":
			serviceName = "Topophany-" + m.Role
		}
	}
	if m.Service != serviceName || m.ServiceFile != serviceFile {
		return errors.New("unexpected rollback service")
	}
	names := map[string]bool{"bin/topo": m.Platform != "windows", "bin/topo.exe": m.Platform == "windows", "ca.pem": true, "credentials.json": true, "worker-credentials.json": m.Role == "worker", "auth.json": m.Role == "server", "server-key.pem": m.Role == "server", "server.pem": m.Role == "server", "topology.json": m.Role != "client"}
	targets := map[string]bool{}
	for _, f := range m.Files {
		if !names[f.Name] {
			return errors.New("unexpected rollback payload")
		}
		targets[targetPath(m.InstallDir, f.Name)] = true
	}
	if m.ServiceFile != "" {
		targets[m.ServiceFile] = true
	}
	if len(r.Files) != len(targets) {
		return errors.New("incomplete rollback record")
	}
	for i, f := range r.Files {
		if !targets[f.Target] || f.Saved != "" && f.Saved != strconv.Itoa(i) || f.Mode > 0777 {
			return errors.New("invalid rollback file")
		}
		delete(targets, f.Target)
	}
	return nil
}
