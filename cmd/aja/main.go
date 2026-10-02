// Command aja applies to LinkedIn Easy Apply jobs from the user's own machine.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/luizfreitas27/aja/internal/cli"
)

// version is set by goreleaser through -ldflags at release time.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.NewApp(version).Execute(ctx, os.Args[1:]))
}
