// export_test.go exposes internal functions for black-box testing from the
// repl_test package. This file is only compiled during tests.
package repl

import (
	"io"

	"github.com/GabrielNunesIT/netconf/netconf/client"
	"github.com/chzyer/readline"
)

// ExportedHandleConnect wraps the unexported handleConnect for testing.
func ExportedHandleConnect(args []string, rl *readline.Instance, sess *Session, outW, errW io.Writer) error {
	return handleConnect(args, rl, sess, outW, errW)
}

// ExportedHandleDisconnect wraps the unexported handleDisconnect for testing.
func ExportedHandleDisconnect(sess *Session, outW, errW io.Writer) error {
	return handleDisconnect(sess, outW, errW)
}

// NewSessionWithClient creates a Session pre-populated with a client and host,
// for tests that need to simulate an already-connected state.
func NewSessionWithClient(cli *client.Client, host string) *Session {
	return &Session{cli: cli, host: host}
}
