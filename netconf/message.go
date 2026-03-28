// Package netconf implements the NETCONF protocol (RFC 6241).
//
// This file defines the XML message types used in the NETCONF protocol:
// Hello, RPC, and RPCReply. All types marshal/unmarshal with the canonical
// NETCONF base namespace "urn:ietf:params:xml:ns:netconf:base:1.0".
package netconf

import "encoding/xml"

// NetconfNS is the XML namespace for all NETCONF base messages (RFC 6241 §3.1).
const NetconfNS = "urn:ietf:params:xml:ns:netconf:base:1.0"

// NotificationNS is the XML namespace for NETCONF notification messages (RFC 5277 §4).
// It is distinct from NetconfNS and is used by the <notification> element and the
// <create-subscription> operation.
const NotificationNS = "urn:ietf:params:xml:ns:netconf:notification:1.0"

// HelloName is the xml.Name constant for the <hello> element.
var HelloName = xml.Name{Space: NetconfNS, Local: "hello"}

// RPCName is the xml.Name constant for the <rpc> element.
var RPCName = xml.Name{Space: NetconfNS, Local: "rpc"}

// RPCReplyName is the xml.Name constant for the <rpc-reply> element.
var RPCReplyName = xml.Name{Space: NetconfNS, Local: "rpc-reply"}

// NotificationName is the xml.Name constant for the <notification> element (RFC 5277 §4).
// Its namespace is NotificationNS, not NetconfNS.
var NotificationName = xml.Name{Space: NotificationNS, Local: "notification"}

// Hello represents a NETCONF <hello> message (RFC 6241 §8.1).
//
// In the client→server direction, SessionID is zero (not included).
// In the server→client direction, SessionID carries the assigned session id.
//
// Marshaling note: encoding/xml emits the namespace as xmlns="…" on the root
// element when XMLName.Space is set, which is correct per RFC 6241.
type Hello struct {
	XMLName      xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 hello"`
	Capabilities []string `xml:"capabilities>capability"`
	SessionID    uint32   `xml:"session-id,omitempty"`
}

// RPC represents a NETCONF <rpc> request message (RFC 6241 §4.1).
//
// Body holds the raw inner XML of the operation element (e.g. <get-config>).
// Callers marshal the operation into Body before marshaling the RPC wrapper.
type RPC struct {
	XMLName   xml.Name `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 rpc"`
	MessageID string   `xml:"message-id,attr"`
	Body      []byte   `xml:",innerxml"`
}

// RPCReply represents a NETCONF <rpc-reply> response message (RFC 6241 §4.2).
//
// For successful operations with no data, Ok will be true and Body will be nil.
// For operations returning data, Body contains the raw inner XML.
// For errors, Body contains one or more <rpc-error> elements.
type RPCReply struct {
	XMLName   xml.Name  `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 rpc-reply"`
	MessageID string    `xml:"message-id,attr"`
	Ok        *struct{} `xml:"ok"`
	Body      []byte    `xml:",innerxml"`
}

// Notification represents a NETCONF <notification> event message (RFC 5277 §4).
//
// The XMLName uses the RFC 5277 notification namespace (NotificationNS), which is
// distinct from the base NETCONF namespace. EventTime is the mandatory xs:dateTime
// timestamp of the event. Body holds the raw inner XML of the event-specific content
// (everything inside <notification> after <eventTime>).
//
// Marshaling note: encoding/xml emits xmlns="urn:ietf:params:xml:ns:netconf:notification:1.0"
// on the root element when XMLName.Space is set, which is correct per RFC 5277.
type Notification struct {
	XMLName   xml.Name `xml:"urn:ietf:params:xml:ns:netconf:notification:1.0 notification"`
	EventTime string   `xml:"eventTime"`
	Body      []byte   `xml:",innerxml"`
}
