// Command medic runs health checks for pipelines.
package main

import (
	"os"
	"runtime/debug"

	"github.com/Rogsiel/medic/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = ""

func main() {
	if version == "" {
		version = "dev"
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
	}
	os.Exit(cli.Execute(version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
