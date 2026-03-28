package netconf_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── RPCError marshal / unmarshal ──────────────────────────────────────────────

// TestRPCError_MarshalUnmarshal verifies that a fully-populated RPCError
// round-trips through xml.Marshal → xml.Unmarshal with all fields preserved.
func TestRPCError_MarshalUnmarshal(t *testing.T) {
	t.Parallel()
	orig := netconf.RPCError{
		Type:     "application",
		Tag:      "invalid-value",
		Severity: "error",
		AppTag:   "my-app-tag",
		Path:     "/interfaces/interface[name='eth0']",
		Message:  "the value is invalid",
	}

	data, err := xml.Marshal(orig)
	require.NoError(t, err, "xml.Marshal should not fail")

	var got netconf.RPCError
	err = xml.Unmarshal(data, &got)
	require.NoError(t, err, "xml.Unmarshal should not fail")

	assert.Equal(t, orig.Type, got.Type, "Type field must round-trip")
	assert.Equal(t, orig.Tag, got.Tag, "Tag field must round-trip")
	assert.Equal(t, orig.Severity, got.Severity, "Severity field must round-trip")
	assert.Equal(t, orig.AppTag, got.AppTag, "AppTag field must round-trip")
	assert.Equal(t, orig.Path, got.Path, "Path field must round-trip")
	assert.Equal(t, orig.Message, got.Message, "Message field must round-trip")
}

// TestRPCError_Error verifies that the Error() method returns a string that
// contains the type, tag, severity, and message values.
func TestRPCError_Error(t *testing.T) {
	t.Parallel()
	e := netconf.RPCError{
		Type:     "protocol",
		Tag:      "missing-attribute",
		Severity: "error",
		Message:  "an attribute is missing",
	}

	s := e.Error()
	assert.Contains(t, s, "protocol", "Error() must contain error type")
	assert.Contains(t, s, "missing-attribute", "Error() must contain error tag")
	assert.Contains(t, s, "error", "Error() must contain severity")
	assert.Contains(t, s, "an attribute is missing", "Error() must contain message")
}

// TestRPCError_OptionalFieldsOmitted verifies that optional fields (AppTag,
// Path, Message) are absent from XML output when their values are zero.
func TestRPCError_OptionalFieldsOmitted(t *testing.T) {
	t.Parallel()
	e := netconf.RPCError{
		Type:     "rpc",
		Tag:      "unknown-element",
		Severity: "error",
		// AppTag, Path, Message intentionally omitted (zero values)
	}

	data, err := xml.Marshal(e)
	require.NoError(t, err)

	xmlStr := string(data)
	assert.NotContains(t, xmlStr, "error-app-tag", "AppTag must be omitted when empty")
	assert.NotContains(t, xmlStr, "error-path", "Path must be omitted when empty")
	assert.NotContains(t, xmlStr, "error-message", "Message must be omitted when empty")

	// Required fields must still be present.
	assert.Contains(t, xmlStr, "<error-type>rpc</error-type>")
	assert.Contains(t, xmlStr, "<error-tag>unknown-element</error-tag>")
	assert.Contains(t, xmlStr, "<error-severity>error</error-severity>")
}

// ── ParseRPCErrors ────────────────────────────────────────────────────────────

// TestParseRPCErrors_SingleError verifies extraction of one <rpc-error> element
// from RPCReply.Body.
func TestParseRPCErrors_SingleError(t *testing.T) {
	t.Parallel()
	body := []byte(`<rpc-error>
		<error-type>application</error-type>
		<error-tag>invalid-value</error-tag>
		<error-severity>error</error-severity>
		<error-message>bad value</error-message>
	</rpc-error>`)

	reply := &netconf.RPCReply{Body: body}
	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	require.Len(t, errs, 1)

	assert.Equal(t, "application", errs[0].Type)
	assert.Equal(t, "invalid-value", errs[0].Tag)
	assert.Equal(t, "error", errs[0].Severity)
	assert.Equal(t, "bad value", errs[0].Message)
}

