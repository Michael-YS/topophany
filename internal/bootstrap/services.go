package bootstrap

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"net/url"
	"strings"
)

func command(o Options) []string {
	exe := targetPath(o.InstallDir, "bin/topo")
	if o.Platform == "windows" {
		exe += ".exe"
	}
	args := []string{exe}
	if o.Role == "server" {
		args = append(args, "serve", "--config", targetPath(o.InstallDir, "topology.json"), "--auth", targetPath(o.InstallDir, "auth.json"), "--data", o.DataDir, "--instance", "collector-"+secure.Hash(o.Server + o.InstallDir)[:12], "--listen", "0.0.0.0:"+serverPort(o.Server), "--tls-cert", targetPath(o.InstallDir, "server.pem"), "--tls-key", targetPath(o.InstallDir, "server-key.pem"))
		if o.Observer != "" {
			args = append(args, "--observer", o.Observer)
		}
	} else {
		args = append(args, "worker", "--config", targetPath(o.InstallDir, "topology.json"), "--credentials", targetPath(o.InstallDir, "worker-credentials.json"), "--data", o.DataDir, "--observer", o.Observer)
	}
	if o.Platform == "darwin" {
		args = append(args, "--log-file", targetPath(o.DataDir, "topophany.log"), "--log-max-bytes", "5242880", "--log-backups", "3")
	}
	return args
}
func unitQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s) + `"`
}

// unitPath writes a path for a setting that takes one path, such as
// WorkingDirectory= or EnvironmentFile=: systemd does not unquote those and
// refuses the whole unit when the path is quoted. Paths are clean and
// absolute (cleanTarget), so only specifiers need escaping.
func unitPath(s string) string { return strings.ReplaceAll(s, "%", "%%") }
func service(o Options) (name, file string, raw []byte, err error) {
	args := command(o)
	switch o.Platform {
	case "linux":
		name = "topophany-" + o.Role + ".service"
		file = "/etc/systemd/system/" + name
		quoted := []string{}
		for _, a := range args {
			quoted = append(quoted, unitQuote(a))
		}
		capability := ""
		for _, x := range o.Config.Checks {
			if x.Observer == o.Observer && x.Kind == "icmp" && !x.Disabled && !x.Maintenance {
				capability = "CAP_NET_RAW"
			}
		}
		raw = []byte(fmt.Sprintf("[Unit]\nDescription=Topophany %s\nAfter=network-online.target\nWants=network-online.target\n[Service]\nType=simple\nUser=%s\nGroup=%s\nWorkingDirectory=%s\nEnvironmentFile=-%s\nExecStart=%s\nExecReload=/bin/kill -HUP $MAINPID\nRestart=on-failure\nRestartSec=10\nUMask=0077\nNoNewPrivileges=true\nProtectSystem=strict\nProtectHome=true\nPrivateTmp=true\nAmbientCapabilities=%s\nCapabilityBoundingSet=%s\nRestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK\nReadWritePaths=%s\nTimeoutStopSec=20\n[Install]\nWantedBy=multi-user.target\n", o.Role, o.User, o.User, unitPath(o.DataDir), unitPath(targetPath(o.InstallDir, "worker.env")), strings.Join(quoted, " "), capability, capability, unitQuote(o.DataDir)))
	case "darwin":
		name = "com.topophany." + o.Role
		file = "/Library/LaunchDaemons/" + name + ".plist"
		escape := func(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict>`)
		text := func(k, v string) { fmt.Fprintf(&b, "<key>%s</key><string>%s</string>", k, escape(v)) }
		text("Label", name)
		text("UserName", o.User)
		text("WorkingDirectory", o.DataDir)
		text("StandardOutPath", "/dev/null")
		text("StandardErrorPath", "/dev/null")
		b.WriteString("<key>ProgramArguments</key><array>")
		for _, a := range args {
			fmt.Fprintf(&b, "<string>%s</string>", escape(a))
		}
		b.WriteString("</array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>10</integer><key>Umask</key><integer>63</integer><key>EnvironmentVariables</key><dict><key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict></dict></plist>")
		raw = []byte(b.String())
	case "windows":
		name = "Topophany-" + o.Role
		quoted := []string{}
		for _, a := range args[1:] {
			quoted = append(quoted, windowsArg(a))
		}
		raw, err = json.Marshal(map[string]string{"task": name, "executable": args[0], "arguments": strings.Join(quoted, " ")})
	}
	return
}
func windowsArg(s string) string {
	// CommandLineToArgvW escaping, including backslashes before a closing quote.
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, c := range s {
		if c == '\\' {
			slashes++
			continue
		}
		if c == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
		}
		slashes = 0
		b.WriteRune(c)
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}

func serverPort(server string) string {
	u, _ := url.Parse(server)
	if u == nil {
		return "7447"
	}
	return u.Port()
}
