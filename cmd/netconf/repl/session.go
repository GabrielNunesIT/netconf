package repl

import (
	"context"

	"github.com/GabrielNunesIT/netconf/netconf/client"
)

// Session holds live NETCONF connection state for the REPL.
// It is safe to call any method on a zero-value Session.
type Session struct {
	host   string         // display string used in the prompt (e.g. "192.0.2.1:830")
	cli    *client.Client // nil when not connected
	locked map[string]bool // tracks locked datastores ("running", "candidate", "startup")
}

// Connected reports whether a NETCONF session is currently active.
func (s *Session) Connected() bool { return s.cli != nil }

// Host returns the remote host:port string used in the prompt.
// Returns an empty string when not connected.
func (s *Session) Host() string { return s.host }

// Client returns the underlying *client.Client, or nil when not connected.
func (s *Session) Client() *client.Client { return s.cli }

// SetLocked records or clears a lock on a named datastore.
// locked=true marks ds as locked; locked=false clears it.
func (s *Session) SetLocked(ds string, locked bool) {
	if s.locked == nil {
		s.locked = make(map[string]bool)
	}
	if locked {
		s.locked[ds] = true
	} else {
		delete(s.locked, ds)
	}
}

// IsLocked reports whether the named datastore is currently locked by this session.
func (s *Session) IsLocked(ds string) bool {
	return s.locked[ds]
}

// Close sends a NETCONF close-session RPC and releases the transport.
// Safe to call on an unconnected Session (no-op).
// Always clears the session state even if the RPC fails.
func (s *Session) Close() error {
	if s.cli == nil {
		return nil
	}
	err := s.cli.CloseSession(context.Background())
	// Always close the transport regardless of close-session result.
	_ = s.cli.Close()
	s.cli = nil
	s.host = ""
	s.locked = nil
	return err
}
