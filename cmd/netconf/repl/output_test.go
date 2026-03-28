package repl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPrettyXML_Nil verifies that nil input returns an empty string.
func TestPrettyXML_Nil(t *testing.T) {
	assert.Equal(t, "", prettyXML(nil))
}

// TestPrettyXML_Empty verifies that empty input returns an empty string.
func TestPrettyXML_Empty(t *testing.T) {
	assert.Equal(t, "", prettyXML([]byte{}))
}

// TestPrettyXML_SingleRoot verifies that a valid single-root XML document
// is indented correctly.
func TestPrettyXML_SingleRoot(t *testing.T) {
	input := []byte(`<data><interfaces><interface><name>eth0</name></interface></interfaces></data>`)

	out := prettyXML(input)
	t.Logf("prettyXML output:\n%s", out)

	assert.NotEmpty(t, out)
	assert.Contains(t, out, "<data>")
	assert.Contains(t, out, "  <interfaces>")
	assert.Contains(t, out, "    <interface>")
	assert.Contains(t, out, "      <name>eth0</name>")
	assert.Contains(t, out, "</data>")
}

// TestPrettyXML_MultiSibling verifies that multi-sibling XML (no single root)
// is handled via the synthetic wrapper and returned indented without panicking.
func TestPrettyXML_MultiSibling(t *testing.T) {
	// Simulate an RPCReply.Body with two sibling elements (e.g. two rpc-errors).
	input := []byte(`<rpc-error><error-tag>lock-denied</error-tag></rpc-error><rpc-error><error-tag>access-denied</error-tag></rpc-error>`)

	out := prettyXML(input)
	t.Logf("prettyXML multi-sibling output:\n%s", out)

	assert.NotEmpty(t, out, "multi-sibling input must produce output")
	assert.Contains(t, out, "rpc-error")
	assert.Contains(t, out, "lock-denied")
	assert.Contains(t, out, "access-denied")
	// Must not contain the synthetic wrapper element.
	assert.NotContains(t, out, "<wrapper>")
	assert.NotContains(t, out, "</wrapper>")
}

// TestPrettyXML_InvalidXML verifies that completely invalid XML falls back to
// returning the raw bytes as a string without panicking.
func TestPrettyXML_InvalidXML(t *testing.T) {
	input := []byte("not xml at all <<<>>>")

	// Must not panic.
	out := prettyXML(input)
	assert.Equal(t, string(input), out, "invalid XML must be returned as-is")
}

// TestPrettyXML_DataReplyBody verifies prettyXML on a realistic RPCReply.Body
// from a get-config response.
func TestPrettyXML_DataReplyBody(t *testing.T) {
	// This matches the innerxml shape of a typical get-config DataReply.
	input := []byte(`<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><config><interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"><interface><name>eth0</name><enabled>true</enabled></interface></interfaces></config></data>`)

	out := prettyXML(input)
	t.Logf("prettyXML data reply output:\n%s", out)

	assert.Contains(t, out, "<data")
	assert.Contains(t, out, "config")
	assert.Contains(t, out, "eth0")
	// Check indentation: inner elements should be indented.
	assert.True(t, strings.Contains(out, "  <config>") || strings.Contains(out, "  <interfaces"),
		"output should contain indented elements")
}

// TestPrettyXML_WithNamespace verifies that namespace attributes are preserved.
func TestPrettyXML_WithNamespace(t *testing.T) {
	input := []byte(`<netconf-state xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring"><capabilities><capability>urn:ietf:params:netconf:base:1.0</capability></capabilities></netconf-state>`)

	out := prettyXML(input)
	t.Logf("prettyXML namespace output:\n%s", out)

	assert.Contains(t, out, "ietf-netconf-monitoring")
	assert.Contains(t, out, "capability")
}

// TestPrintXML_Raw verifies that PrintXML with raw=true writes bytes verbatim.
func TestPrintXML_Raw(t *testing.T) {
	input := []byte(`<data><x/></data>`)
	var buf bytes.Buffer

	PrintXML(&buf, input, true)

	// Raw: must contain exactly the input bytes (plus trailing newline).
	assert.Contains(t, buf.String(), string(input))
}

// TestPrintXML_Pretty verifies that PrintXML with raw=false writes indented XML.
func TestPrintXML_Pretty(t *testing.T) {
	input := []byte(`<data><x><y>val</y></x></data>`)
	var buf bytes.Buffer

	PrintXML(&buf, input, false)

	out := buf.String()
	t.Logf("PrintXML pretty output:\n%s", out)

	assert.NotEmpty(t, out)
	assert.Contains(t, out, "<data>")
	assert.Contains(t, out, "  <x>")
}

// TestPrintXML_Empty verifies that PrintXML with empty body writes nothing.
func TestPrintXML_Empty(t *testing.T) {
	var buf bytes.Buffer
	PrintXML(&buf, nil, false)
	assert.Empty(t, buf.String())

	PrintXML(&buf, []byte{}, true)
	assert.Empty(t, buf.String())
}
