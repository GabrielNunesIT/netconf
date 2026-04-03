package repl

import (
	"fmt"
	"io"
)

// printHelp writes the command reference to w.
func printHelp(w io.Writer) {
	fmt.Fprintf(w, `
netconf REPL — interactive NETCONF client

CONNECTION
  connect --host <host> [--port <port>] --user <user>
          [--password <pw>] [--key <path>] [--known-hosts <path>] [--insecure]
                        Connect to a NETCONF device over SSH
  disconnect            Close the current session
  capabilities          Print negotiated capabilities

OPERATIONS  (require an active connection)
  get [--filter <xpath>] [--subtree <file>] [--raw]
                        Retrieve running config + state
  get-config [--source running|candidate|startup]
             [--filter <xpath>] [--subtree <file>] [--raw]
                        Retrieve configuration
  edit-config --target <ds> [--operation merge|replace|none]
              (--config <file> | --config -)
                        Modify configuration
  copy-config --source <ds> --target <ds>
                        Copy one datastore to another
  delete-config --target <ds>
                        Delete a configuration datastore
  lock   [--target running|candidate|startup]
                        Lock a datastore (default: running)
  unlock [--target running|candidate|startup]
                        Unlock a datastore (default: running)
  commit [--confirmed] [--timeout <seconds>]
                        Commit candidate configuration
  discard               Discard candidate changes
  validate [--source running|candidate|startup]
                        Validate a datastore
  kill-session --id <session-id>
                        Kill another NETCONF session

GENERAL
  version               Print the tool version
  help, ?               Print this help
  exit, quit            Close session and exit

FLAGS ON ALL OPERATIONS
  --raw                 Print raw XML without indentation

`)
}
