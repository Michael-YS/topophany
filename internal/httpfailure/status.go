// Package httpfailure classifies HTTP failures without choosing a caller's
// retry cadence or deciding whether a particular payload may be discarded.
package httpfailure

type Kind uint8

const (
	Other Kind = iota
	Authentication
	Permission
	RateLimited
	Transient
	Rejected
)

func Classify(status int) Kind {
	switch {
	case status == 401:
		return Authentication
	case status == 403:
		return Permission
	case status == 429:
		return RateLimited
	case status == 408 || status >= 500 && status <= 599:
		return Transient
	case status >= 400 && status <= 499:
		return Rejected
	default:
		return Other
	}
}
