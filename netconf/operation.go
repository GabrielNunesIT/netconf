// Package netconf implements the NETCONF protocol (RFC 6241).
//
// This file defines the 13 base protocol operation request types described in
// RFC 6241 §7, plus the shared Datastore, Filter, and DataReply types used
// across multiple operations.
//
// Every operation struct carries the NETCONF base namespace in its XMLName tag
// (lesson L001: namespace is set statically in the struct tag, never at runtime).
// This ensures xml.Marshal always emits xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"
// on the operation element.
package netconf

import "encoding/xml"

// ── Shared types ──────────────────────────────────────────────────────────────

// Datastore represents a NETCONF configuration datastore reference.
// It is used as the Source or Target in operations such as GetConfig,
// EditConfig, CopyConfig, DeleteConfig, Lock, Unlock, and Validate.
//
// Exactly one field should be non-nil / non-empty for a valid datastore selector.
// Each field encodes as a child element within the enclosing <source> or <target>:
//   - Running:   <running/>
//   - Candidate: <candidate/>
//   - Startup:   <startup/>
//   - URL:       <url>https://…</url>
//
// Using *struct{} for the boolean datastores gives omitempty semantics with
// child-element encoding. A non-nil pointer marshals as an empty element.
type Datastore struct {
	Running   *struct{} `xml:"running,omitempty"`
	Candidate *struct{} `xml:"candidate,omitempty"`
	Startup   *struct{} `xml:"startup,omitempty"`
	URL       string    `xml:"url,omitempty"`
}

// Filter represents a NETCONF filter element used in <get> and <get-config>.
// RFC 6241 §6 defines two filter types:
//
//   - Subtree (type="subtree"): Content holds the raw inner XML of the
//     filter criteria. Use the innerxml tag so arbitrary filter subtrees
//     are preserved verbatim without requiring a schema.
//
//   - XPath (type="xpath"): Select holds the XPath expression. Requires
//     the :xpath capability (CapabilityXPath) to be advertised by the device.
//
// The Type attribute discriminates between the two modes.
type Filter struct {
	Type    string `xml:"type,attr,omitempty"`
	Select  string `xml:"select,attr,omitempty"`
	Content []byte `xml:",innerxml"`
}

// DataReply wraps the <data> element returned in the body of a get or
// get-config response. Unmarshal RPCReply.Body into a DataReply to access
// the raw configuration content without writing a schema.
//
// Example:
//
//	var dr netconf.DataReply
//	if err := xml.Unmarshal(reply.Body, &dr); err != nil { … }
//	// dr.Content holds the raw inner XML of <data>
type DataReply struct {
	XMLName xml.Name `xml:"data"`
	Content []byte   `xml:",innerxml"`
}

// ── RFC 6243 with-defaults ────────────────────────────────────────────────────

// WithDefaultsNS is the XML namespace for the with-defaults parameter (RFC 6243 §4).
const WithDefaultsNS = "urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults"

// WithDefaultsMode is the mode value for the with-defaults parameter (RFC 6243 §3).
// It controls which default values appear in the device's response.
type WithDefaultsMode string

const (
	// WithDefaultsReportAll causes all default values to be reported (RFC 6243 §3.1).
	WithDefaultsReportAll WithDefaultsMode = "report-all"

	// WithDefaultsTrim causes default values to be omitted from the reply (RFC 6243 §3.2).
	WithDefaultsTrim WithDefaultsMode = "trim"

	// WithDefaultsExplicit causes only explicitly set values to be reported (RFC 6243 §3.3).
	WithDefaultsExplicit WithDefaultsMode = "explicit"

	// WithDefaultsReportAllTagged causes all default values to be reported with
	// a wd:default="true" annotation (RFC 6243 §3.4).
	WithDefaultsReportAllTagged WithDefaultsMode = "report-all-tagged"
)

// WithDefaultsParam encodes the <with-defaults> parameter element required by
// RFC 6243 §4. The element uses the with-defaults YANG namespace and carries
// the mode as character data.
//
// Example wire output:
//
//	<with-defaults xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults">report-all</with-defaults>
type WithDefaultsParam struct {
	XMLName xml.Name         `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults with-defaults"`
	Mode    WithDefaultsMode `xml:",chardata"`
}

