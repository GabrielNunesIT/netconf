// Package transport provides framing and transport implementations for the
// NETCONF protocol.
//
// Framing is the lowest layer of NETCONF message exchange. This file
// implements two framing modes required by RFC 6242:
//
//   - End-of-message (EOM) framing: base:1.0. Each complete NETCONF message
//     is terminated by the sentinel string "]]>]]>". Used before and during
//     the hello exchange.
//   - Chunked framing: base:1.1 (RFC 6242 §4.2). Messages are encoded as one
//     or more chunks prefixed by "\n#<size>\n" and terminated by "\n##\n".
//     Used after both peers have advertised base:1.1 in their hello messages.
//
// A Framer wraps any io.ReadWriter (a net.Conn, an io.Pipe pair, an SSH
// channel, etc.) and exposes MsgReader/MsgWriter matching the Transport
// interface contract. Callers never see the framing bytes; they read and write
// complete, unframed NETCONF messages.
//
// Observability: every non-nil error returned by MsgReader or MsgWriter
// includes descriptive context (e.g. "eom: read delimiter", "chunked: chunk
// header", "chunked: chunk size 0 is invalid"). Callers log these before
// tearing down the session. No error is silently swallowed.
package transport

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// eomDelimiter is the end-of-message sentinel for base:1.0 framing.
const eomDelimiter = "]]>]]>"

// maxChunkSize is the maximum legal chunk data size per RFC 6242 §4.2:
// chunk-size = 1*DIGIT, where the value fits in uint32.
const maxChunkSize = 4294967295 // max uint32

// framingMode distinguishes the two supported framing modes.
type framingMode int

const (
	modeEOM     framingMode = iota // base:1.0 end-of-message framing
	modeChunked                    // base:1.1 chunked framing
)

// Framer implements the Transport interface, adding Upgrade() to satisfy the
// Upgrader interface. It wraps an io.ReadWriter and manages all framing.
//
// Concurrent MsgReader or concurrent MsgWriter calls are not safe; the caller
// (Session) serialises access per direction.
type Framer struct {
	mu   sync.Mutex  // protects mode only; read/write are single-goroutine
	mode framingMode // current framing mode

	rw  io.ReadWriter     // underlying byte stream
	br  *bufio.Reader     // buffered reader over rw (read side)
}

// NewFramer creates a Framer that starts in EOM mode (base:1.0).
// The underlying ReadWriter must remain valid for the lifetime of the Framer.
func NewFramer(rw io.ReadWriter) *Framer {
	return &Framer{
		mode: modeEOM,
		rw:   rw,
		br:   bufio.NewReaderSize(rw, 65536),
	}
}

// Upgrade switches the Framer from EOM framing to chunked framing.
// It must be called at most once, after the hello exchange. Calling Upgrade on
// a Framer already in chunked mode panics.
func (f *Framer) Upgrade() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mode == modeChunked {
		panic("transport.Framer.Upgrade: already in chunked mode")
	}
	f.mode = modeChunked
}

// currentMode returns the current framing mode safely.
func (f *Framer) currentMode() framingMode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

// ── MsgReader ────────────────────────────────────────────────────────────────

// MsgReader returns a ReadCloser that delivers exactly one complete NETCONF
// message, stripped of framing bytes. The caller must drain and Close it
// before calling MsgReader again.
func (f *Framer) MsgReader() (io.ReadCloser, error) {
	switch f.currentMode() {
	case modeEOM:
		return f.eomReader()
	case modeChunked:
		return f.chunkedReader()
	default:
		return nil, fmt.Errorf("framer: unknown framing mode %d", f.currentMode())
	}
}

// eomReader reads from the buffered stream until the "]]>]]>" delimiter is
// found, then returns the message body (excluding the delimiter) as a
// ReadCloser backed by a bytes.Buffer.
//
// The delimiter may span multiple underlying reads; bufio.Reader handles
// the buffering so we scan the accumulated bytes correctly.
func (f *Framer) eomReader() (io.ReadCloser, error) {
	var buf bytes.Buffer
	delim := []byte(eomDelimiter)

	for {
		// ReadString reads until '\x3e' ('>') which is the last byte of
		// ]]>]]>. We read one byte at a time conceptually, but bufio
		// makes this efficient.
		b, err := f.br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && buf.Len() > 0 {
				return nil, fmt.Errorf("eom: unexpected EOF before ]]>]]> delimiter")
			}
			return nil, fmt.Errorf("eom: read: %w", err)
		}
		buf.WriteByte(b)

		// Check if the tail of buf matches the delimiter.
		if buf.Len() >= len(delim) {
			tail := buf.Bytes()[buf.Len()-len(delim):]
			if bytes.Equal(tail, delim) {
				// Strip the delimiter and return the message body.
				msg := make([]byte, buf.Len()-len(delim))
				copy(msg, buf.Bytes())
				return io.NopCloser(bytes.NewReader(msg)), nil
			}
		}
	}
}

