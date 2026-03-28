// Package subscriptions implements the ietf-subscriptions YANG module (RFC 8639)
// and the ietf-netconf-subscriptions YANG module (RFC 8640).
//
// It provides Go struct types for the RFC 8639 dynamic subscription operations
// (establish, modify, delete, kill) and the RFC 8639 subscription lifecycle
// notification bodies (subscription-started, subscription-modified,
// subscription-terminated, subscription-killed).
//
// # Namespaces
//
// RFC 8639 operations and notification bodies use SubscriptionsNS:
//
//	"urn:ietf:params:xml:ns:yang:ietf-subscriptions"
//
// RFC 8640 NETCONF-transport-specific bindings use NetconfSubscriptionsNS:
//
//	"urn:ietf:params:xml:ns:yang:ietf-netconf-subscriptions"
//
// Both are YANG module namespace URIs, NOT NETCONF capability URNs of the form
// "urn:ietf:params:netconf:capability:…". Do not pass them to netconf.ValidateURN
// (per P020).
//
// # Dynamic Subscriptions (RFC 8639 §2.4)
//
// A subscriber establishes a subscription via [EstablishSubscriptionRequest] and
// receives a [EstablishSubscriptionReply] containing the assigned subscription ID.
// The subscriber may later modify, delete, or kill the subscription.
//
// Subscription lifecycle notifications ([SubscriptionStarted],
// [SubscriptionModified], [SubscriptionTerminated], [SubscriptionKilled])
// are delivered as RFC 5277 <notification> messages. Callers unmarshal the
// notification body into the appropriate type by wrapping it in a synthetic
// root element per P012.
//
// # Observability Impact
//
// Types in this package are pure encoding/decoding structs with no runtime state.
// Failure visibility is through XML marshal/unmarshal errors and go test output.
//
//   - go test ./netconf/subscriptions/... -v — per-struct round-trip pass/fail
//     with actual marshaled XML printed in failure messages via t.Logf.
//   - The SubscriptionsNS constant is testable via:
//     assert.Equal(t, subscriptions.SubscriptionsNS, "urn:ietf:params:xml:ns:yang:ietf-subscriptions")
package subscriptions

import "encoding/xml"

// SubscriptionsNS is the XML namespace for the ietf-subscriptions YANG module
// (RFC 8639). All RFC 8639 operation and notification elements in this package
// are qualified with this namespace.
const SubscriptionsNS = "urn:ietf:params:xml:ns:yang:ietf-subscriptions"

// CapabilityURI is the URI a server includes in its <hello> capabilities list
// to advertise support for the ietf-subscriptions YANG module (RFC 8639).
//
// Note: this is a YANG module namespace URI, not a
// "urn:ietf:params:netconf:capability:…" URN. Do not pass it to
// netconf.ValidateURN (per P020).
const CapabilityURI = "urn:ietf:params:xml:ns:yang:ietf-subscriptions"

// NetconfSubscriptionsNS is the XML namespace for the ietf-netconf-subscriptions
// YANG module (RFC 8640), which defines NETCONF-transport-specific bindings for
// RFC 8639 subscriptions.
const NetconfSubscriptionsNS = "urn:ietf:params:xml:ns:yang:ietf-netconf-subscriptions"

// CapabilityURINetconf is the URI a server includes in its <hello> capabilities
// list to advertise support for the ietf-netconf-subscriptions YANG module (RFC 8640).
//
// Note: this is a YANG module namespace URI, not a
// "urn:ietf:params:netconf:capability:…" URN. Do not pass it to
// netconf.ValidateURN (per P020).
const CapabilityURINetconf = "urn:ietf:params:xml:ns:yang:ietf-netconf-subscriptions"

// SubscriptionID is a uint32 identifying a specific subscription instance.
// It is assigned by the server when a subscription is established and used
// in all subsequent modify, delete, and kill operations.
type SubscriptionID = uint32

// SubtreeFilterContent is a helper type that wraps raw inner XML bytes in a
// <subtree-filter> element. encoding/xml requires a struct field to emit a
// named wrapper element around innerxml content; a bare []byte with a named
// tag would be escaped as text rather than emitted as raw XML (per P003).
type SubtreeFilterContent struct {
	Content []byte `xml:",innerxml"`
}

// FilterSpec represents the filter choice within a subscription request
// (RFC 8639 §2.3). Exactly one of SubtreeFilter or XPathFilter should be set.
//
//   - SubtreeFilter: wraps raw inner XML bytes in a <subtree-filter> element;
//     content is emitted verbatim via innerxml (per P003).
//   - XPathFilter: XPath 1.0 expression selecting the event records of interest.
type FilterSpec struct {
	SubtreeFilter *SubtreeFilterContent `xml:"subtree-filter,omitempty"`
	XPathFilter   string                `xml:"xpath-filter,omitempty"`
}

