package cloudflared

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// ReadyBudget is the most a /ready body may be. The documented shape is one
// small object; anything larger is not this listener's answer.
const ReadyBudget = 4 << 10

// maxReadyConnections bounds the reported count so a malformed or hostile
// body cannot put an arbitrary integer into an observation.
const maxReadyConnections = 1024

// A connector id is kept only in a UUID shape, lowercased. It identifies one
// connector process, so it is never free text.
var connectorID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Ready is the connector's own readiness statement. Status mirrors the HTTP
// status, and Connections counts the edge sessions the connector holds.
type Ready struct {
	Status      int
	Connections int
	ConnectorID string // lowercase UUID, or empty when the body's id is not one
}

// ParseReady reads `{status, readyConnections, connectorId}` within the byte
// budget. Both numeric members are required; a body without them is not this
// listener's shape. ConnectorId is optional and is kept only when it has a
// UUID shape.
func ParseReady(body []byte) (Ready, error) {
	if len(body) > ReadyBudget {
		return Ready{}, errors.New("ready response exceeds its budget")
	}
	if err := secure.JSONResponseBudget(body, 8, 4, 8); err != nil {
		return Ready{}, err
	}
	var v struct {
		Status           *int   `json:"status"`
		ReadyConnections *int   `json:"readyConnections"`
		ConnectorID      string `json:"connectorId"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return Ready{}, err
	}
	if v.Status == nil || v.ReadyConnections == nil {
		return Ready{}, errors.New("ready response lacks status or readyConnections")
	}
	if *v.Status < 100 || *v.Status > 599 || *v.ReadyConnections < 0 || *v.ReadyConnections > maxReadyConnections {
		return Ready{}, errors.New("ready response value out of range")
	}
	r := Ready{Status: *v.Status, Connections: *v.ReadyConnections}
	if connectorID.MatchString(v.ConnectorID) {
		r.ConnectorID = strings.ToLower(v.ConnectorID)
	}
	return r, nil
}
