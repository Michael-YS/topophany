package wireguard

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

var interfaceName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

func Validate(options model.WireGuardOptions) error {
	if options.Peer == "" || !interfaceName.MatchString(options.Interface) || strings.HasPrefix(options.Interface, "-") || options.Interface == "all" || options.Interface == "interfaces" {
		return errors.New("wireguard needs a single interface and peer public key")
	}
	if options.ExpectedAllowedIPs != nil {
		if len(options.ExpectedAllowedIPs) == 0 || len(options.ExpectedAllowedIPs) > 128 {
			return errors.New("expected_allowed_ips needs 1..128 CIDRs")
		}
		if _, err := prefixSet(options.ExpectedAllowedIPs); err != nil {
			return err
		}
	}
	if value := options.ExpectedKeepaliveSeconds; value != nil && (*value < 0 || *value > 65535) {
		return errors.New("expected_keepalive_seconds must be 0..65535")
	}
	return nil
}
func prefixSet(values []string) (map[netip.Prefix]bool, error) {
	out := map[netip.Prefix]bool{}
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, errors.New("expected_allowed_ips contains invalid CIDR")
		}
		prefix = prefix.Masked()
		if out[prefix] {
			return nil, errors.New("duplicate AllowedIPs prefix")
		}
		out[prefix] = true
	}
	return out, nil
}