// ── Read operations ───────────────────────────────────────────────────────────

// Get retrieves running configuration and state data (RFC 6241 §7.7).
// Filter is optional; when nil all data is returned.
// WithDefaults is optional; when nil the parameter is omitted (backward compatible).
// Requires CapabilityWithDefaults on the device when set.
type Get struct {
	XMLName      xml.Name           `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 get"`
	Filter       *Filter            `xml:"filter,omitempty"`
	WithDefaults *WithDefaultsParam `xml:",omitempty"`
}

// GetConfig retrieves all or part of a specified configuration datastore
// (RFC 6241 §7.1). Source identifies the datastore; Filter is optional.
// WithDefaults is optional; when nil the parameter is omitted (backward compatible).
// Requires CapabilityWithDefaults on the device when set.
type GetConfig struct {
	XMLName      xml.Name           `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 get-config"`
	Source       Datastore          `xml:"source"`
	Filter       *Filter            `xml:"filter,omitempty"`
	WithDefaults *WithDefaultsParam `xml:",omitempty"`
}

// ── Write operations ──────────────────────────────────────────────────────────

// EditConfig loads part or all of a specified configuration into the target
// datastore (RFC 6241 §7.2).
//
// DefaultOperation, TestOption, and ErrorOption are optional string fields;
// when non-empty they encode as child elements. Config holds the raw inner XML
// of the <config> element (arbitrary configuration content).
type EditConfig struct {
	XMLName          xml.Name  `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 edit-config"`
	Target           Datastore `xml:"target"`
	DefaultOperation string    `xml:"default-operation,omitempty"`
	TestOption       string    `xml:"test-option,omitempty"`
	ErrorOption      string    `xml:"error-option,omitempty"`
	Config           []byte    `xml:",innerxml"`
}

// CopyConfig creates or replaces an entire configuration datastore with the
// contents of another (RFC 6241 §7.3).
// WithDefaults is optional; when nil the parameter is omitted (backward compatible).
// Requires CapabilityWithDefaults on the device when set.
type CopyConfig struct {
	XMLName      xml.Name           `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 copy-config"`
	Target       Datastore          `xml:"target"`
	Source       Datastore          `xml:"source"`
	WithDefaults *WithDefaultsParam `xml:",omitempty"`
}

// DeleteConfig deletes a configuration datastore (RFC 6241 §7.4).
// The running datastore cannot be deleted.
type DeleteConfig struct {
	XMLName xml.Name  `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 delete-config"`
	Target  Datastore `xml:"target"`
}

// ── Lock operations ───────────────────────────────────────────────────────────

// Lock locks the entire configuration datastore for the current session
// (RFC 6241 §7.5). Target identifies the datastore to lock.
type Lock struct {
	XMLName xml.Name  `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 lock"`
	Target  Datastore `xml:"target"`
}

// Unlock releases the lock held on a configuration datastore by this session
// (RFC 6241 §7.6). Target identifies the datastore to unlock.
type Unlock struct {
	XMLName xml.Name  `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 unlock"`
	Target  Datastore `xml:"target"`
}

// ── Session operations ────────────────────────────────────────────────────────

// CloseSession requests a graceful termination of the current NETCONF session
// (RFC 6241 §7.8). It carries no body fields.
type CloseSession struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 close-session"`
}

// KillSession forces the termination of another NETCONF session
// (RFC 6241 §7.9). SessionID identifies the session to terminate.
type KillSession struct {
	XMLName   xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 kill-session"`
	SessionID uint32   `xml:"session-id"`
}

// ── Capability-gated operations ───────────────────────────────────────────────

// Validate validates the contents of a configuration datastore
// (RFC 6241 §8.6). Requires CapabilityValidate.
// Source identifies the datastore (or candidate) to validate.
type Validate struct {
	XMLName xml.Name  `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 validate"`
	Source  Datastore `xml:"source"`
}

