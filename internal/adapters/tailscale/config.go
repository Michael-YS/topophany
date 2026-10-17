package tailscale

import (
	"errors"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func Validate(options model.TailscaleOptions) error {
	if options.Peer == "" {
		return errors.New("tailscale check needs adapter.peer")
	}
	return nil
}
