package app

import (
	"errors"
	"flag"
	"fmt"

	"github.com/WhitenWhiten/topophany/internal/logging"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type logFlags struct {
	path    *string
	bytes   *int64
	backups *int
}

func addLogFlags(f *flag.FlagSet) logFlags {
	return logFlags{f.String("log-file", "", "managed rotating log; empty uses stderr"), f.Int64("log-max-bytes", 5<<20, "managed log size limit (64 KiB..1 GiB)"), f.Int("log-backups", 3, "managed log backups (1..10)")}
}
func (a *App) serviceLog(f logFlags) (func(error), error) {
	if *f.path == "" {
		return func(error) {}, nil
	}
	if *f.bytes < 64<<10 || *f.bytes > 1<<30 {
		return nil, errors.New("log-max-bytes must be 64 KiB..1 GiB")
	}
	out, e := logging.Open(*f.path, *f.bytes, *f.backups)
	if e != nil {
		return nil, e
	}
	previous := a.Err
	a.Err = secure.Terminal(out)
	return func(err error) {
		if err != nil {
			fmt.Fprintf(a.Err, "service stopped: %v\n", err)
		}
		out.Close()
		a.Err = previous
	}, nil
}
