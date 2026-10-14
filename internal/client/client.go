// Package client is independent of the TUI. Read failover never becomes write
// failover, and every successful response is checked against a pinned project.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Endpoint struct {
	URL           string `json:"url"`
	Token         string `json:"token,omitempty"`
	TokenEnv      string `json:"token_env,omitempty"`
	CAFile        string `json:"ca_file,omitempty"`
	AllowInsecure bool   `json:"allow_insecure,omitempty"`
}
type Profile struct {
	Project string     `json:"project"`
	Servers []Endpoint `json:"servers"`
}
type Credentials struct {
	Schema   int                `json:"schema"`
	Active   string             `json:"active"`
	Profiles map[string]Profile `json:"profiles"`
}

func DefaultPath() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "topophany", "credentials.json"), nil
}
func Load(path string) (Credentials, error) {
	var c Credentials
	b, e := secure.PrivateRead(path)
	if e != nil {
		return c, e
	}
	if e = secure.Decode(strings.NewReader(string(b)), &c, 1<<20); e != nil {
		return c, e
	}
	if c.Schema != 1 || len(c.Profiles) == 0 || !config.ID(c.Active) {
		return c, errors.New("invalid credential schema or active profile")
	}
	if _, ok := c.Profiles[c.Active]; !ok {
		return c, errors.New("active profile missing")
	}
	for k, p := range c.Profiles {
		if !config.ID(k) {
			return c, errors.New("invalid profile name")
		}
		if e = p.Validate(); e != nil {
			return c, e
		}
	}
	return c, nil
}
func Save(path string, c Credentials) error {
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	return secure.AtomicWrite(path, append(b, '\n'), 0600)
}
func (p Profile) Validate() error {
	if !config.ID(p.Project) || len(p.Servers) == 0 || len(p.Servers) > 8 {
		return errors.New("profile needs project and 1..8 collector URLs")
	}
	seen := map[string]bool{}
	for _, s := range p.Servers {
		if e := secure.URL(s.URL, s.AllowInsecure); e != nil {
			return e
		}
		if seen[s.URL] {
			return errors.New("duplicate collector URL")
		}
		seen[s.URL] = true
		if s.Token != "" && s.TokenEnv != "" {
			return errors.New("choose token or token_env, not both")
		}
		if s.TokenEnv == "" {
			if e := secure.CheckToken(s.Token); e != nil {
				return e
			}
		}
	}
	return nil
}

type Client struct {
	profile    Profile
	transports []*http.Client
	Now        func() time.Time
}

func New(p Profile) (*Client, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	c := &Client{profile: p, Now: time.Now}
	for _, ep := range p.Servers {
		h, e := HTTP(ep)
		if e != nil {
			c.Close()
			return nil, e
		}
		c.transports = append(c.transports, h)
	}
	return c, nil
}
func HTTP(ep Endpoint) (*http.Client, error) {
	if e := secure.URL(ep.URL, ep.AllowInsecure); e != nil {
		return nil, e
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if ep.CAFile != "" {
		b, e := secure.ReadBounded(ep.CAFile, 1<<20)
		if e != nil {
			return nil, errors.New("collector CA file unreadable")
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if len(b) > 1<<20 || !roots.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid collector CA file")
		}
		tc.RootCAs = roots
	}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: tc, MaxResponseHeaderBytes: 32768, TLSHandshakeTimeout: 4 * time.Second, ResponseHeaderTimeout: 6 * time.Second, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConnsPerHost: 2}
	return &http.Client{Transport: tr, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func (c *Client) Close() {
	for _, h := range c.transports {
		h.CloseIdleConnections()
	}
}
func endpointURL(ep Endpoint, path string, q url.Values) string {
	u, _ := url.Parse(ep.URL)
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	return u.String()
}
func authorize(req *http.Request, ep Endpoint) error {
	token := ep.Token
	if ep.TokenEnv != "" {
		v, e := secure.SecretEnv(ep.TokenEnv)
		if e != nil {
			return e
		}
		token = v
	}
	if e := secure.CheckToken(token); e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	return nil
}
func (c *Client) Snapshot(ctx context.Context, observer string) (model.Snapshot, string, error) {
	var empty model.Snapshot
	for i, ep := range c.profile.Servers {
		req, e := http.NewRequestWithContext(ctx, "GET", endpointURL(ep, "/v1/snapshot", url.Values{"observer": {observer}}), nil)
		if e != nil {
			return empty, "", errors.New("invalid request")
		}
		if e = authorize(req, ep); e != nil {
			return empty, "", e
		}
		resp, e := c.transports[i].Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return empty, "", ctx.Err()
			}
			continue
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, model.MaxSnapshotBytes+1))
		resp.Body.Close()
		if e != nil {
			continue
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return empty, "", errors.New("collector authentication/authorization rejected; not falling through silently")
		}
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			continue
		}
		if resp.StatusCode != 200 {
			return empty, "", fmt.Errorf("collector rejected read (HTTP %d)", resp.StatusCode)
		}
		if len(b) > model.MaxSnapshotBytes {
			return empty, "", errors.New("snapshot budget exceeded")
		}
		var snap model.Snapshot
		if e = decodeSnapshot(b, &snap); e != nil {
			return empty, "", errors.New("invalid collector snapshot")
		}
		if snap.Project != c.profile.Project || snap.Topology.Project != c.profile.Project {
			return empty, "", errors.New("project identity mismatch; response rejected")
		}
		if snap.Schema != model.SnapshotSchema || snap.GeneratedAt.IsZero() {
			return empty, "", errors.New("unsupported snapshot contract")
		}
		if snap.ViewObserver != observer {
			return empty, "", errors.New("observer view mismatch")
		}
		// A serving HTTP process with an old snapshot is not a healthy data source.
		if c.Now().Sub(snap.GeneratedAt) > 30*time.Second || snap.GeneratedAt.After(c.Now().Add(30*time.Second)) {
			continue
		}
		return snap, ep.URL, nil
	}
	return empty, "", fmt.Errorf("%w with a current, matching snapshot", ErrNoCollector)
}

