// Package repl implements the interactive REPL loop for the netconf CLI tool.
package repl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
)

// errExit is a sentinel returned by dispatch() to signal the REPL should exit.
var errExit = errors.New("exit")

func filterInput(r rune) (rune, bool) {
	// Block Ctrl+Z to prevent accidental suspension in REPL context.
	if r == readline.CharCtrlZ {
		return r, false
	}
	return r, true
}

// Run starts the interactive REPL. It returns only on fatal initialisation
// errors; normal exit (exit/quit/Ctrl+D) returns nil.
func Run(version string) error {
	histFile := ""
	if home, err := os.UserHomeDir(); err == nil {
		histFile = filepath.Join(home, ".netconf_history")
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:              "netconf> ",
		HistoryFile:         histFile,
		HistoryLimit:        1000,
		HistorySearchFold:   true,
		AutoComplete:        Completer,
		InterruptPrompt:     "^C",
		EOFPrompt:           "exit",
		FuncFilterInputRune: filterInput,
	})
	if err != nil {
		return fmt.Errorf("readline init: %w", err)
	}
	defer func() {
		if closeErr := rl.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "readline close: %v\n", closeErr)
		}
	}()

	// Route all log/fmt output through readline's stderr so it doesn't corrupt
	// the prompt during async writes.
	errW := rl.Stderr()
	outW := rl.Stdout()

	// Session state — populated by connect/disconnect.
	sess := &Session{}

	for {
		// Dynamic prompt: shows host when connected, with lock indicator.
		rl.SetPrompt(buildPrompt(sess))

		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				// Ctrl+C on empty line → exit.
				break
			}
			// Ctrl+C with partial input → clear and continue.
			continue
		} else if err == io.EOF {
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if dispatchErr := dispatch(rl, line, version, sess, outW, errW); dispatchErr == errExit {
			break
		} else if dispatchErr != nil {
			fmt.Fprintf(errW, "error: %v\n", dispatchErr)
		}
	}

	// If a session is active, close it gracefully on exit.
	if sess.Connected() {
		if closeErr := sess.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: session close: %v\n", closeErr)
		}
	}

	return nil
}

// dispatch parses a raw input line and calls the appropriate command handler.
// rl is passed for commands that need masked input (password prompt).
// Returns errExit to signal the loop should terminate, nil to continue,
// or a non-fatal error to print and continue.
func dispatch(rl *readline.Instance, line, version string, sess *Session, outW, errW io.Writer) error {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}
	cmd, args := parts[0], parts[1:]

	switch cmd {
	case "exit", "quit":
		return errExit

	case "help", "?":
		printHelp(outW)
		return nil

	case "version":
		fmt.Fprintf(outW, "netconf %s\n", version)
		return nil

	case "connect":
		return handleConnect(args, rl, sess, outW, errW)

	case "disconnect", "close-session":
		return handleDisconnect(sess, outW, errW)

	case "get", "get-config", "edit-config", "copy-config", "delete-config",
		"lock", "unlock", "commit", "discard", "validate", "kill-session", "capabilities":
		return handleOperation(cmd, args, sess, outW, errW)

	default:
		fmt.Fprintf(errW, "unknown command: %q (type 'help' for usage)\n", cmd)
		return nil
	}
}

// handleOperation delegates to the full operation dispatcher in operations.go.
func handleOperation(cmd string, args []string, sess *Session, outW, errW io.Writer) error {
	return dispatchOperation(cmd, args, sess, outW, errW)
}

// buildPrompt returns the REPL prompt string for the current session state.
//   - Not connected:           "netconf> "
//   - Connected, no locks:     "netconf@host> "
//   - Connected, candidate locked: "netconf@host[locked]> "
func buildPrompt(sess *Session) string {
	if !sess.Connected() {
		return "netconf> "
	}
	if sess.IsLocked("candidate") {
		return fmt.Sprintf("netconf@%s[locked]> ", sess.Host())
	}
	return fmt.Sprintf("netconf@%s> ", sess.Host())
}
