// Package nacm implements the ietf-netconf-acm YANG module (RFC 8341).
//
// It provides Go struct types for the NACM (NETCONF Access Control Model)
// data model and an enforcement function that evaluates access control
// decisions against a NACM configuration.
//
// # Namespace
//
// All types in this package use the YANG module namespace
// "urn:ietf:params:xml:ns:yang:ietf-netconf-acm" (NacmNS).
// This is a YANG module namespace, NOT a NETCONF capability URN of the form
// "urn:ietf:params:netconf:capability:…", so it must NOT be passed to
// netconf.ValidateURN. Use CapabilityURI to announce the capability in a
// NETCONF hello exchange.
//
// # Data Model
//
// The NACM data model (RFC 8341 §3.2) defines a single /nacm container with:
//
//   - Global parameters: enable-nacm, read/write/exec default actions,
//     enable-external-groups, and deny counters.
//   - /nacm/groups: list of named groups, each with member usernames.
//   - /nacm/rule-list: ordered list of rule-lists; each rule-list targets
//     one or more groups and contains an ordered list of rules.
//
// Each rule specifies: the YANG module it applies to, the type of access
// (protocol-operation, notification, or data-node), the operation name
// (or "*" for wildcard), the allowed access operations, and the action
// (permit or deny).
//
// # Enforcement
//
// Use [Enforce] to evaluate an access request against a [Nacm] configuration.
// See enforce.go for algorithm details (RFC 8341 §3.4).
//
// # Observability Impact
//
// Types in this package are pure encoding/decoding structs and a pure
// enforcement function; there is no runtime process or daemon. Failure
// visibility is through XML marshal/unmarshal errors and go test output.
//
//   - go test ./... -v — per-struct round-trip pass/fail
//     with actual marshaled XML printed in failure messages via t.Logf.
//   - The NacmNS constant value is testable via:
//     assert.Equal(t, nacm.NacmNS, "urn:ietf:params:xml:ns:yang:ietf-netconf-acm")
package nacm

import "encoding/xml"

// NacmNS is the XML namespace for the ietf-netconf-acm YANG module (RFC 8341).
// All elements in this package are qualified with this namespace.
const NacmNS = "urn:ietf:params:xml:ns:yang:ietf-netconf-acm"

// CapabilityURI is the URI a server includes in its <hello> capabilities list
// to advertise support for the ietf-netconf-acm YANG module (RFC 8341).
//
// Note: this is a YANG module namespace URI, not a
// "urn:ietf:params:netconf:capability:…" URN. Do not pass it to
// netconf.ValidateURN.
const CapabilityURI = "urn:ietf:params:xml:ns:yang:ietf-netconf-acm"

// Action represents the NACM rule action (RFC 8341 §3.2.6).
// It determines whether access is granted or denied when a rule matches.
type Action = string

const (
	// ActionPermit grants access to the operation or notification.
	ActionPermit Action = "permit"

	// ActionDeny denies access to the operation or notification.
	ActionDeny Action = "deny"
)

// RuleType represents the type of NACM rule (RFC 8341 §3.2.6).
// It indicates which YANG schema node type the rule applies to.
type RuleType = string

const (
	// RuleTypeProtocolOperation applies to NETCONF protocol operations (RPCs).
	RuleTypeProtocolOperation RuleType = "protocol-operation"

	// RuleTypeNotification applies to NETCONF notifications.
	RuleTypeNotification RuleType = "notification"

	// RuleTypeDataNode applies to YANG data nodes (read/write access control).
	RuleTypeDataNode RuleType = "data-node"
)