// chunkedReader reads RFC 6242 §4.2 chunked framing:
//
//	chunk = "\n#" chunk-size "\n" chunk-data
//	chunk-end = "\n##\n"
//
// It accumulates all chunk data until the end-of-chunks marker, then returns
// the concatenated message body as a ReadCloser.
func (f *Framer) chunkedReader() (io.ReadCloser, error) {
	var msg bytes.Buffer

	for {
		// Read the chunk header line, which must be "\n#<digits>\n" or
		// "\n##\n" for end-of-chunks.
		// We expect the first character to be '\n'.
		nl, err := f.br.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("chunked: read chunk header start: %w", err)
		}
		if nl != '\n' {
			return nil, fmt.Errorf("chunked: expected '\\n' at chunk start, got %q", nl)
		}

		// Read the '#' marker.
		hash, err := f.br.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("chunked: read '#' marker: %w", err)
		}
		if hash != '#' {
			return nil, fmt.Errorf("chunked: expected '#' after '\\n', got %q", hash)
		}

		// Peek to decide: end-of-chunks ("\n##\n") or chunk size.
		next, err := f.br.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("chunked: read after '#': %w", err)
		}

		if next == '#' {
			// End-of-chunks: consume the trailing '\n'.
			trailingNL, err := f.br.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("chunked: read trailing '\\n' after '##': %w", err)
			}
			if trailingNL != '\n' {
				return nil, fmt.Errorf("chunked: expected '\\n' after '\\n##', got %q", trailingNL)
			}
			// Return accumulated message.
			return io.NopCloser(bytes.NewReader(msg.Bytes())), nil
		}

		// Parse chunk size: next is the first digit; read the rest until '\n'.
		if next < '0' || next > '9' {
			return nil, fmt.Errorf("chunked: chunk header: expected digit after '#', got %q", next)
		}
		sizeStr := string(next)
		for {
			c, err := f.br.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("chunked: read chunk size: %w", err)
			}
			if c == '\n' {
				break
			}
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("chunked: chunk size contains non-digit %q", c)
			}
			sizeStr += string(c)
		}

		// Validate the chunk size.
		size64, err := strconv.ParseUint(sizeStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("chunked: chunk size %q is not a valid uint: %w", sizeStr, err)
		}
		if size64 == 0 {
			return nil, fmt.Errorf("chunked: chunk size 0 is invalid (RFC 6242 §4.2 requires size ≥ 1)")
		}
		if size64 > maxChunkSize {
			return nil, fmt.Errorf("chunked: chunk size %d exceeds maximum %d", size64, maxChunkSize)
		}

		// Read exactly size64 bytes of chunk data.
		chunkData := make([]byte, size64)
		if _, err := io.ReadFull(f.br, chunkData); err != nil {
			return nil, fmt.Errorf("chunked: read chunk data (%d bytes): %w", size64, err)
		}
		msg.Write(chunkData)
	}
}

// ── MsgWriter ────────────────────────────────────────────────────────────────

// MsgWriter returns a WriteCloser. The caller writes the complete message body
// and then calls Close to commit (frame and flush) it to the peer.
func (f *Framer) MsgWriter() (io.WriteCloser, error) {
	switch f.currentMode() {
	case modeEOM:
		return &eomWriter{w: f.rw}, nil
	case modeChunked:
		return &chunkedWriter{w: f.rw}, nil
	default:
		return nil, fmt.Errorf("framer: unknown framing mode %d", f.currentMode())
	}
}

// eomWriter buffers the message body. On Close it writes body + "]]>]]>" to
// the underlying writer.
type eomWriter struct {
	w   io.Writer
	buf bytes.Buffer
}

func (w *eomWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

// Close frames the accumulated body with the EOM delimiter and flushes it.
func (w *eomWriter) Close() error {
	w.buf.WriteString(eomDelimiter)
	_, err := w.w.Write(w.buf.Bytes())
	if err != nil {
		return fmt.Errorf("eom: write message+delimiter: %w", err)
	}
	return nil
}

// chunkedWriter buffers the message body. On Close it encodes and writes the
// message as a single chunk followed by the end-of-chunks marker.
//
// RFC 6242 §4.2 grammar (simplified for single-chunk messages):
//
//	"\n#" chunk-size "\n" chunk-data "\n##\n"
//
// The chunk-size must be > 0. If the message body is empty, we send
// "\n##\n" (end-of-chunks only, zero chunks), which is technically valid
// per the grammar (zero occurrences of chunk is allowed by *OCTET rule).
// In practice NETCONF messages are never empty, but we handle it gracefully.
type chunkedWriter struct {
	w   io.Writer
	buf bytes.Buffer
}

func (w *chunkedWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

// Close encodes the buffered body as chunked framing and flushes.
func (w *chunkedWriter) Close() error {
	var out strings.Builder

	if w.buf.Len() > 0 {
		size := w.buf.Len()
		out.WriteString("\n#")
		out.WriteString(strconv.Itoa(size))
		out.WriteByte('\n')
		out.Write(w.buf.Bytes())
	}
	// End-of-chunks marker.
	out.WriteString("\n##\n")

	_, err := io.WriteString(w.w, out.String())
	if err != nil {
		return fmt.Errorf("chunked: write framed message: %w", err)
	}
	return nil
}

// ── Framer lifecycle ──────────────────────────────────────────────────────────

// Close is a no-op on the Framer itself. The caller (LoopbackTransport, SSH
// transport, etc.) owns the underlying ReadWriter and is responsible for
// closing it.
func (f *Framer) Close() error { return nil }
