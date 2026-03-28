package repl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	netconf "github.com/GabrielNunesIT/netconf"
)

// dispatchOperation routes a REPL command to its handler.
// This is called by repl.go's dispatch() for all operation commands.
func dispatchOperation(cmd string, args []string, sess *Session, outW, errW io.Writer) error {
	if !sess.Connected() {
		fmt.Fprintf(errW, "%s: not connected (use 'connect' first)\n", cmd)
		return nil
	}

	switch cmd {
	case "get":
		return cmdGet(args, sess, outW, errW)
	case "get-config":
		return cmdGetConfig(args, sess, outW, errW)
	case "edit-config":
		return cmdEditConfig(args, sess, outW, errW)
	case "copy-config":
		return cmdCopyConfig(args, sess, outW, errW)
	case "delete-config":
		return cmdDeleteConfig(args, sess, outW, errW)
	case "lock":
		return cmdLock(args, sess, outW, errW)
	case "unlock":
		return cmdUnlock(args, sess, outW, errW)
	case "commit":
		return cmdCommit(args, sess, outW, errW)
	case "discard":
		return cmdDiscard(sess, outW, errW)
	case "validate":
		return cmdValidate(args, sess, outW, errW)
	case "kill-session":
		return cmdKillSession(args, sess, outW, errW)
	case "capabilities":
		return cmdCapabilities(sess, outW)
	default:
		fmt.Fprintf(errW, "unknown operation: %q\n", cmd)
		return nil
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parseDatastore converts a datastore name string to a netconf.Datastore.
// Accepts "running", "candidate", "startup". Defaults to running.
func parseDatastore(s string) netconf.Datastore {
	switch strings.ToLower(s) {
	case "candidate":
		return netconf.Datastore{Candidate: &struct{}{}}
	case "startup":
		return netconf.Datastore{Startup: &struct{}{}}
	default:
		return netconf.Datastore{Running: &struct{}{}}
	}
}

// readConfig reads XML configuration bytes from a file path or stdin.
// path=="-" reads from os.Stdin; otherwise reads the named file.
func readConfig(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// parseFilter builds a *netconf.Filter from an XPath expression or a subtree
// XML file. Returns nil when neither is specified.
func parseFilter(xpath, subtreeFile string) (*netconf.Filter, error) {
	if xpath != "" {
		return &netconf.Filter{Type: "xpath", Select: xpath}, nil
	}
	if subtreeFile != "" {
		content, err := os.ReadFile(subtreeFile)
		if err != nil {
			return nil, fmt.Errorf("read subtree file %q: %w", subtreeFile, err)
		}
		return &netconf.Filter{Type: "subtree", Content: content}, nil
	}
	return nil, nil //nolint:nilnil // (nil, nil) is the documented sentinel meaning "no filter"
}

// Returns true if it handled an RPCError, false otherwise.
func printRPCError(errW io.Writer, err error) bool {
	var rpcErr netconf.RPCError
	if errors.As(err, &rpcErr) {
		fmt.Fprintf(errW, "rpc-error: tag=%s message=%s\n", rpcErr.Tag, rpcErr.Message)
		return true
	}
	return false
}

// printErr writes a formatted error to errW, preferring RPC error formatting.
func printErr(errW io.Writer, cmd string, err error) {
	if !printRPCError(errW, err) {
		fmt.Fprintf(errW, "%s: %v\n", cmd, err)
	}
}

// ctx returns a background context with a 30-second timeout for REPL operations.
func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// ── get ───────────────────────────────────────────────────────────────────────

func cmdGet(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(errW)
	filterXPath := fs.String("filter", "", "XPath filter expression")
	subtreeFile := fs.String("subtree", "", "path to subtree filter XML file")
	raw := fs.Bool("raw", false, "print raw XML without indentation")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	filter, err := parseFilter(*filterXPath, *subtreeFile)
	if err != nil {
		fmt.Fprintf(errW, "get: %v\n", err)
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	dr, err := sess.Client().Get(c, filter)
	if err != nil {
		printErr(errW, "get", err)
		return nil
	}
	PrintXML(outW, dr.Content, *raw)
	return nil
}

// ── get-config ────────────────────────────────────────────────────────────────

func cmdGetConfig(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("get-config", flag.ContinueOnError)
	fs.SetOutput(errW)
	source := fs.String("source", "running", "datastore: running|candidate|startup")
	filterXPath := fs.String("filter", "", "XPath filter expression")
	subtreeFile := fs.String("subtree", "", "path to subtree filter XML file")
	raw := fs.Bool("raw", false, "print raw XML without indentation")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	filter, err := parseFilter(*filterXPath, *subtreeFile)
	if err != nil {
		fmt.Fprintf(errW, "get-config: %v\n", err)
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	dr, err := sess.Client().GetConfig(c, parseDatastore(*source), filter)
	if err != nil {
		printErr(errW, "get-config", err)
		return nil
	}
	PrintXML(outW, dr.Content, *raw)
	return nil
}

// ── edit-config ───────────────────────────────────────────────────────────────

func cmdEditConfig(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("edit-config", flag.ContinueOnError)
	fs.SetOutput(errW)
	target := fs.String("target", "running", "target datastore: running|candidate|startup")
	operation := fs.String("operation", "merge", "default operation: merge|replace|none")
	configPath := fs.String("config", "", "path to config XML file, or '-' for stdin (required)")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	if *configPath == "" {
		fmt.Fprintf(errW, "edit-config: --config is required\n")
		return nil
	}

	cfgBytes, err := readConfig(*configPath)
	if err != nil {
		fmt.Fprintf(errW, "edit-config: read config: %v\n", err)
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().EditConfig(c, netconf.EditConfig{
		Target:           parseDatastore(*target),
		DefaultOperation: *operation,
		Config:           cfgBytes,
	}); err != nil {
		printErr(errW, "edit-config", err)
		return nil
	}
	fmt.Fprintf(outW, "ok\n")
	return nil
}

// ── copy-config ───────────────────────────────────────────────────────────────

func cmdCopyConfig(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("copy-config", flag.ContinueOnError)
	fs.SetOutput(errW)
	source := fs.String("source", "", "source datastore: running|candidate|startup (required)")
	target := fs.String("target", "", "target datastore: running|candidate|startup (required)")
	if err := fs.Parse(args); err != nil {
		return nil
	}
	if *source == "" || *target == "" {
		fmt.Fprintf(errW, "copy-config: --source and --target are required\n")
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().CopyConfig(c, netconf.CopyConfig{
		Source: parseDatastore(*source),
		Target: parseDatastore(*target),
	}); err != nil {
		printErr(errW, "copy-config", err)
		return nil
	}
	fmt.Fprintf(outW, "ok\n")
	return nil
}

// ── delete-config ─────────────────────────────────────────────────────────────

func cmdDeleteConfig(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("delete-config", flag.ContinueOnError)
	fs.SetOutput(errW)
	target := fs.String("target", "", "target datastore: candidate|startup (required)")
	if err := fs.Parse(args); err != nil {
		return nil
	}
	if *target == "" {
		fmt.Fprintf(errW, "delete-config: --target is required\n")
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().DeleteConfig(c, netconf.DeleteConfig{
		Target: parseDatastore(*target),
	}); err != nil {
		printErr(errW, "delete-config", err)
		return nil
	}
	fmt.Fprintf(outW, "ok\n")
	return nil
}

// ── lock ──────────────────────────────────────────────────────────────────────

func cmdLock(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("lock", flag.ContinueOnError)
	fs.SetOutput(errW)
	target := fs.String("target", "running", "datastore to lock: running|candidate|startup")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().Lock(c, parseDatastore(*target)); err != nil {
		printErr(errW, "lock", err)
		return nil
	}
	sess.SetLocked(*target, true)
	fmt.Fprintf(outW, "locked %s\n", *target)
	return nil
}

// ── unlock ────────────────────────────────────────────────────────────────────

func cmdUnlock(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("unlock", flag.ContinueOnError)
	fs.SetOutput(errW)
	target := fs.String("target", "running", "datastore to unlock: running|candidate|startup")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().Unlock(c, parseDatastore(*target)); err != nil {
		printErr(errW, "unlock", err)
		return nil
	}
	sess.SetLocked(*target, false)
	fmt.Fprintf(outW, "unlocked %s\n", *target)
	return nil
}

// ── commit ────────────────────────────────────────────────────────────────────

func cmdCommit(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(errW)
	confirmed := fs.Bool("confirmed", false, "initiate a confirmed commit")
	timeout := fs.Int("timeout", 0, "confirmed-commit timeout in seconds (default: 600)")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	var opts *netconf.Commit
	if *confirmed || *timeout > 0 {
		opts = &netconf.Commit{}
		if *confirmed {
			opts.Confirmed = &struct{}{}
		}
		if *timeout > 0 {
			opts.ConfirmTimeout = uint32(*timeout)
		}
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().Commit(c, opts); err != nil {
		printErr(errW, "commit", err)
		return nil
	}
	fmt.Fprintf(outW, "committed\n")
	return nil
}

// ── discard ───────────────────────────────────────────────────────────────────

func cmdDiscard(sess *Session, outW, errW io.Writer) error {
	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().DiscardChanges(c); err != nil {
		printErr(errW, "discard", err)
		return nil
	}
	fmt.Fprintf(outW, "discarded\n")
	return nil
}

// ── validate ──────────────────────────────────────────────────────────────────

func cmdValidate(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(errW)
	source := fs.String("source", "running", "datastore to validate: running|candidate|startup")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().Validate(c, parseDatastore(*source)); err != nil {
		printErr(errW, "validate", err)
		return nil
	}
	fmt.Fprintf(outW, "valid\n")
	return nil
}

// ── kill-session ──────────────────────────────────────────────────────────────

func cmdKillSession(args []string, sess *Session, outW, errW io.Writer) error {
	fs := flag.NewFlagSet("kill-session", flag.ContinueOnError)
	fs.SetOutput(errW)
	idStr := fs.String("id", "", "session-id to kill (required)")
	if err := fs.Parse(args); err != nil {
		return nil
	}
	if *idStr == "" {
		fmt.Fprintf(errW, "kill-session: --id is required\n")
		return nil
	}
	id, err := strconv.ParseUint(*idStr, 10, 32)
	if err != nil {
		fmt.Fprintf(errW, "kill-session: --id must be a positive integer: %v\n", err)
		return nil
	}

	c, cancel := ctx()
	defer cancel()

	if err := sess.Client().KillSession(c, uint32(id)); err != nil {
		printErr(errW, "kill-session", err)
		return nil
	}
	fmt.Fprintf(outW, "killed session %d\n", id)
	return nil
}

// ── capabilities ──────────────────────────────────────────────────────────────

func cmdCapabilities(sess *Session, outW io.Writer) error {
	caps := sess.Client().RemoteCapabilities()
	for _, cap := range caps {
		fmt.Fprintf(outW, "%s\n", cap)
	}
	return nil
}
