//go:build webui

package app

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net/http"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/tui"
	"github.com/WhitenWhiten/topophany/internal/webui"
)

type webFlags struct {
	enabled     *bool
	listen      *string
	proxyOrigin *string
}

func addWebFlags(f *flag.FlagSet) webFlags {
	return webFlags{
		enabled:     f.Bool("web", false, "enable the native read-only Web UI"),
		listen:      f.String("web-listen", "127.0.0.1:8080", "Web UI listener (non-loopback requires TLS)"),
		proxyOrigin: f.String("web-proxy-origin", "", "exact HTTPS origin for a loopback reverse proxy (requires --web)"),
	}
}
func (f webFlags) validate() error {
	if *f.proxyOrigin != "" && !*f.enabled {
		return errors.New("web-proxy-origin requires --web")
	}
	return webui.ValidateProxyOrigin(*f.proxyOrigin)
}
func (a *App) startWeb(f webFlags, handler http.Handler, tc *tls.Config, failures chan<- error) (func(), error) {
	if !*f.enabled {
		return func() {}, nil
	}
	stop, addr, err := webui.Start(*f.listen, *f.proxyOrigin, handler, tc, failures)
	if err != nil {
		return nil, err
	}
	scheme := "http"
	if tc != nil {
		scheme = "https"
	}
	fmt.Fprintf(a.Err, "Native Web UI listening on %s://%s\n", scheme, addr)
	return stop, nil
}

func configureWeb(backend *api.Server, enabled bool) {
	backend.WebEnabled = enabled
	if enabled {
		backend.WebMap = tui.NativeMap
	}
}
