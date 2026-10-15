package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

// Verify proves TLS trust and role/project binding without writing observations.
func Verify(ctx context.Context, dir string) error {
	m, e := loadManifest(dir)
	if e != nil {
		return e
	}
	if m.Role == "server" {
		return errors.New("start server first; verify worker/client enrollment against it")
	}
	name := "credentials.json"
	endpoint := "/v1/snapshot"
	if m.Role == "worker" {
		name = "worker-credentials.json"
		endpoint = "/v1/worker"
	}
	c, e := client.Load(filepath.Join(dir, "payload", name))
	if e != nil {
		return e
	}
	for _, ep := range c.Profiles[c.Active].Servers {
		ep.CAFile = filepath.Join(dir, "payload/ca.pem")
		h, e := client.HTTP(ep)
		if e != nil {
			return e
		}
		token := ep.Token
		if ep.TokenEnv != "" {
			token, e = secure.SecretEnv(ep.TokenEnv)
			if e != nil {
				return e
			}
		}
		req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(ep.URL, "/")+endpoint, nil)
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, e := h.Do(req)
		h.CloseIdleConnections()
		if e != nil {
			return errors.New("collector TLS or connection verification failed")
		}
		b, e := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
		response.Body.Close()
		if e != nil || len(b) > 8<<20 || response.StatusCode != 200 {
			return errors.New("collector enrollment verification failed")
		}
		var result struct {
			Project  string `json:"project"`
			Observer string `json:"observer"`
		}
		if e = json.Unmarshal(b, &result); e != nil || result.Project != m.Project || m.Role == "worker" && result.Observer != m.Observer {
			return errors.New("collector project or worker observer mismatch")
		}
	}
	return nil
}
