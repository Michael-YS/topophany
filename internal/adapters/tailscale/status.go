package tailscale

import (
	"encoding/json"
	"net/netip"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type peerStatus struct {
	ID, HostName, DNSName, CurAddr, Relay, PeerRelay string
	TailscaleIPs                                     []string
	Online                                           *bool
	Active                                           bool
	LastSeen                                         time.Time
}
type statusResponse struct {
	BackendState string
	Peer         map[string]peerStatus
	Health       []string
}

func parseStatus(raw []byte) (statusResponse, error) {
	var status statusResponse
	if len(raw) > 2<<20 {
		return status, adapters.Local("adapter", "response_limit_exceeded")
	}
	// Bound ignored users and future fields as well as peer maps before allocation.
	if secure.JSONResponseBudget(raw, 4096, 4096, 32768) != nil {
		return status, adapters.Local("adapter", "response_complexity_exceeded")
	}
	if json.Unmarshal(raw, &status) != nil || status.BackendState == "" {
		return status, adapters.Local("adapter", "unsupported_tailscale_response")
	}
	return status, nil
}

func resolvePeer(peers map[string]peerStatus, identity string) (peerStatus, string) {
	best, count := 0, 0
	var match peerStatus
	wantIP, ipErr := netip.ParseAddr(identity)
	for key, peer := range peers {
		rank := 0
		switch {
		case identity != "" && (key == identity || peer.ID == identity):
			rank = 3
		case identity != "" && (strings.EqualFold(strings.TrimSuffix(peer.DNSName, "."), strings.TrimSuffix(identity, ".")) || strings.EqualFold(peer.HostName, identity)):
			rank = 1
		}
		if ipErr == nil {
			for _, address := range peer.TailscaleIPs {
				if ip, err := netip.ParseAddr(address); err == nil && ip.Unmap() == wantIP.Unmap() {
					rank = max(rank, 2)
				}
			}
		}
		if rank > best {
			best, count, match = rank, 1, peer
		} else if rank != 0 && rank == best {
			count++
		}
	}
	if best == 0 {
		return peerStatus{}, "peer_not_found"
	}
	if count != 1 {
		return peerStatus{}, "ambiguous_peer"
	}
	return match, ""
}

func closedBackend(state string) string {
	switch state {
	case "NoState", "NeedsLogin", "NeedsMachineAuth", "Stopped", "Starting", "Running":
		return state
	default:
		return "other"
	}
}
func backendCode(state string) string {
	switch state {
	case "NeedsLogin":
		return "backend_needs_login"
	case "NeedsMachineAuth":
		return "backend_needs_machine_auth"
	case "Stopped":
		return "backend_stopped"
	case "Starting", "NoState":
		return "backend_not_ready"
	default:
		return "unsupported_backend_state"
	}
}
