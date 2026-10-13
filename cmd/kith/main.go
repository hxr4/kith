// Command kith is the single Kith binary: daemon, CLI and MCP server.
package main

import (
	"os"

	"kith/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
