package tailscale

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type pingResult struct {
	route   string
	latency time.Duration
}

func diagnose(ctx context.Context, command adapters.Command, options model.TailscaleOptions, peer peerStatus, o *model.Observation) error {
	if options.Diagnostic == "" {
		return nil
	}
	var target netip.Addr
	for _, value := range peer.TailscaleIPs {
		ip, err := netip.ParseAddr(value)
		if err == nil && ip.Zone() == "" && ip.IsGlobalUnicast() && !ip.IsLoopback() {
			target = ip.Unmap()
			break
		}
	}
	if !target.IsValid() {
		o.Outcome, o.Code = "unknown", "peer_diagnostic_ip_unavailable"
		o.Detail = "selected peer has no usable diagnostic IP"
		return nil
	}
	args := []string{"ping", "--c=1", "--until-direct=false", "--timeout=2s"}
	if options.Diagnostic == "tsmp" {
		args = append(args, "--tsmp")
	}
	args = append(args, target.String())
	o.Facts["diagnostic_protocol"] = options.Diagnostic
	raw, err := command(ctx, "tailscale", args...)
	if err != nil {
		if ctx.Err() != nil {
			o.Outcome, o.Code = "unknown", "diagnostic_timeout"
			if errors.Is(ctx.Err(), context.Canceled) {
				o.Code = "diagnostic_canceled"
			}
			o.Detail = "optional Tailscale diagnostic did not complete; application reachability is unverified"
			return nil
		}
		if pingTimedOut(raw, target) {
			o.Outcome, o.Code = "unknown", "diagnostic_ping_timed_out"
			o.Facts["diagnostic_response"] = "timed_out"
			o.Detail = "the peer did not answer the diagnostic within its own timeout; application reachability is unverified"
			return nil
		}
		return adapters.CommandFailure(err, "tailscale_diagnostic_failed")
	}
	result, err := parsePing(raw, target, options.Diagnostic)
	if err != nil {
		return adapters.Local("adapter", "unsupported_tailscale_diagnostic_response")
	}
	o.Facts["diagnostic_response"] = "received"
	o.Facts["diagnostic_route"] = result.route
	o.Facts["diagnostic_latency_ms"] = strconv.FormatInt(result.latency.Milliseconds(), 10)
	o.PathEvidence = "peer_runtime_reported_with_" + options.Diagnostic + "_diagnostic"
	o.Detail += "; optional diagnostic response received; application reachability is unverified"
	return nil
}

// pingTimedOut recognizes the CLI's own account of an internal timeout for
// exactly the fixed target this check names. Nothing broader: "no matching
// peer", a stopped daemon and permission errors also exit non-zero, and they
// are monitor-side failures, not a peer that did not answer.
func pingTimedOut(raw []byte, target netip.Addr) bool {
	if len(raw) == 0 || len(raw) > 256 {
		return false
	}
	text := strings.TrimSpace(string(raw))
	return !strings.ContainsAny(text, "\r\n\x00") && text == "ping \""+target.String()+"\" timed out"
}

func parsePing(raw []byte, target netip.Addr, protocol string) (pingResult, error) {
	bad := errors.New("unsupported diagnostic response")
	text := strings.TrimSpace(string(raw))
	if len(raw) > 4096 || strings.ContainsAny(text, "\r\n\x00") || !strings.HasPrefix(text, "pong from ") {
		return pingResult{}, bad
	}
	viaIndex := strings.LastIndex(text, " via ")
	if viaIndex < 0 {
		return pingResult{}, bad
	}
	prefix := text[:viaIndex]
	ipIndex := strings.LastIndex(prefix, " (")
	if ipIndex < 10 || !strings.HasSuffix(prefix, ")") {
		return pingResult{}, bad
	}
	address := strings.Split(prefix[ipIndex+2:len(prefix)-1], ", ")
	if len(address) > 2 {
		return pingResult{}, bad
	}
	ip, err := netip.ParseAddr(address[0])
	if err != nil || ip.Unmap() != target.Unmap() {
		return pingResult{}, bad
	}
	if len(address) == 2 {
		port, err := strconv.ParseUint(address[1], 10, 16)
		if err != nil || port == 0 {
			return pingResult{}, bad
		}
	}
	tail := text[viaIndex+5:]
	timeIndex := strings.LastIndex(tail, " in ")
	if timeIndex < 0 {
		return pingResult{}, bad
	}
	latency, err := time.ParseDuration(tail[timeIndex+4:])
	if err != nil || latency < 0 || latency > time.Minute {
		return pingResult{}, bad
	}
	via := tail[:timeIndex]
	route := ""
	if protocol == "tsmp" && strings.EqualFold(via, "tsmp") {
		route = "unknown"
	}
	if protocol == "disco" {
		if endpoint, err := netip.ParseAddrPort(via); err == nil && endpoint.Port() > 0 && !endpoint.Addr().IsUnspecified() && !endpoint.Addr().IsMulticast() {
			route = "direct"
		}
		if strings.HasPrefix(via, "DERP(") && strings.HasSuffix(via, ")") {
			region := strings.TrimSuffix(strings.TrimPrefix(via, "DERP("), ")")
			if len(region) > 0 && len(region) <= 32 && strings.Trim(region, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") == "" {
				route = "derp"
			}
		}
		if strings.HasPrefix(via, "peer-relay(") && strings.HasSuffix(via, ")") {
			relay := strings.TrimSuffix(strings.TrimPrefix(via, "peer-relay("), ")")
			split := strings.LastIndex(relay, ":vni:")
			if split > 0 {
				endpoint, err1 := netip.ParseAddrPort(relay[:split])
				_, err2 := strconv.ParseUint(relay[split+5:], 10, 32)
				if err1 == nil && err2 == nil && endpoint.Port() > 0 && !endpoint.Addr().IsUnspecified() && !endpoint.Addr().IsMulticast() {
					route = "peer_relay"
				}
			}
		}
	}
	if route == "" {
		return pingResult{}, bad
	}
	return pingResult{route: route, latency: latency}, nil
}
