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
	e := app.New().Run(ctx, os.Args[1:])
	var exit *app.ExitError
	switch {
	case e == nil, errors.Is(e, flag.ErrHelp), errors.Is(e, context.Canceled):
	case errors.As(e, &exit):
		// A status that is not an error, such as topo status finding
		// something to attend to, ends quietly.
		if exit.Err != nil {
			fmt.Fprintln(os.Stderr, "topo:", exit.Err)
		}
		os.Exit(exit.Code)
	default:
		fmt.Fprintln(os.Stderr, "topo:", e)
		os.Exit(1)
	}
}
