package repl

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// prettyXML indents XML bytes for human-readable display.
//
// It handles the three shapes that RPCReply.Body can take:
//
//   - Empty / nil: returns "".
//   - Single-root XML: indented directly via xml.Encoder with Indent set.
//   - Multi-sibling innerxml (e.g. multiple <rpc-error> siblings, or
//     a <data> element followed by a text node): wrapped in a synthetic
//     <wrapper>…</wrapper> root before indenting, then the
//     wrapper element and its tags are stripped from the output.
//
// If both attempts fail (malformed XML), the raw bytes are returned as-is
// so output is never silently lost.
func prettyXML(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	// Attempt 1: direct indent — works for any single-root XML document.
	if out, ok := indentXML(b); ok {
		return strings.TrimSpace(out)
	}

	// Attempt 2: wrap in a synthetic root and indent — handles multi-sibling
	// innerxml bodies (e.g. RPCReply.Body with multiple <rpc-error> elements).
	wrapped := append([]byte("<wrapper>"), append(b, []byte("</wrapper>")...)...)
	if out, ok := indentXML(wrapped); ok {
		result := unwrapIndented(out)
		return strings.TrimSpace(result)
	}

	// Fallback: return raw bytes unchanged — never panic, never silently drop.
	return string(b)
}

// indentXML uses encoding/xml to re-encode src with 2-space indentation.
// Returns the indented string and true on success; "", false on any error.
// Only returns ok=true if the entire input was consumed successfully.
func indentXML(src []byte) (string, bool) {
	var buf bytes.Buffer
	dec := xml.NewDecoder(bytes.NewReader(src))
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")

	var decodeErr error
	for {
		var tok xml.Token
		tok, decodeErr = dec.Token()
		if decodeErr != nil {
			// io.EOF is the expected end-of-input for valid XML.
			break
		}
		if encErr := enc.EncodeToken(tok); encErr != nil {
			return "", false
		}
	}
	if decodeErr != io.EOF {
		// Decode stopped with an error other than EOF — malformed XML.
		return "", false
	}
	if err := enc.Flush(); err != nil {
		return "", false
	}
	return buf.String(), buf.Len() > 0
}

// unwrapIndented removes the first and last lines (the synthetic <wrapper>
// tags) from an indented XML string and strips one level of leading
// indentation ("  ") from each remaining line.
func unwrapIndented(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= 2 {
		return ""
	}
	inner := lines[1 : len(lines)-1]
	for i, line := range inner {
		if strings.HasPrefix(line, "  ") {
			inner[i] = line[2:]
		}
	}
	return strings.Join(inner, "\n")
}

// PrintXML writes XML body to w, indenting it for readability unless raw is true.
//
// When raw is true, the bytes are written verbatim (useful for piping output
// to other tools or when the caller needs exact bytes).
//
// When raw is false, prettyXML is called to indent the output.
// If the input is empty, nothing is written.
func PrintXML(w io.Writer, body []byte, raw bool) {
	if len(body) == 0 {
		return
	}
	if raw {
		fmt.Fprintf(w, "%s\n", body)
		return
	}
	out := prettyXML(body)
	if out != "" {
		fmt.Fprintf(w, "%s\n", out)
	}
}
