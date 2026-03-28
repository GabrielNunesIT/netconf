// Package nmda implements the ietf-netconf-nmda YANG module (RFC 8526).
//
// It provides Go struct types for the NMDA (Network Management Datastore
// Architecture) NETCONF extensions: get-data, edit-data, delete-data, and
// copy-data operations. These operations extend the base NETCONF protocol
// to support all NMDA datastores defined in RFC 8342.
//
// # Namespace
//
// All operation types use the YANG module namespace
// "urn:ietf:params:xml:ns:yang:ietf-netconf-nmda" (NmdaNS).
// This is a YANG module namespace URI, NOT a NETCONF capability URN of the form
// "urn:ietf:params:netconf:capability:…". Do not pass it to netconf.ValidateURN
// (per P020). Use CapabilityURI to announce the capability in a hello exchange.
//
// # NMDA Datastores
//
// RFC 8342 defines the NMDA datastore architecture. The datastore identity URNs
// used in GetData, EditData, etc. are constants in this package:
//
//   - DatastoreRunning:     "urn:ietf:params:netconf:datastore:running"
//   - DatastoreCandidate:   "urn:ietf:params:netconf:datastore:candidate"
//   - DatastoreStartup:     "urn:ietf:params:netconf:datastore:startup"
//   - DatastoreIntended:    "urn:ietf:params:netconf:datastore:intended"
//   - DatastoreOperational: "urn:ietf:params:netconf:datastore:operational"
//
// # Operations
//
// [GetData] retrieves configuration or operational data from any NMDA datastore.
// [EditData] applies configuration changes to a writable datastore.
// [DeleteData] discards all data in a datastore.
// [CopyData] copies one datastore to another.
//
// # Observability Impact
//
// Types in this package are pure encoding/decoding structs with no runtime state.
// Failure visibility is through XML marshal/unmarshal errors and go test output.
//
//   - go test ./netconf/nmda/... -v — per-struct round-trip pass/fail
//     with actual marshaled XML printed in failure messages via t.Logf.
//   - The NmdaNS constant value is testable via:
//     assert.Equal(t, nmda.NmdaNS, "urn:ietf:params:xml:ns:yang:ietf-netconf-nmda")
package nmda

import "encoding/xml"

// NmdaNS is the XML namespace for the ietf-netconf-nmda YANG module (RFC 8526).
// All operation elements in this package are qualified with this namespace.
const NmdaNS = "urn:ietf:params:xml:ns:yang:ietf-netconf-nmda"

// CapabilityURI is the URI a server includes in its <hello> capabilities list
// to advertise support for the ietf-netconf-nmda YANG module (RFC 8526).
//
// Note: this is a YANG module namespace URI, not a
// "urn:ietf:params:netconf:capability:…" URN. Do not pass it to
// netconf.ValidateURN (per P020).
const CapabilityURI = "urn:ietf:params:xml:ns:yang:ietf-netconf-nmda"

// NMDA datastore identity URNs (RFC 8342 §8).
// These are the standard identifiers for the NMDA datastores. Use them as the
// Name field in a DatastoreRef.
const (
	// DatastoreRunning is the running configuration datastore.
	DatastoreRunning = "urn:ietf:params:netconf:datastore:running"

	// DatastoreCandidate is the candidate configuration datastore.
	// Requires the :candidate capability.
	DatastoreCandidate = "urn:ietf:params:netconf:datastore:candidate"

	// DatastoreStartup is the startup configuration datastore.
	// Requires the :startup capability.
	DatastoreStartup = "urn:ietf:params:netconf:datastore:startup"

	// DatastoreIntended is the intended configuration datastore (RFC 8342 §5.1).
	// Contains the final effective configuration after applying all transforms.
	DatastoreIntended = "urn:ietf:params:netconf:datastore:intended"

	// DatastoreOperational is the operational state datastore (RFC 8342 §5.3).
	// Contains the current operational state of the device.
	DatastoreOperational = "urn:ietf:params:netconf:datastore:operational"
)

// DatastoreRef identifies an NMDA datastore by its identity URN.
// Use one of the DatastoreRunning/Candidate/Startup/Intended/Operational
// constants or a custom identity value.
type DatastoreRef struct {
	Name string `xml:"name"`
}

// NmdaFilter is a filter for get-data requests (RFC 8526 §3.1).
// It is analogous to netconf.Filter but uses the NMDA operation context.
//
// Exactly one of Content (subtree) or Select+Type (XPath) should be used.
//
//   - Type:    "subtree" or "xpath"; omitted for subtree filters.
//   - Select:  XPath expression (when Type is "xpath").
//   - Content: raw inner XML of the subtree filter criteria (per P003).
type NmdaFilter struct {
	Type    string `xml:"type,attr,omitempty"`
	Select  string `xml:"select,attr,omitempty"`
	Content []byte `xml:",innerxml"`
}

// GetData retrieves data from any NMDA datastore (RFC 8526 §3.1).
//
// Unlike the base <get-config> operation (which only accesses <running>),
// get-data can retrieve data from any NMDA datastore including
// <operational>, <intended>, <candidate>, etc.
//
//   - Datastore:   the NMDA datastore to retrieve data from; use a
//     DatastoreRef{Name: DatastoreOperational} or similar.
//   - Filter:      optional filter to select a subset of the data.
//   - WithOrigin:  when non-nil, includes origin metadata in the response
//     (RFC 8342 §5.3.4). Only applicable to the operational datastore.
//   - MaxDepth:    limits the depth of the returned subtree; 0 means unlimited.
type GetData struct {
	XMLName    xml.Name      `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-nmda get-data"`
	Datastore  DatastoreRef  `xml:"datastore"`
	Filter     *NmdaFilter   `xml:"filter,omitempty"`
	WithOrigin *struct{}     `xml:"with-origin,omitempty"`
	MaxDepth   uint32        `xml:"max-depth,omitempty"`
}

// EditData applies configuration changes to a writable NMDA datastore
// (RFC 8526 §3.2).
//
// Unlike the base <edit-config> operation (which only targets <running> or
// <candidate>), edit-data can target any writable NMDA datastore.
//
//   - Datastore:         the NMDA datastore to edit; must be a writable datastore
//     (running, candidate, or startup).
//   - DefaultOperation:  default edit operation ("merge", "replace", "none").
//   - Config:            raw inner XML of the configuration to apply (per P003).
//     The content follows the same edit operation semantics as <edit-config>.
type EditData struct {
	XMLName          xml.Name     `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-nmda edit-data"`
	Datastore        DatastoreRef `xml:"datastore"`
	DefaultOperation string       `xml:"default-operation,omitempty"`
	Config           []byte       `xml:",innerxml"`
}

// DeleteData discards all data in a writable NMDA datastore (RFC 8526 §3.3).
// The datastore must exist and be writable. This operation removes all
// configuration from the target datastore.
//
//   - Datastore: the NMDA datastore to clear.
type DeleteData struct {
	XMLName   xml.Name     `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-nmda delete-data"`
	Datastore DatastoreRef `xml:"datastore"`
}

// CopyData copies the content of a source NMDA datastore to a target datastore
// (RFC 8526 §3.4). This is the NMDA equivalent of <copy-config>.
//
//   - Source: the NMDA datastore to copy from.
//   - Target: the NMDA datastore to copy to; must be writable.
type CopyData struct {
	XMLName xml.Name     `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-nmda copy-data"`
	Source  DatastoreRef `xml:"source>datastore"`
	Target  DatastoreRef `xml:"target>datastore"`
}
