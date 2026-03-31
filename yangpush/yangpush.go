// Package yangpush implements the ietf-yang-push YANG module (RFC 8641).
//
// It provides Go struct types for the YANG-push datastore subscription
// triggers (periodic and on-change) and the push notification body types
// (push-update and push-change-update).
//
// # Namespace
//
// All types in this package use the YANG module namespace
// "urn:ietf:params:xml:ns:yang:ietf-yang-push" (YangPushNS).
// This is a YANG module namespace URI, NOT a NETCONF capability URN of the form
// "urn:ietf:params:netconf:capability:…". Do not pass it to netconf.ValidateURN
// . Use CapabilityURI to announce the capability in a hello exchange.
//
// # YANG-Push Subscription Triggers (RFC 8641 §3)
//
// A YANG-push datastore subscription can use two trigger modes:
//
//   - Periodic ([PeriodicTrigger]): pushes data snapshots at a fixed interval.
//     Period is expressed in centiseconds (hundredths of a second).
//   - On-change ([OnChangeTrigger]): pushes an update whenever the subscribed
//     data changes, with optional dampening to reduce update frequency.
//
// These trigger types are included as fields in the
// establish-subscription/modify-subscription operation. Callers build a
// subscriptions.EstablishSubscriptionRequest (which carries the Period field
// for the simple periodic case) or use the trigger types in this package for
// more complete control.
//
// # Push Notification Bodies (RFC 8641 §4)
//
// [PushUpdate] carries a periodic datastore snapshot.
// [PushChangeUpdate] carries the diff from the previous snapshot.
// Both are delivered as RFC 5277 <notification> message bodies through the
// client's notification channel.
//
// # Observability Impact
//
// Types in this package are pure encoding/decoding structs with no runtime state.
// Failure visibility is through XML marshal/unmarshal errors and go test output.
//
//   - go test ./... -v — per-struct round-trip pass/fail
//     with actual marshaled XML printed in failure messages via t.Logf.
//   - The YangPushNS constant value is testable via:
//     assert.Equal(t, yangpush.YangPushNS, "urn:ietf:params:xml:ns:yang:ietf-yang-push")
package yangpush

import "encoding/xml"

// YangPushNS is the XML namespace for the ietf-yang-push YANG module (RFC 8641).
// All elements in this package are qualified with this namespace.
const YangPushNS = "urn:ietf:params:xml:ns:yang:ietf-yang-push"

// CapabilityURI is the URI a server includes in its <hello> capabilities list
// to advertise support for the ietf-yang-push YANG module (RFC 8641).
//
// Note: this is a YANG module namespace URI, not a
// "urn:ietf:params:netconf:capability:…" URN. Do not pass it to
// netconf.ValidateURN .
const CapabilityURI = "urn:ietf:params:xml:ns:yang:ietf-yang-push"

// ── Subscription triggers ─────────────────────────────────────────────────────

// PeriodicTrigger specifies that the subscription delivers periodic datastore
// snapshots (RFC 8641 §3.1).
//
//   - Period:     push interval expressed in centiseconds (1/100 of a second).
//     A value of 100 = 1 second. Required.
//   - AnchorTime: optional xs:dateTime that the periodic schedule aligns to.
//     If absent, the server chooses an anchor time.
type PeriodicTrigger struct {
	XMLName    xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-push periodic"`
	Period     uint64   `xml:"period"`
	AnchorTime string   `xml:"anchor-time,omitempty"`
}

// OnChangeTrigger specifies that the subscription delivers updates whenever the
// subscribed data changes (RFC 8641 §3.2).
//
//   - DampeningPeriod:   minimum interval in centiseconds between consecutive
//     push-change-update notifications. 0 means no dampening (each change
//     triggers an immediate notification).
//   - SyncOnStart:       when non-nil, the server sends an initial push-update
//     snapshot when the subscription is established.
//   - ExcludedChanges:   list of change types to suppress. Valid values include
//     "create", "delete", "insert", "move", "replace". Empty list means all
//     change types are reported.
type OnChangeTrigger struct {
	XMLName         xml.Name  `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-push on-change"`
	DampeningPeriod uint64    `xml:"dampening-period,omitempty"`
	SyncOnStart     *struct{} `xml:"sync-on-start,omitempty"`
	ExcludedChanges []string  `xml:"excluded-change"`
}

// ── Push notification bodies ──────────────────────────────────────────────────

// PushUpdate is the body of a push-update notification (RFC 8641 §4.3).
// It delivers a complete datastore snapshot for a periodic subscription.
// It is delivered as the body of an RFC 5277 <notification> message.
//
//   - ID:              the subscription identifier this push relates to.
//   - ObservationTime: the time at which the datastore was observed;
//     xs:dateTime format.
//   - Datastore:       the NMDA datastore identity URN that was observed.
//   - Updates:         raw inner XML of the pushed data records.
//     The structure follows the YANG schema of the subscribed path.
type PushUpdate struct {
	XMLName         xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-push push-update"`
	ID              uint32   `xml:"id"`
	ObservationTime string   `xml:"observation-time,omitempty"`
	Datastore       string   `xml:"datastore,omitempty"`
	Updates         []byte   `xml:",innerxml"`
}

// PushChangeUpdate is the body of a push-change-update notification
// (RFC 8641 §4.4). It delivers the set of changes to the datastore since
// the previous push-update or push-change-update. It is delivered as the
// body of an RFC 5277 <notification> message.
//
//   - ID:              the subscription identifier this push relates to.
//   - ObservationTime: the time at which the changes were observed.
//   - Datastore:       the NMDA datastore identity URN.
//   - Changes:         raw inner XML of the edit records describing the changes
//    .. Each edit record identifies the target path, operation,
//     and new value.
type PushChangeUpdate struct {
	XMLName         xml.Name `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-push push-change-update"`
	ID              uint32   `xml:"id"`
	ObservationTime string   `xml:"observation-time,omitempty"`
	Datastore       string   `xml:"datastore,omitempty"`
	Changes         []byte   `xml:",innerxml"`
}
