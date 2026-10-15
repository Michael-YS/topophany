// topo is the single Topophany binary: remote TUI by default, collector and
// executor roles via subcommands. No external Go modules are required.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/WhitenWhiten/topophany/internal/app"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	e := app.New().Run(ctx, os.Args[1:])
	// An error can name what topo read, from a file or a peer: it reaches
	// the terminal without controls.
	stderr := secure.Terminal(os.Stderr)
	var exit *app.ExitError
	switch {
	case e == nil, errors.Is(e, flag.ErrHelp), errors.Is(e, context.Canceled):
	case errors.As(e, &exit):
		// A status that is not an error, such as topo status finding
		// something to attend to, ends quietly.
		if exit.Err != nil {
			fmt.Fprintln(stderr, "topo:", exit.Err)
		}
		os.Exit(exit.Code)
	default:
		fmt.Fprintln(stderr, "topo:", e)
		os.Exit(1)
	}
}
