// Command aja applies to LinkedIn Easy Apply jobs from the user's own machine.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/luizfreitas27/aja/internal/cli"
)

// Set by goreleaser through -ldflags at release time.
var (
	version = "dev"
	commit  = ""
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	info, _ := debug.ReadBuildInfo()
	app := cli.NewApp(cli.ResolveVersion(version, info))
	app.Commit = commit
	os.Exit(app.Execute(ctx, os.Args[1:]))
}
