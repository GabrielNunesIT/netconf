// Package transport provides the framing layer and transport interface for
// the NETCONF protocol.
//
// # Transport interface
//
// [Transport] is the core interface all transports implement. It exposes
// [Transport.MsgReader] (one message in) and [Transport.MsgWriter] (one message
// out), abstracting away the underlying connection and framing. The optional
// [Upgrader] interface allows the [Session] layer to switch framing mode after
// the hello exchange.
//
// Concrete transport implementations live in sub-packages:
//
//   - [github.com/GabrielNunesIT/netconf/transport/ssh] — SSH transport (RFC 6242).
//   - [github.com/GabrielNunesIT/netconf/transport/tls] — TLS transport (RFC 7589).
//
// The [NewLoopback] function creates matched in-process transport pairs for
// testing.
//
// # Framing
//
// [Framer] handles the two RFC 6242 framing modes transparently:
//
//   - EOM framing (base:1.0): each message is terminated by "]]>]]>". Used
//     during and before the hello exchange.
//   - Chunked framing (base:1.1, RFC 6242 §4.2): messages are encoded as one
//     or more "\n#<size>\n<data>" chunks, terminated by "\n##\n". Used after
//     both peers advertise base:1.1 in their hello messages.
//
// All transport implementations in this module use [Framer] internally.
// Callers interact only with [Transport.MsgReader] and [Transport.MsgWriter]
// and never see framing bytes.
//
// # Helpers
//
// [ReadMsg] and [WriteMsg] are convenience functions for reading or writing a
// complete message in one call. They are used by tests and the session layer;
// application code should normally use the higher-level session API instead.
package transport
