// Command aja applies to LinkedIn Easy Apply jobs from the user's own machine.
//
// This file is the placeholder entry point created with the repository; the real
// command tree arrives with the first ticket of the spec (issue #1).
package main

import (
	"fmt"
	"os"
)

// version is set by goreleaser through -ldflags at release time.
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("aja", version)
		return
	}
	fmt.Fprintln(os.Stderr, "aja: not implemented yet; see https://github.com/luizfreitas27/aja/issues/1")
	os.Exit(2)
}
