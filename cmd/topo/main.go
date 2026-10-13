// topo is the single Topophany binary: remote TUI by default, collector and
// executor roles via subcommands. No external Go modules are required.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/app"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := app.New().Run(ctx, os.Args[1:]); e != nil && !errors.Is(e, flag.ErrHelp) && !errors.Is(e, context.Canceled) {
		fmt.Fprintln(os.Stderr, "topo:", e)
		os.Exit(1)
	}
}
