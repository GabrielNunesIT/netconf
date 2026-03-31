// Package monitoring implements the ietf-netconf-monitoring YANG module
// (RFC 6022). It provides Go struct types for all containers in the
// netconf-state subtree, plus the GetSchema operation types.
//
// # Namespace
//
// All types in this package use the YANG module namespace
// "urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring" (MonitoringNS).
// This is a YANG module namespace, NOT a NETCONF capability URN of the form
// "urn:ietf:params:netconf:capability:…", so it must NOT be passed to
// netconf.ValidateURN. Use CapabilityURI to announce the capability in a
// NETCONF hello exchange.
//
// # GetSchema
//
// RFC 6022 §3 defines the get-schema RPC. Build a GetSchemaRequest and pass
// it to client.Client.GetSchema to retrieve the YANG or XSD schema document
// for a given identifier. The raw schema text is returned as []byte.
//
// # Observability Impact
//
// Types in this package are pure encoding/decoding structs; there is no
// runtime process or daemon. Failure visibility is through XML marshal/unmarshal
// errors and go test output.
//
//   - go test ./... -v — per-struct round-trip pass/fail
//     with actual marshaled XML printed in failure messages via t.Logf.
//   - go test ./... -run TestClient_GetSchema -v — tests the
//     GetSchema typed method end-to-end.
//   - A missing xmlns attribute in marshal output causes assert.Contains
//     failures with the actual XML printed. An unmarshal field mismatch shows
//     assert.Equal diffs with expected vs actual values.
//   - The MonitoringNS constant value is testable via:
//     assert.Equal(t, monitoring.MonitoringNS, "urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring")
package monitoring

import "encoding/xml"

// MonitoringNS is the XML namespace for the ietf-netconf-monitoring YANG
// module (RFC 6022). All elements in this package are qualified with this
// namespace.
const MonitoringNS = "urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring"

// CapabilityURI is the URI a server includes in its <hello> capabilities list
// to advertise support for the ietf-netconf-monitoring YANG module (RFC 6022).
//
// Note: this is a YANG module namespace URI, not a
// "urn:ietf:params:netconf:capability:…" URN. Do not pass it to
// netconf.ValidateURN.
const CapabilityURI = "urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring"

// NetconfState is the top-level container element returned by a server that
// supports the ietf-netconf-monitoring YANG module (RFC 6022 §3.1).
//
// It aggregates the five monitoring sub-trees:
//   - Capabilities: list of capability URIs the server currently supports.
//   - Datastores:   per-datastore information including lock state.
//   - Schemas:      schemas available for retrieval via get-schema.
//   - Sessions:     currently active NETCONF sessions.
//   - Statistics:   aggregate session and message counters.
type NetconfState struct {
	XMLName      xml.Name    `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring netconf-state"`
	Capabilities []string    `xml:"capabilities>capability"`
	Datastores   []Datastore `xml:"datastores>datastore"`
	Schemas      []Schema    `xml:"schemas>schema"`
	Sessions     []Session   `xml:"sessions>session"`
	Statistics   *Statistics `xml:"statistics"`
}

// Datastore describes a single NETCONF configuration datastore (RFC 6022 §3.1.2).
//
//   - Name:  the datastore identifier (e.g. "running", "candidate", "startup").
//   - Locks: current lock state; nil when there are no active locks.
type Datastore struct {
	Name  string    `xml:"name"`
	Locks *LockInfo `xml:"locks"`
}

// LockInfo holds the lock state for a datastore (RFC 6022 §3.1.2.1).
// A datastore may have at most one global lock and zero or more partial locks
// active simultaneously.
//
//   - GlobalLock:  the active global lock (nil if none).
//   - PartialLock: zero or more active partial locks.
type LockInfo struct {
	GlobalLock  *GlobalLock       `xml:"global-lock"`
	PartialLock []PartialLockInfo `xml:"partial-lock"`
}

// GlobalLock describes an active global datastore lock (RFC 6022 §3.1.2.1.1).
//
//   - LockedBySession: session-id that holds the lock.
//   - LockedTime:      timestamp when the lock was acquired (RFC 3339 format).
type GlobalLock struct {
	LockedBySession uint32 `xml:"locked-by-session"`
	LockedTime      string `xml:"locked-time"`
}

