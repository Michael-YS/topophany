package client

import (
	"context"
	"time"
)

// Divide a caller's remaining read budget among the endpoints still eligible
// for failover. This also bounds response bodies, not only header arrival.
// Without a caller deadline the transport retains its normal timeout.
func endpointContext(ctx context.Context, remaining int) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && remaining > 1 {
		return context.WithTimeout(ctx, time.Until(deadline)/time.Duration(remaining))
	}
	return context.WithCancel(ctx)
}