// EstablishSubscriptionRequest is the establish-subscription RPC input
// (RFC 8639 §2.4.1). It requests creation of a dynamic subscription.
//
//   - Stream:     name of the event stream; defaults to "NETCONF" when empty.
//   - Filter:     optional filter to select a subset of event records.
//   - StopTime:   optional xs:dateTime after which the subscription expires.
//   - Period:     optional uint64 for periodic yang-push subscriptions (RFC 8641).
//     Included here for forward compatibility; S05 uses this field.
//   - Dscp:       optional DSCP value for network-level QoS marking.
type EstablishSubscriptionRequest struct {
	XMLName   xml.Name    `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions establish-subscription"`
	Stream    string      `xml:"stream,omitempty"`
	Filter    *FilterSpec `xml:"filter,omitempty"`
	StopTime  string      `xml:"stop-time,omitempty"`
	Period    uint64      `xml:"period,omitempty"`
	Dscp      uint8       `xml:"dscp,omitempty"`
}

// EstablishSubscriptionReply is the establish-subscription RPC output
// (RFC 8639 §2.4.1). The server returns the assigned subscription ID on success.
//
//   - ID: the server-assigned subscription identifier; used in all subsequent
//     modify, delete, and kill operations for this subscription.
type EstablishSubscriptionReply struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions establish-subscription-reply"`
	ID      SubscriptionID `xml:"id"`
}

// ModifySubscriptionRequest is the modify-subscription RPC input
// (RFC 8639 §2.4.2). It changes parameters of an existing subscription.
//
//   - ID:       subscription to modify; must reference an established subscription.
//   - Filter:   new filter replacing the current filter, or nil to remove filtering.
//   - StopTime: new stop-time, or empty string to clear.
type ModifySubscriptionRequest struct {
	XMLName  xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions modify-subscription"`
	ID       SubscriptionID `xml:"id"`
	Filter   *FilterSpec    `xml:"filter,omitempty"`
	StopTime string         `xml:"stop-time,omitempty"`
}

// ModifySubscriptionReply is the modify-subscription RPC output (RFC 8639 §2.4.2).
// On success, the server returns an empty body (signaled by <ok/> in the rpc-reply).
// This type is a placeholder for future extensions; callers typically just check
// for the absence of an rpc-error.
type ModifySubscriptionReply struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions modify-subscription-reply"`
}

// DeleteSubscription is the delete-subscription RPC input (RFC 8639 §2.4.3).
// It terminates an existing subscription gracefully.
//
//   - ID: subscription to delete; must reference an established subscription
//     owned by the requesting session.
type DeleteSubscription struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions delete-subscription"`
	ID      SubscriptionID `xml:"id"`
}

// KillSubscription is the kill-subscription RPC input (RFC 8639 §2.4.4).
// It forcibly terminates any subscription, regardless of ownership. Typically
// used by administrators.
//
//   - ID:     subscription to kill.
//   - Reason: optional human-readable reason for the termination.
type KillSubscription struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions kill-subscription"`
	ID      SubscriptionID `xml:"id"`
	Reason  string         `xml:"reason,omitempty"`
}

// ── Subscription lifecycle notification bodies ────────────────────────────────

// SubscriptionStarted is the body of a subscription-started notification
// (RFC 8639 §2.7.1). Delivered to the subscriber when a dynamic subscription
// has been successfully established. Its XMLName uses SubscriptionsNS.
//
//   - ID:     the subscription identifier assigned by the server.
//   - Stream: the event stream to which the subscription is bound.
type SubscriptionStarted struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions subscription-started"`
	ID      SubscriptionID `xml:"id"`
	Stream  string         `xml:"stream,omitempty"`
}

// SubscriptionModified is the body of a subscription-modified notification
// (RFC 8639 §2.7.2). Delivered to the subscriber after a successful
// modify-subscription operation.
//
//   - ID: the subscription identifier of the modified subscription.
type SubscriptionModified struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions subscription-modified"`
	ID      SubscriptionID `xml:"id"`
}

// SubscriptionTerminated is the body of a subscription-terminated notification
// (RFC 8639 §2.7.3). Delivered to the subscriber when the server has ended the
// subscription (e.g., because stop-time expired or the stream was deleted).
//
//   - ID:     the subscription identifier.
//   - Reason: the reason for termination (e.g., "filter-unavailable", "stream-unavailable").
type SubscriptionTerminated struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions subscription-terminated"`
	ID      SubscriptionID `xml:"id"`
	Reason  string         `xml:"reason,omitempty"`
}

// SubscriptionKilled is the body of a subscription-killed notification
// (RFC 8639 §2.7.4). Delivered to the subscriber whose subscription was
// forcibly terminated by a kill-subscription operation.
//
//   - ID:     the subscription identifier.
//   - Reason: the reason provided by the kill-subscription caller.
type SubscriptionKilled struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:yang:ietf-subscriptions subscription-killed"`
	ID      SubscriptionID `xml:"id"`
	Reason  string         `xml:"reason,omitempty"`
}