// PartialLockInfo describes one active partial-lock entry (RFC 6022 §3.1.2.1.2).
// This type is named PartialLockInfo (not PartialLock) to avoid a name
// collision with the netconf.PartialLock operation type defined in the base
// netconf package (S03).
//
//   - LockID:      the lock-id assigned by the server.
//   - LockedTime:  timestamp when the lock was acquired (RFC 3339 format).
//   - LockedNode:  list of locked node canonical XPath expressions.
//   - Select:      list of XPath 1.0 select expressions used when acquiring the lock.
type PartialLockInfo struct {
	LockID     uint32   `xml:"lock-id"`
	LockedTime string   `xml:"locked-time"`
	LockedNode []string `xml:"locked-node"`
	Select     []string `xml:"select"`
}

// Schema describes one schema available for retrieval via get-schema
// (RFC 6022 §3.1.3).
//
//   - Identifier: the schema identifier (e.g. a YANG module name).
//   - Version:    the schema version string.
//   - Format:     the schema language format (e.g. "yang", "yin", "xsd").
//   - Namespace:  the XML namespace the schema defines.
//   - Location:   list of retrieval locations; the special value "NETCONF"
//     indicates the schema is available via get-schema.
type Schema struct {
	Identifier string   `xml:"identifier"`
	Version    string   `xml:"version"`
	Format     string   `xml:"format"`
	Namespace  string   `xml:"namespace"`
	Location   []string `xml:"location"`
}

// Session describes one currently active NETCONF session (RFC 6022 §3.1.4).
//
//   - SessionID:        unique session-id assigned by the server.
//   - Transport:        transport protocol in use (e.g. "netconf-ssh", "netconf-tls").
//   - Username:         NETCONF username of the session.
//   - SourceHost:       IP address or hostname of the client; omitted when absent.
//   - LoginTime:        timestamp when the session was established (RFC 3339).
//   - InRPCs:           number of valid <rpc> messages received.
//   - InBadRPCs:        number of invalid <rpc> messages received.
//   - OutRPCErrors:     number of <rpc-reply> messages sent with <rpc-error>.
//   - OutNotifications: number of <notification> messages sent.
type Session struct {
	SessionID        uint32 `xml:"session-id"`
	Transport        string `xml:"transport"`
	Username         string `xml:"username"`
	SourceHost       string `xml:"source-host,omitempty"`
	LoginTime        string `xml:"login-time"`
	InRPCs           uint32 `xml:"in-rpcs"`
	InBadRPCs        uint32 `xml:"in-bad-rpcs"`
	OutRPCErrors     uint32 `xml:"out-rpc-errors"`
	OutNotifications uint32 `xml:"out-notifications"`
}

// Statistics holds aggregate message and session counters (RFC 6022 §3.1.5).
//
//   - NetconfStartTime:  timestamp when the NETCONF subsystem was started (RFC 3339).
//   - InBadHellos:       number of sessions dropped because of badly-formed hellos.
//   - InSessions:        number of sessions started since start time.
//   - DroppedSessions:   number of sessions dropped abnormally.
//   - InRPCs:            number of valid <rpc> messages received.
//   - InBadRPCs:         number of invalid <rpc> messages received.
//   - OutRPCErrors:      number of <rpc-reply> messages sent with <rpc-error>.
//   - OutNotifications:  number of <notification> messages sent.
type Statistics struct {
	NetconfStartTime string `xml:"netconf-start-time"`
	InBadHellos      uint32 `xml:"in-bad-hellos"`
	InSessions       uint32 `xml:"in-sessions"`
	DroppedSessions  uint32 `xml:"dropped-sessions"`
	InRPCs           uint32 `xml:"in-rpcs"`
	InBadRPCs        uint32 `xml:"in-bad-rpcs"`
	OutRPCErrors     uint32 `xml:"out-rpc-errors"`
	OutNotifications uint32 `xml:"out-notifications"`
}

// GetSchemaRequest is the input payload for the get-schema RPC (RFC 6022 §3.2).
// It must be wrapped in an <rpc> element via client.Client.Do or
// client.Client.GetSchema.
//
//   - Identifier: the schema identifier to retrieve (required).
//   - Version:    the specific version to retrieve; omit for the latest.
//   - Format:     the desired schema language format (e.g. "yang"); omit for default.
type GetSchemaRequest struct {
	XMLName    xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring get-schema"`
	Identifier string   `xml:"identifier"`
	Version    string   `xml:"version,omitempty"`
	Format     string   `xml:"format,omitempty"`
}

// GetSchemaReply is the decoded reply from a get-schema RPC (RFC 6022 §3.3).
// The server wraps the schema document in a <data> element in the rpc-reply
// body, and the raw schema bytes (YANG text, YIN XML, or XSD) are exposed as
// Content.
//
//   - Content: the raw schema document bytes as returned by the server.
type GetSchemaReply struct {
	XMLName xml.Name `xml:"data"`
	Content []byte   `xml:",innerxml"`
}
