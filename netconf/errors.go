// Package netconf implements the NETCONF protocol (RFC 6241).
//
// This file defines the rpc-error model described in RFC 6241 §4.3.
// RPCError is the canonical error type for all NETCONF operation responses;
// ParseRPCErrors extracts one or more rpc-error elements from an RPCReply.
package netconf

import (
	"encoding/xml"
	"fmt"
)

// RPCError represents a single NETCONF <rpc-error> element as defined in
// RFC 6241 §4.3.  It implements the standard error interface so it can be
// returned directly from Go functions.
//
// Field mapping (RFC 6241 §4.3 → XML element → Go field):
//
//	error-type     → Type     — one of: transport, rpc, protocol, application
//	error-tag      → Tag      — machine-readable error identifier
//	error-severity → Severity — "error" or "warning"
//	error-app-tag  → AppTag   — device/application-specific tag (optional)
//	error-path     → Path     — XPath to the element causing the error (optional)
//	error-message  → Message  — human-readable description (optional)
//	error-info     → Info     — raw inner XML of arbitrary <error-info> children
type RPCError struct {
	XMLName  xml.Name `xml:"rpc-error"`
	Type     string   `xml:"error-type"`
	Tag      string   `xml:"error-tag"`
	Severity string   `xml:"error-severity"`
	AppTag   string   `xml:"error-app-tag,omitempty"`
	Path     string   `xml:"error-path,omitempty"`
	Message  string   `xml:"error-message,omitempty"`
	// Info captures the raw inner XML of any <error-info> children verbatim.
	// Using innerxml preserves arbitrary vendor-specific sub-elements without
	// requiring a schema — a future parser can decode specific sub-elements.
	Info []byte `xml:",innerxml"`
}

// Error implements the error interface.  The string includes the error type,
// tag, severity, and human-readable message so it is immediately actionable
// in log output and error chains.
func (e RPCError) Error() string {
	return fmt.Sprintf("rpc-error: type=%s tag=%s severity=%s message=%s",
		e.Type, e.Tag, e.Severity, e.Message)
}

// RPCErrors is a slice of RPCError values returned by ParseRPCErrors.
// Defining it as a named type lets callers range over it cleanly and makes
// function signatures self-documenting.
type RPCErrors = []RPCError

// wrapperDoc is the synthetic root element used internally by ParseRPCErrors.
// encoding/xml requires a single root element; RPCReply.Body may contain
// multiple sibling <rpc-error> elements with no wrapper, so we synthesise one.
type wrapperDoc struct {
	XMLName xml.Name   `xml:"wrapper"`
	Errors  []RPCError `xml:"rpc-error"`
}

// ParseRPCErrors extracts all <rpc-error> elements from reply.Body.
//
// RPCReply.Body is raw innerxml — it may contain zero or more <rpc-error>
// siblings with no enclosing root element.  This function wraps the bytes in
// a synthetic <wrapper> element so that encoding/xml can decode the sibling
// list as a slice.
//
// Return values:
//   - (nil, nil)         — reply.Body is empty or contains no rpc-error elements
//   - ([]RPCError, nil)  — one or more errors were decoded successfully
//   - (nil, err)         — Body is non-empty but not valid XML
func ParseRPCErrors(reply *RPCReply) ([]RPCError, error) {
	if len(reply.Body) == 0 {
		return nil, nil
	}

	wrapped := append([]byte("<wrapper>"), reply.Body...)
	wrapped = append(wrapped, []byte("</wrapper>")...)

	var doc wrapperDoc
	if err := xml.Unmarshal(wrapped, &doc); err != nil {
		return nil, fmt.Errorf("ParseRPCErrors: xml decode: %w", err)
	}

	if len(doc.Errors) == 0 {
		return nil, nil
	}
	return doc.Errors, nil
}