// Commit commits the candidate configuration as the device's new running
// configuration (RFC 6241 §8.3). Requires CapabilityCandidate.
//
// Optional confirmed-commit fields (RFC 6241 §8.4, requires CapabilityConfirmedCommit):
//   - Confirmed:      when non-nil, initiates a confirmed commit
//   - ConfirmTimeout: timeout in seconds (default 600 per RFC)
//   - Persist:        token to identify a persistent confirmed commit
//   - PersistID:      confirms a prior persistent confirmed commit
type Commit struct {
	XMLName        xml.Name  `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 commit"`
	Confirmed      *struct{} `xml:"confirmed,omitempty"`
	ConfirmTimeout uint32    `xml:"confirm-timeout,omitempty"`
	Persist        string    `xml:"persist,omitempty"`
	PersistID      string    `xml:"persist-id,omitempty"`
}

// DiscardChanges reverts the candidate configuration to the current running
// configuration (RFC 6241 §8.3.4). Requires CapabilityCandidate.
// It carries no body fields.
type DiscardChanges struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 discard-changes"`
}

// CancelCommit cancels an ongoing confirmed commit (RFC 6241 §8.4.9).
// Requires CapabilityConfirmedCommit.
// PersistID, when non-empty, identifies the persistent confirmed commit to cancel.
type CancelCommit struct {
	XMLName   xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 cancel-commit"`
	PersistID string   `xml:"persist-id,omitempty"`
}

// ── RFC 5277 notification operations ─────────────────────────────────────────

// CreateSubscription requests the creation of a notification subscription
// (RFC 5277 §2.1.1). Requires CapabilityNotification.
//
// The XMLName uses the RFC 5277 notification namespace
// (urn:ietf:params:xml:ns:netconf:notification:1.0), which is distinct from
// the base NETCONF namespace. This is critical for correct wire encoding.
//
// All fields are optional:
//   - Stream:     name of the event stream to subscribe to (default: NETCONF)
//   - Filter:     subtree or XPath filter to select events
//   - StartTime:  xs:dateTime replay start (requires stored events)
//   - StopTime:   xs:dateTime replay end (only valid with StartTime)
type CreateSubscription struct {
	XMLName   xml.Name `xml:"urn:ietf:params:xml:ns:netconf:notification:1.0 create-subscription"`
	Stream    string   `xml:"stream,omitempty"`
	Filter    *Filter  `xml:"filter,omitempty"`
	StartTime string   `xml:"startTime,omitempty"`
	StopTime  string   `xml:"stopTime,omitempty"`
}

// ── RFC 5717 partial-lock operations ─────────────────────────────────────────

// PartialLock locks a subset of the configuration datastore described by XPath
// select expressions (RFC 5717 §2.1). Requires CapabilityPartialLock.
//
// Each string in Select must be a valid XPath 1.0 expression that identifies
// the configuration nodes to lock. The device returns a PartialLockReply
// containing the assigned lock-id and the list of locked node instances.
//
// RFC 5717 §3.1 uses the NETCONF base namespace for this operation element.
type PartialLock struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 partial-lock"`
	Select  []string `xml:"select"`
}

// PartialUnlock releases a partial lock previously acquired via PartialLock
// (RFC 5717 §2.2). Requires CapabilityPartialLock.
//
// LockID must be the lock-id value returned in the PartialLockReply for the
// lock being released.
type PartialUnlock struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 partial-unlock"`
	LockID  uint32   `xml:"lock-id"`
}

// PartialLockReply deserializes the reply body returned by a <partial-lock> RPC
// (RFC 5717 §2.1.3). After a successful partial-lock operation, unmarshal
// RPCReply.Body into this type to retrieve the assigned LockID and the
// canonical XPath expressions of the locked nodes.
//
// Example usage:
//
//	var plr netconf.PartialLockReply
//	if err := xml.Unmarshal(reply.Body, &plr); err != nil { … }
//	// plr.LockID holds the lock-id to pass to PartialUnlock
//	// plr.LockedNode holds the locked-node list
//
// Note: The <partial-lock-reply> element is sent without a namespace prefix
// inside the base-namespace <rpc-reply> body; matching on local name only.
type PartialLockReply struct {
	XMLName    xml.Name `xml:"partial-lock-reply"`
	LockID     uint32   `xml:"lock-id"`
	LockedNode []string `xml:"locked-node"`
}
