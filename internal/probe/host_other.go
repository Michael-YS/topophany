//go:build !linux && !darwin

package probe

import (
	"context"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func (r Runner) sampleHost(context.Context, model.HostOptions) (HostSample, error) {
	return HostSample{}, local("host", "host_platform_unsupported")
}