// ErrNoCollector is a read that no configured collector answered: each was
// unreachable, failing or, for a snapshot, stale.
var ErrNoCollector = errors.New("no configured collector answered")

// History is a check's raw samples under its current contract, newest
// first. Revision is the topology's and Contract the check's, both public.
type History struct {
	Project  string              `json:"project"`
	Revision string              `json:"revision"`
	Check    string              `json:"check"`
	Contract string              `json:"contract"`
	Entries  []model.Observation `json:"entries"`
}

// History reads up to limit (1..500) of a check's samples, failing over as
// Snapshot does: past unreachable or failing collectors, never past a
// rejection.
func (c *Client) History(ctx context.Context, check string, limit int) (History, string, error) {
	var empty History
	if !config.ID(check) || limit < 1 || limit > 500 {
		return empty, "", errors.New("history needs a check ID and a limit of 1..500")
	}
	for i, ep := range c.profile.Servers {
		req, e := http.NewRequestWithContext(ctx, "GET", endpointURL(ep, "/v1/history", url.Values{"check": {check}, "limit": {fmt.Sprint(limit)}}), nil)
		if e != nil {
			return empty, "", errors.New("invalid request")
		}
		if e = authorize(req, ep); e != nil {
			return empty, "", e
		}
		resp, e := c.transports[i].Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return empty, "", ctx.Err()
			}
			continue
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, maxHistoryBytes+1))
		resp.Body.Close()
		if e != nil {
			continue
		}
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return empty, "", errors.New("collector authentication/authorization rejected; not falling through silently")
		case resp.StatusCode == 404:
			return empty, "", fmt.Errorf("collector has no check %q", check)
		case resp.StatusCode >= 500 || resp.StatusCode == 429:
			continue
		case resp.StatusCode != 200:
			return empty, "", fmt.Errorf("collector rejected read (HTTP %d)", resp.StatusCode)
		case len(b) > maxHistoryBytes:
			return empty, "", errors.New("history budget exceeded")
		}
		var h History
		if e = secure.Decode(bytes.NewReader(b), &h, maxHistoryBytes); e != nil {
			return empty, "", errors.New("invalid collector history")
		}
		if h.Project != c.profile.Project {
			return empty, "", errors.New("project identity mismatch; response rejected")
		}
		if len(h.Revision) != 64 || len(h.Contract) != 64 || h.Check != check || len(h.Entries) > limit {
			return empty, "", errors.New("invalid collector history")
		}
		for _, v := range h.Entries {
			if v.Check != check || v.Project != h.Project || v.Contract != h.Contract || v.Revision != "" || len(v.Facts) > 32 || len(v.Timings) > 16 {
				return empty, "", errors.New("invalid collector history")
			}
		}
		return h, ep.URL, nil
	}
	return empty, "", fmt.Errorf("%w with the history of %s", ErrNoCollector, check)
}

// A collector bounds history to 1 MiB of samples; the envelope adds little.
const maxHistoryBytes = 2 << 20

type HTTPError struct {
	Status int
	Code   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("collector HTTP %d: %s", e.Status, e.Code) }

// Permanent means the executor must quarantine this batch for operator review,
// rather than retry forever or silently discard the observation.
func (e *HTTPError) Permanent() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != 408 && e.Status != 429
}
func PostBatch(ctx context.Context, h *http.Client, ep Endpoint, b model.Batch) error {
	body, e := json.Marshal(b)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", endpointURL(ep, "/v1/observations", nil), strings.NewReader(string(body)))
	if e != nil {
		return errors.New("invalid collector request")
	}
	if e = authorize(req, ep); e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := h.Do(req)
	if e != nil {
		return errors.New("collector transport unavailable")
	}
	defer resp.Body.Close()
	payload, e := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if e != nil || len(payload) > 4096 {
		return errors.New("invalid collector acknowledgement")
	}
	if resp.StatusCode != 202 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(payload, &v)
		code := secure.Text(v.Error)
		if len(code) > 128 {
			code = "invalid_error"
		}
		return &HTTPError{Status: resp.StatusCode, Code: code}
	}
	var ack struct {
		Project  string `json:"project"`
		Revision string `json:"revision"`
		Accepted int    `json:"accepted"`
	}
	if e = json.Unmarshal(payload, &ack); e != nil || ack.Project != b.Project || ack.Revision != b.Revision || ack.Accepted != len(b.Observations) {
		return errors.New("collector acknowledgement identity/count mismatch")
	}
	return nil
}
