package mihomo

import (
	"errors"
	"strings"
)

var errMissingProxy = errors.New("missing proxy")

func expandProxy(all map[string]coreProxy, root string) ([]string, error) {
	state := map[string]bool{}
	var visit func(string, int) ([]string, error)
	visit = func(name string, depth int) ([]string, error) {
		if depth > 64 || state[name] {
			return nil, errors.New("proxy reference cycle/depth")
		}
		if name == "DIRECT" {
			return []string{"DIRECT"}, nil
		}
		p, ok := all[name]
		if !ok {
			return nil, errMissingProxy
		}
		state[name] = true
		defer delete(state, name)
		switch strings.ToLower(p.Type) {
		case "loadbalance", "load-balance":
			return nil, errors.New("load-balanced request path is unknown")
		case "reject", "rejectdrop":
			return nil, errors.New("reject policy")
		case "selector", "select", "fallback", "urltest", "url-test":
			if p.Now == "" {
				return nil, errors.New("no selected member")
			}
			return visit(p.Now, depth+1)
		}
		if p.Now != "" {
			return visit(p.Now, depth+1)
		}
		if len(p.All) > 0 {
			return nil, errors.New("unknown group shape")
		}
		chain := []string{}
		if p.DialerProxy != "" {
			prefix, e := visit(p.DialerProxy, depth+1)
			if e != nil {
				return nil, e
			}
			chain = append(chain, prefix...)
		}
		return append(chain, name), nil
	}
	return visit(root, 0)
}