// Nacm is the top-level /nacm container (RFC 8341 §3.2).
//
// It holds the global NACM configuration parameters, the groups definition,
// and the rule-list ordering.
//
//   - EnableNacm:            master switch; when false, all access is permitted.
//   - ReadDefault:           action applied to read access when no rule matches.
//   - WriteDefault:          action applied to write access when no rule matches.
//   - ExecDefault:           action applied to exec access when no rule matches.
//   - EnableExternalGroups:  when true, group membership may be taken from
//     external AAA systems in addition to /nacm/groups.
//   - DeniedOperations:      counter of denied protocol operations (read-only in RFC,
//     useful for testing and state capture).
//   - DeniedDataWrites:      counter of denied data-write operations.
//   - DeniedNotifications:   counter of denied notification deliveries.
//   - Groups:                group definitions.
//   - RuleLists:             ordered rule-lists; first-match wins.
type Nacm struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-acm nacm"`

	// Global control
	EnableNacm           bool   `xml:"enable-nacm"`
	ReadDefault          Action `xml:"read-default,omitempty"`
	WriteDefault         Action `xml:"write-default,omitempty"`
	ExecDefault          Action `xml:"exec-default,omitempty"`
	EnableExternalGroups bool   `xml:"enable-external-groups,omitempty"`

	// State counters (config false in RFC, included for completeness and testing)
	DeniedOperations    uint32 `xml:"denied-operations,omitempty"`
	DeniedDataWrites    uint32 `xml:"denied-data-writes,omitempty"`
	DeniedNotifications uint32 `xml:"denied-notifications,omitempty"`

	// Access control lists
	Groups    *Groups    `xml:"groups,omitempty"`
	RuleLists []RuleList `xml:"rule-list"`
}

// Groups is the /nacm/groups container (RFC 8341 §3.2.2).
// It holds the list of locally-defined NACM groups.
type Groups struct {
	Group []Group `xml:"group"`
}

// Group is a single named group within /nacm/groups (RFC 8341 §3.2.2).
//
//   - Name:     the group identifier (used in rule-list/group references).
//   - UserName: the list of NETCONF usernames that are members of this group.
type Group struct {
	XMLName  xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-acm group"`
	Name     string   `xml:"name"`
	UserName []string `xml:"user-name"`
}

// RuleList is a single entry in /nacm/rule-list (RFC 8341 §3.2.4).
// Rule-lists are evaluated in document order; the first matching rule
// determines the access decision.
//
//   - Name:  unique identifier for the rule-list.
//   - Group: groups this rule-list applies to. An empty slice applies to all groups.
//   - Rules: ordered list of rules within this rule-list.
type RuleList struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-acm rule-list"`
	Name    string   `xml:"name"`
	Group   []string `xml:"group"`
	Rules   []Rule   `xml:"rule"`
}

// Rule is a single access control rule within a /nacm/rule-list (RFC 8341 §3.2.6).
//
// A rule matches a request when all non-wildcard fields match:
//
//   - ModuleName must match the YANG module name of the operation, or be "*".
//   - One of ProtocolOperation, Notification, or Path must match the specific
//     node being accessed (YANG choice "rule-type").
//   - AccessOperations must include the type of access being requested, or be "*".
//
// When a rule matches, Action is returned as the enforcement decision.
//
//   - Name:               unique identifier for the rule.
//   - ModuleName:         YANG module name to match, or "*" for any.
//   - ProtocolOperation:  non-nil for protocol-operation rules (exec access).
//   - Notification:       non-nil for notification rules (read access).
//   - Path:               non-empty XPath string for data-node rules.
//   - AccessOperations:   space-separated list of operations ("*", "exec", "read",
//     "create", "update", "delete"); "*" matches all.
//   - Action:             ActionPermit or ActionDeny.
//   - Comment:            optional human-readable description.
type Rule struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-netconf-acm rule"`

	Name       string `xml:"name"`
	ModuleName string `xml:"module-name,omitempty"`

	// Rule type (YANG choice "rule-type"): at most one should be non-nil/non-empty.
	ProtocolOperation *ProtocolOperationRule `xml:"protocol-operation,omitempty"`
	Notification      *NotificationRule      `xml:"notification,omitempty"`
	Path              string                 `xml:"path,omitempty"`

	AccessOperations string `xml:"access-operations,omitempty"`
	Action           Action `xml:"action"`
	Comment          string `xml:"comment,omitempty"`
}

// ProtocolOperationRule is the rule-type for protocol operations (RFC 8341 §3.2.6).
//
//   - RPCName: the local name of the NETCONF RPC to match (e.g. "get-config"),
//     or "*" to match any RPC.
type ProtocolOperationRule struct {
	RPCName string `xml:"rpc-name"`
}

// NotificationRule is the rule-type for notifications (RFC 8341 §3.2.6).
//
//   - NotificationName: the local name of the NETCONF notification to match
//     (e.g. "netconf-config-change"), or "*" to match any notification.
type NotificationRule struct {
	NotificationName string `xml:"notification-name"`
}
