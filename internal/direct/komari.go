package direct

import (
	"context"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/komari"
	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/source"
)

type komariConnection struct{ source SourceConfig }

func (*komariConnection) Close() {}

// API keys are opaque, nonrenewable credentials. A local fingerprint keeps a
// changed environment key from silently replacing a saved source's identity.
func komariSubject(token string) string { return "komari:key:" + secure.Hash(token) }

func komariToken(c Credential, subject string) (string, error) {
	if c.Mode == "anonymous" && c.Subject == "komari:anonymous" && subject == c.Subject {
		return "", nil
	}
	if c.Mode != "token" && c.Mode != "env" {
		return "", fail("auth_method_unsupported")
	}
	token := c.Token
	if c.Mode == "env" {
		var err error
		token, err = secure.SecretEnv(c.Env)
		if err != nil {
			return "", fail("auth_required")
		}
	}
	if !sourceutil.ValidCredential(token, 12, 1024-len("Bearer ")) || c.Subject != komariSubject(token) || subject != c.Subject {
		return "", fail("auth_required")
	}
	return token, nil
}

func (s *Store) authenticateKomari(r Request) (Credential, Connection, error) {
	if err := r.Source.Validate(); err != nil {
		return Credential{}, nil, err
	}
	c := Credential{Mode: r.Auth.Mode, Token: r.Auth.Token, Env: r.Auth.Name}
	if c.Mode == "saved" {
		p, err := s.Load(r.Profile)
		if err != nil {
			return c, nil, err
		}
		entry, ok := findEntry(p, r.Source.ID)
		if !ok {
			return c, nil, fail("source_not_found")
		}
		if entry.Source.URL != r.Source.URL || entry.Source.Adapter != "komari" {
			return c, nil, fail("invalid_request")
		}
		c, err = s.credential(entry.Credential)
		if err != nil {
			return c, nil, err
		}
		if _, err := komariToken(c, entry.Subject); err != nil {
			return c, nil, err
		}
	} else {
		switch c.Mode {
		case "anonymous":
			c.Subject = "komari:anonymous"
		case "token", "env":
			token := c.Token
			if c.Mode == "env" {
				var err error
				token, err = secure.SecretEnv(c.Env)
				if err != nil {
					return c, nil, fail("auth_required")
				}
			}
			if !sourceutil.ValidCredential(token, 12, 1024-len("Bearer ")) {
				return c, nil, fail("auth_required")
			}
			c.Subject = komariSubject(token)
		default:
			return c, nil, fail("auth_method_unsupported")
		}
	}
	return c, &komariConnection{source: r.Source}, nil
}

func (h *komariConnection) Discover(ctx context.Context, c Credential) ([]System, bool, error) {
	token, err := komariToken(c, c.Subject)
	if err != nil {
		return nil, false, err
	}
	src := h.source.model(1)
	if c.Mode == "anonymous" {
		src.CredentialEnv = ""
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.source.Timeout)*time.Second)
	defer cancel()
	base, err := probe.SourceIO(model.Config{}, src, func(context.Context, model.Source) (string, error) { return token, nil })
	if err != nil {
		return nil, false, discoveryError(err)
	}
	defer base.Close()
	io := &attemptIO{IO: base, now: time.Now}
	nodes, err := komari.Discover(ctx, io, src)
	if io.failure != nil {
		AsError(io.failure).RetryAt = io.retry
		return nil, false, io.failure
	}
	if err != nil {
		return nil, false, discoveryError(err)
	}
	systems := make([]System, 0, len(nodes))
	for _, n := range nodes {
		systems = append(systems, System{ID: n.ID, Name: n.Name})
	}
	return systems, false, nil
}

func discoveryError(err error) error {
	switch source.Code(err) {
	case "source_tls":
		return fail("tls_error")
	case "transport_error", "source_canceled":
		return fail("connection_failed")
	case "komari_discovery_budget", "response_budget":
		return fail("discovery_limit")
	default:
		return fail("upstream_incompatible")
	}
}