// TestParseRPCErrors_MultipleErrors verifies that two sibling <rpc-error>
// elements are each decoded into separate RPCError values.
func TestParseRPCErrors_MultipleErrors(t *testing.T) {
	t.Parallel()
	body := []byte(`<rpc-error>
		<error-type>protocol</error-type>
		<error-tag>unknown-element</error-tag>
		<error-severity>error</error-severity>
	</rpc-error>
	<rpc-error>
		<error-type>application</error-type>
		<error-tag>invalid-value</error-tag>
		<error-severity>warning</error-severity>
		<error-message>second error</error-message>
	</rpc-error>`)

	reply := &netconf.RPCReply{Body: body}
	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	require.Len(t, errs, 2, "should return exactly 2 errors")

	assert.Equal(t, "protocol", errs[0].Type)
	assert.Equal(t, "unknown-element", errs[0].Tag)

	assert.Equal(t, "application", errs[1].Type)
	assert.Equal(t, "warning", errs[1].Severity)
	assert.Equal(t, "second error", errs[1].Message)
}

// TestParseRPCErrors_OkReply verifies that an RPCReply with an empty Body
// returns a nil slice with no error — this is the normal success case.
func TestParseRPCErrors_OkReply(t *testing.T) {
	t.Parallel()
	reply := &netconf.RPCReply{
		Ok:   &struct{}{},
		Body: nil,
	}

	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	assert.Nil(t, errs, "ok reply with no body should return nil slice")
}

// TestParseRPCErrors_ComplexErrorInfo verifies that the Info field captures the
// raw inner XML of arbitrary <error-info> children.
func TestParseRPCErrors_ComplexErrorInfo(t *testing.T) {
	t.Parallel()
	body := []byte(`<rpc-error>
		<error-type>application</error-type>
		<error-tag>data-exists</error-tag>
		<error-severity>error</error-severity>
		<error-info>
			<bad-element>interface</bad-element>
			<bad-namespace>urn:example:ns</bad-namespace>
		</error-info>
	</rpc-error>`)

	reply := &netconf.RPCReply{Body: body}
	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	require.Len(t, errs, 1)

	// Info captures the raw inner XML bytes of the <rpc-error> element.
	// It includes both required fields and the <error-info> subtree.
	infoStr := string(errs[0].Info)
	assert.Contains(t, infoStr, "bad-element", "Info must contain error-info child elements")
	assert.Contains(t, infoStr, "interface", "Info must contain error-info text content")
}

// TestParseRPCErrors_EmptyBody confirms that a Body with only whitespace is
// treated the same as a nil Body and returns (nil, nil).
func TestParseRPCErrors_EmptyBody(t *testing.T) {
	t.Parallel()
	reply := &netconf.RPCReply{Body: []byte("  \n  ")}

	// The wrapper is "<wrapper>  \n  </wrapper>" — valid XML, no rpc-error children.
	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	assert.Nil(t, errs, "whitespace-only body should return nil slice")
}

// TestRPCError_ImplementsErrorInterface confirms the type assertion at compile
// time — RPCError must satisfy the built-in error interface.
func TestRPCError_ImplementsErrorInterface(t *testing.T) {
	t.Parallel()
	var _ error = netconf.RPCError{}
	// If the above compiles, the interface is satisfied.
}

// TestRPCError_XMLElementName verifies that marshaled RPCError uses the
// element name <rpc-error> as required by RFC 6241 §4.3.
func TestRPCError_XMLElementName(t *testing.T) {
	t.Parallel()
	e := netconf.RPCError{Type: "rpc", Tag: "bad-attribute", Severity: "error"}
	data, err := xml.Marshal(e)
	require.NoError(t, err)

	xmlStr := string(data)
	assert.True(t, strings.HasPrefix(xmlStr, "<rpc-error>"),
		"marshaled RPCError must start with <rpc-error>, got: %s", xmlStr)
	assert.True(t, strings.HasSuffix(xmlStr, "</rpc-error>"),
		"marshaled RPCError must end with </rpc-error>, got: %s", xmlStr)
}
