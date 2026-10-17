package wireguard

import (
	"errors"
	"regexp"

	"github.com/WhitenWhiten/topophany/internal/model"
)

var interfaceName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

func Validate(options model.WireGuardOptions) error {
	if options.Peer == "" || !interfaceName.MatchString(options.Interface) {
		return errors.New("wireguard needs interface and peer public key")
	}
	return nil
}
