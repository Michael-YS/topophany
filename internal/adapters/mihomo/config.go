package mihomo

import (
	"errors"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func Validate(options model.MihomoOptions) error {
	if len(options.ExpectedProviders) > 16 {
		return errors.New("maximum 16 expected providers per check")
	}
	for k := range options.ExpectedProviders {
		if len(k) > 119 || secure.Text(k) != k {
			return errors.New("provider name exceeds evidence budget or contains control characters")
		}
	}
	return nil
}
