package config

import (
	"regexp"
	"sync"
)

// Share validated body patterns with executions. Two bounded generations keep
// reloads from retaining every pattern a process has ever encountered.
var bodyPatterns struct {
	sync.Mutex
	current, previous map[string]*regexp.Regexp
}

func CompileBodyRegex(pattern string) (*regexp.Regexp, error) {
	bodyPatterns.Lock()
	defer bodyPatterns.Unlock()
	if re := bodyPatterns.current[pattern]; re != nil {
		return re, nil
	}
	re := bodyPatterns.previous[pattern]
	if re == nil {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
	}
	if bodyPatterns.current == nil || len(bodyPatterns.current) >= 128 {
		bodyPatterns.previous = bodyPatterns.current
		bodyPatterns.current = make(map[string]*regexp.Regexp)
	}
	bodyPatterns.current[pattern] = re
	return re, nil
}
