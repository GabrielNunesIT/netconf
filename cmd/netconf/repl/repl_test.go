package repl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf/client"
	"github.com/stretchr/testify/assert"
)

// TestDispatch_Exit verifies that 'exit' and 'quit' return errExit.
func TestDispatch_Exit(t *testing.T) {
	t.Parallel()
	sess := &Session{}
	var out, errOut bytes.Buffer

	for _, cmd := range []string{"exit", "quit"} {
		err := dispatch(nil, cmd, "dev", sess, &out, &errOut)
		assert.Equal(t, errExit, err, "dispatch(%q) must return errExit", cmd)
	}
}

// TestDispatch_Unknown verifies that an unrecognised command writes an error
// message and returns nil (loop continues).
func TestDispatch_Unknown(t *testing.T) {
	t.Parallel()
	sess := &Session{}
	var out, errOut bytes.Buffer

	err := dispatch(nil, "frobnicate", "dev", sess, &out, &errOut)
	assert.NoError(t, err, "unknown command must not return an error (just print)")
	assert.Contains(t, errOut.String(), "unknown command", "stderr must mention 'unknown command'")
	assert.Contains(t, errOut.String(), "frobnicate", "stderr must echo the bad command name")
}

// TestDispatch_Version verifies that 'version' prints the version string to stdout.
func TestDispatch_Version(t *testing.T) {
	t.Parallel()
	sess := &Session{}
	var out, errOut bytes.Buffer

	err := dispatch(nil, "version", "1.2.3", sess, &out, &errOut)
	assert.NoError(t, err)
	assert.Contains(t, out.String(), "1.2.3", "stdout must contain the version string")
	assert.Empty(t, errOut.String(), "stderr must be empty for version command")
}

// TestDispatch_Help verifies that 'help' writes to stdout and returns nil.
func TestDispatch_Help(t *testing.T) {
	t.Parallel()
	sess := &Session{}
	var out, errOut bytes.Buffer

	err := dispatch(nil, "help", "dev", sess, &out, &errOut)
	assert.NoError(t, err)
	assert.NotEmpty(t, out.String(), "help must write output to stdout")
	// Basic sanity: key command names appear in the help text.
	assert.Contains(t, out.String(), "connect")
	assert.Contains(t, out.String(), "get-config")
	assert.Contains(t, out.String(), "exit")
}

// TestDispatch_OperationNotConnected verifies that operation commands print
// a 'not connected' message and return nil when no session is active.
func TestDispatch_OperationNotConnected(t *testing.T) {
	t.Parallel()
	sess := &Session{} // not connected
	var out, errOut bytes.Buffer

	for _, cmd := range []string{"get", "get-config", "edit-config", "lock", "unlock", "commit", "capabilities"} {
		errOut.Reset()
		err := dispatch(nil, cmd, "dev", sess, &out, &errOut)
		assert.NoError(t, err, "operation without connection must not error")
		assert.Contains(t, errOut.String(), "not connected", "must print 'not connected' for %q", cmd)
	}
}

// TestSession_Connected verifies Session.Connected() reflects client state.
func TestSession_Connected(t *testing.T) {
	t.Parallel()
	s := &Session{}
	assert.False(t, s.Connected(), "new session must not be connected")
	// Session.Connected() depends on the cli field — tested via connect/disconnect
	// integration tests in connect_test.go (S02).
}

// TestCompleter_Shape verifies the full tab-completion tree covers all expected
// top-level command names.
func TestCompleter_Shape(t *testing.T) {
	t.Parallel()
	expected := []string{
		"connect", "disconnect",
		"get", "get-config", "edit-config", "copy-config", "delete-config",
		"lock", "unlock", "commit", "discard", "validate", "kill-session",
		"capabilities", "version", "help", "exit", "quit",
	}

	children := Completer.GetChildren()
	names := make([]string, 0, len(children))
	for _, child := range children {
		// readline.PrefixCompleter.GetName() includes a trailing space; trim it.
		names = append(names, strings.TrimRight(string(child.GetName()), " "))
	}

	for _, want := range expected {
		assert.Contains(t, names, want, "Completer must contain top-level command %q", want)
	}
}

// TestSession_LockState verifies SetLocked/IsLocked round-trip and that
// Connected() is unaffected by lock state.
func TestSession_LockState(t *testing.T) {
	t.Parallel()
	s := &Session{}
	assert.False(t, s.IsLocked("candidate"), "new session must not have candidate locked")
	assert.False(t, s.IsLocked("running"), "new session must not have running locked")

	s.SetLocked("candidate", true)
	assert.True(t, s.IsLocked("candidate"), "candidate must be locked after SetLocked(true)")
	assert.False(t, s.IsLocked("running"), "running must still be unlocked")

	s.SetLocked("candidate", false)
	assert.False(t, s.IsLocked("candidate"), "candidate must be unlocked after SetLocked(false)")

	// Lock state does not affect Connected().
	assert.False(t, s.Connected(), "lock state must not affect Connected()")
}

// TestBuildPrompt_States verifies the prompt string for all three session states.
// White-box test — accesses unexported Session fields directly.
func TestBuildPrompt_States(t *testing.T) {
	t.Parallel()
	// Not connected → plain prompt.
	s := &Session{}
	assert.Equal(t, "netconf> ", buildPrompt(s))

	// Connected, no lock. new(client.Client) is a non-nil *client.Client;
	// Connected() only checks for nil, so this is sufficient.
	s.cli = new(client.Client)
	s.host = "192.0.2.1"
	assert.Equal(t, "netconf@192.0.2.1> ", buildPrompt(s))

	// Connected + candidate locked.
	s.SetLocked("candidate", true)
	assert.Equal(t, "netconf@192.0.2.1[locked]> ", buildPrompt(s))

	// Unlock candidate → plain connected prompt.
	s.SetLocked("candidate", false)
	assert.Equal(t, "netconf@192.0.2.1> ", buildPrompt(s))
}
