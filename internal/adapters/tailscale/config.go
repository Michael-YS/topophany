package tailscale

import (
	"errors"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func Validate(options model.TailscaleOptions) error {
	if options.Peer == "" {
		return errors.New("tailscale check needs adapter.peer")
	}
	if options.Diagnostic != "" && options.Diagnostic != "disco" && options.Diagnostic != "tsmp" {
		return errors.New("tailscale_diagnostic must be disco or tsmp")
	}
	return nil
}
