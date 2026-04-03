// Package netconf implements the NETCONF network configuration protocol
// (RFC 6241) and its key extensions.
//
// # Overview
//
// NETCONF is a standards-based protocol for network device configuration and
// state retrieval. It uses XML-encoded RPCs over a secured transport (SSH per
// RFC 6242, or TLS per RFC 7589) and a capability-negotiated hello exchange
// that determines the operation set and framing mode.
//
// This package provides the core types: capability negotiation, session
// establishment, XML wire messages, RFC 6241 operation structs, RFC 5277
// notifications, and RFC 6470 notification body types.
//
// # Quick start
//
// Connect as a client and retrieve the running configuration:
//
//	import (
//	    "context"
//	    "os"
//	    "path/filepath"
//	    "golang.org/x/crypto/ssh"
//	    "golang.org/x/crypto/ssh/knownhosts"
//	    netconf "github.com/GabrielNunesIT/netconf"
//	    "github.com/GabrielNunesIT/netconf/client"
//	)
//
//	home, err := os.UserHomeDir()
//	if err != nil { /* handle */ }
//	hostKeyCallback, err := knownhosts.New(filepath.Join(home, ".ssh", "known_hosts"))
//	if err != nil { /* handle */ }
//
//	cfg := &ssh.ClientConfig{
//	    User: "admin",
//	    Auth: []ssh.AuthMethod{ssh.Password("secret")},
//	    HostKeyCallback: hostKeyCallback,
//	}
//	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})
//
//	cli, err := client.Dial(context.Background(), "192.0.2.1:830", cfg, caps)
//	if err != nil { /* handle */ }
//	defer cli.Close()
//
//	dr, err := cli.GetConfig(context.Background(), netconf.Datastore{Running: &struct{}{}}, nil)
//	if err != nil { /* handle */ }
//	// dr.Content holds the raw inner XML of the <data> element.
//
// # Package layout
//
// The library is split into focused packages:
//
//   - [github.com/GabrielNunesIT/netconf] (this package) — core types: Session,
//     hello exchange, capabilities, wire messages (Hello, RPC, RPCReply,
//     Notification), RFC 6241 operation structs, RPCError, and RFC 6470
//     notification body types.
//
//   - [github.com/GabrielNunesIT/netconf/client] — high-level client API.
//     [client.Client] multiplexes concurrent RPCs over a single session, delivers
//     notifications to a channel, and provides typed methods for all RFC 6241
//     operations (Get, GetConfig, EditConfig, …).
//
//   - [github.com/GabrielNunesIT/netconf/server] — server-side RPC dispatch.
//     [server.Server] routes incoming RPCs to registered [server.Handler]
//     implementations by operation name. Handlers may implement
//     [server.StreamHandler] for zero-copy body decoding.
//
//   - [github.com/GabrielNunesIT/netconf/transport] — framing layer.
//     [transport.Framer] handles EOM (base:1.0) and chunked (base:1.1) framing
//     transparently. [transport.Transport] and [transport.Upgrader] are the
//     interfaces all transports implement.
//
//   - [github.com/GabrielNunesIT/netconf/transport/ssh] — SSH transport (RFC 6242).
//
//   - [github.com/GabrielNunesIT/netconf/transport/tls] — TLS transport (RFC 7589),
//     including mutual X.509 authentication and cert-to-name username derivation.
//
//   - [github.com/GabrielNunesIT/netconf/monitoring] — ietf-netconf-monitoring
//     (RFC 6022): session/schema/datastore state structs and get-schema RPC.
//
//   - [github.com/GabrielNunesIT/netconf/nacm] — NETCONF Access Control Model
//     (RFC 8341): data model structs and enforcement function.
//
//   - [github.com/GabrielNunesIT/netconf/nmda] — NMDA operations (RFC 8526):
//     get-data, edit-data, delete-data, copy-data.
//
//   - [github.com/GabrielNunesIT/netconf/subscriptions] — RFC 8639 dynamic
//     subscriptions and RFC 8640 NETCONF transport bindings.
//
//   - [github.com/GabrielNunesIT/netconf/yangpush] — YANG-push (RFC 8641):
//     periodic and on-change datastore subscriptions.
//
//   - [github.com/GabrielNunesIT/netconf/yanglibrary] — ietf-yang-library
//     (RFC 8525): YANG module discovery structs.
//
//   - [github.com/GabrielNunesIT/netconf/cmd/netconf] — interactive REPL CLI for
//     developer use and library validation.
//
// # Session lifecycle
//
// [ClientSession] and [ServerSession] perform the RFC 6241 §8.1 hello exchange:
// both peers send their capabilities simultaneously (required to avoid deadlock
// on unbuffered transports), then negotiate framing. If both advertise base:1.1,
// the transport is upgraded to chunked framing ([FramingChunked]); otherwise it
// stays in EOM framing ([FramingEOM]).
//
// # Capabilities
//
// [CapabilitySet] is an ordered list of capability URNs. [NewCapabilitySet]
// constructs one from a string slice. [ValidateURN] checks RFC 7803 format.
// The predefined constants (BaseCap10, BaseCap11, CapabilityCandidate, …)
// cover all standard RFC 6241 and related extension capabilities.
//
// # Operations
//
// Each RFC 6241 §7 operation is a Go struct that xml.Marshal encodes correctly.
// Pass one to [client.Client.Do] or use a typed convenience method:
//
//	// Typed method:
//	dr, err := cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
//
//	// Raw RPC (any marshalable op):
//	reply, err := cli.Do(ctx, &netconf.EditConfig{
//	    Target: netconf.Datastore{Running: &struct{}{}},
//	    Config: []byte(`<config>…</config>`),
//	})
//
// # Error handling
//
// [RPCError] is the structured NETCONF error type (RFC 6241 §4.3). It implements
// the error interface and is returned by typed client methods when the server
// replies with an <rpc-error> element. Use errors.As to extract it:
//
//	dr, err := cli.GetConfig(ctx, source, nil)
//	var rpcErr netconf.RPCError
//	if errors.As(err, &rpcErr) {
//	    log.Printf("server error: tag=%s message=%s", rpcErr.Tag, rpcErr.Message)
//	}
//
// # Notifications (RFC 5277)
//
// Subscribe and receive notifications:
//
//	notifCh, err := cli.Subscribe(ctx, netconf.CreateSubscription{})
//	if err != nil { /* handle */ }
//	for n := range notifCh {
//	    // n.EventTime is the xs:dateTime timestamp.
//	    // n.Body is the raw inner XML of the notification content.
//	}
package netconf
