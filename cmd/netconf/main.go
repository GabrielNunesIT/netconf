// Command netconf is an interactive REPL for exploring NETCONF devices.
//
// Usage:
//
//	netconf [--version]
//
// Once started, use the 'connect' command to establish a NETCONF session:
//
//	netconf> connect --host 192.0.2.1 --port 830 --user admin
//
// Type 'help' for a full command listing.
package main

import (
	"fmt"
	"os"

	"github.com/GabrielNunesIT/netconf/cmd/netconf/repl"
)

// version is overridden at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	// Handle --version as the only top-level flag (before entering the REPL).
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version" || os.Args[1] == "version") {
		fmt.Printf("netconf %s\n", version)
		os.Exit(0)
	}
	if len(os.Args) > 1 {
		fmt.Fprintf(os.Stderr, "usage: netconf [--version]\n")
		fmt.Fprintf(os.Stderr, "Start the REPL, then type 'help' for commands.\n")
		os.Exit(1)
	}

	if err := repl.Run(version); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
