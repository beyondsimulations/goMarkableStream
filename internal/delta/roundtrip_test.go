package delta

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// decodeStream mirrors the client decoder in client/worker_stream_processing.js
// byte for byte. Any wire-format change that breaks the browser breaks this too.
type decodeStream struct {
	t     *testing.T
	dec   *zstd.Decoder
	frame []byte // reconstructed framebuffer, as the client's previousFrame
	types []byte // frame type of each decoded frame, in order
}

func newDecodeStream(t *testing.T, frameSize int) *decodeStream {
	t.Helper()
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	return &decodeStream{t: t, dec: dec, frame: make([]byte, frameSize)}
}

// feed consumes every complete frame in buf and applies it.
func (d *decodeStream) feed(buf []byte) {
	d.t.Helper()
	for len(buf) >= 4 {
		frameType := buf[0]
		payloadLen := int(buf[1]) | int(buf[2])<<8 | int(buf[3])<<16
		if len(buf) < 4+payloadLen {
			d.t.Fatalf("truncated frame: have %d bytes, need %d", len(buf), 4+payloadLen)
		}
		payload := buf[4 : 4+payloadLen]
		buf = buf[4+payloadLen:]
		d.types = append(d.types, frameType)

		switch frameType {
		case FrameTypeFullZstd:
			full, err := d.dec.DecodeAll(payload, nil)
			if err != nil {
				d.t.Fatalf("full frame inflate: %v", err)
			}
			if len(full) != len(d.frame) {
				d.t.Fatalf("full frame size %d, want %d", len(full), len(d.frame))
			}
			copy(d.frame, full)
		case FrameTypeDelta:
			d.applyRuns(payload)
		case FrameTypeDeltaZstd:
			runs, err := d.dec.DecodeAll(payload, nil)
			if err != nil {
				d.t.Fatalf("delta inflate: %v", err)
			}
			d.applyRuns(runs)
		default:
			d.t.Fatalf("unknown frame type 0x%02x", frameType)
		}
	}
	if len(buf) != 0 {
		d.t.Fatalf("%d trailing bytes", len(buf))
	}
}

func (d *decodeStream) applyRuns(payload []byte) {
	d.t.Helper()
	pos, frameOffset := 0, 0
	for pos < len(payload) {
		lengthByte := payload[pos]
		var runLength, relativeOffset, dataStart int

		if lengthByte&0x80 == 0 {
			if pos+3 > len(payload) {
				d.t.Fatal("truncated short run header")
			}
			runLength = int(lengthByte)
			relativeOffset = int(binary.LittleEndian.Uint16(payload[pos+1 : pos+3]))
			dataStart = pos + 3
		} else {
			if pos+5 > len(payload) {
				d.t.Fatal("truncated long run header")
			}
			runLength = int(lengthByte&0x7F)<<8 | int(payload[pos+1])
			relativeOffset = int(payload[pos+2]) | int(payload[pos+3])<<8 | int(payload[pos+4])<<16
			dataStart = pos + 5
		}

		pos = dataStart + runLength*4
		if pos > len(payload) {
			d.t.Fatal("delta run exceeds payload")
		}

		frameOffset += relativeOffset
		if frameOffset+runLength*4 > len(d.frame) {
			d.t.Fatal("delta run exceeds frame bounds")
		}
		copy(d.frame[frameOffset:], payload[dataStart:pos])
		frameOffset += runLength * 4
	}
}

// drawStroke paints a horizontal run of dark pixels, the shape a pen stroke
// actually produces in the framebuffer.
func drawStroke(frame []byte, width, row, col, length int) {
	for i := range length {
		off := (row*width + col + i) * 4
		if off+4 > len(frame) {
			return
		}
		frame[off], frame[off+1], frame[off+2], frame[off+3] = 0x20, 0x20, 0x20, 0xFF
	}
}

// newPageFrame builds a realistic mostly-white page.
func newPageFrame(size int) []byte {
	f := make([]byte, size)
	for i := range f {
		f[i] = 0xFF
	}
	return f
}

// TestRoundTrip_ProgressiveDrawing walks a realistic writing session through the
// encoder and reconstructs it with the client's decoding rules, asserting the
// client's framebuffer stays pixel-identical to the server's at every step.
func TestRoundTrip_ProgressiveDrawing(t *testing.T) {
	const (
		width  = 1872
		height = 1404
		size   = width * height * 4
	)

	enc := NewEncoder(DefaultThreshold)
	dst := newDecodeStream(t, size)

	frame := newPageFrame(size)

	for step := range 40 {
		// Each step adds a few strokes, as writing does.
		for s := range 3 {
			drawStroke(frame, width, 100+step*7+s, 200+step*3, 400)
		}

		var buf bytes.Buffer
		if err := enc.Encode(frame, &buf); err != nil {
			t.Fatalf("step %d: encode: %v", step, err)
		}
		dst.feed(buf.Bytes())

		if !bytes.Equal(dst.frame, frame) {
			t.Fatalf("step %d: decoded framebuffer differs from source", step)
		}
	}

	// The point of the change: this workload must ride on compressed deltas,
	// not on full frames and not on raw deltas.
	var compressedDeltas int
	for _, ft := range dst.types {
		if ft == FrameTypeDeltaZstd {
			compressedDeltas++
		}
	}
	if compressedDeltas == 0 {
		t.Fatalf("expected compressed delta frames, got types %v", dst.types)
	}
	t.Logf("frames=%d compressed_deltas=%d", len(dst.types), compressedDeltas)
}

// TestRoundTrip_PageTurn covers the large-change path, where the encoder is
// expected to fall back to a full frame, and the recovery afterwards.
func TestRoundTrip_PageTurn(t *testing.T) {
	const (
		width  = 1872
		height = 1404
		size   = width * height * 4
	)

	enc := NewEncoder(DefaultThreshold)
	dst := newDecodeStream(t, size)

	frame := newPageFrame(size)
	var buf bytes.Buffer
	if err := enc.Encode(frame, &buf); err != nil {
		t.Fatal(err)
	}
	dst.feed(buf.Bytes())

	// Turn the page: invert everything.
	for i := range frame {
		frame[i] = ^frame[i]
	}
	buf.Reset()
	if err := enc.Encode(frame, &buf); err != nil {
		t.Fatal(err)
	}
	dst.feed(buf.Bytes())
	if !bytes.Equal(dst.frame, frame) {
		t.Fatal("page turn: decoded framebuffer differs from source")
	}

	// Then resume writing on the new page.
	for step := range 5 {
		drawStroke(frame, width, 300+step*10, 150, 600)
		buf.Reset()
		if err := enc.Encode(frame, &buf); err != nil {
			t.Fatal(err)
		}
		dst.feed(buf.Bytes())
		if !bytes.Equal(dst.frame, frame) {
			t.Fatalf("post-page-turn step %d: decoded framebuffer differs", step)
		}
	}
}

// TestRoundTrip_UnchangedFrames checks the idle keepalive path still decodes.
func TestRoundTrip_UnchangedFrames(t *testing.T) {
	const size = 1872 * 1404 * 4

	enc := NewEncoder(DefaultThreshold)
	dst := newDecodeStream(t, size)
	frame := newPageFrame(size)

	for i := range 5 {
		var buf bytes.Buffer
		if err := enc.Encode(frame, &buf); err != nil {
			t.Fatal(err)
		}
		dst.feed(buf.Bytes())
		if !bytes.Equal(dst.frame, frame) {
			t.Fatalf("iteration %d: decoded framebuffer differs", i)
		}
	}
}

// TestDeltaCompressionRatio documents the bandwidth effect of compressing the
// run stream, which is the whole reason FrameTypeDeltaZstd exists.
func TestDeltaCompressionRatio(t *testing.T) {
	const (
		width = 1872
		size  = width * 1404 * 4
	)

	enc := NewEncoder(DefaultThreshold)
	frame := newPageFrame(size)

	var buf bytes.Buffer
	if err := enc.Encode(frame, &buf); err != nil {
		t.Fatal(err)
	}

	// A dense but sub-threshold change (~11% of the frame).
	for row := 100; row < 300; row++ {
		drawStroke(frame, width, row, 100, 1400)
	}

	buf.Reset()
	if err := enc.Encode(frame, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if out[0] != FrameTypeDeltaZstd {
		t.Fatalf("expected compressed delta (0x%02x), got 0x%02x", FrameTypeDeltaZstd, out[0])
	}

	// Same runs, uncompressed, for comparison.
	raw := enc.calculateDeltaSize(enc.runsBuf) + 4
	t.Logf("delta on the wire: %d bytes raw -> %d bytes compressed (%.1fx)",
		raw, len(out), float64(raw)/float64(len(out)))

	if len(out) >= raw {
		t.Errorf("compressed delta (%d) should be smaller than raw (%d)", len(out), raw)
	}
}

// TestRoundTrip_WideBand covers a change that spans whole rows for longer than
// a long run can describe (32767 pixels, about 20 rows of the Paper Pro): the
// tablet's sleep banner, a menu, a zoom. The encoder must split such a run, or
// the client reads pixel data as run headers and paints it in the wrong place.
func TestRoundTrip_WideBand(t *testing.T) {
	const (
		width  = 1632
		height = 2154
		size   = width * height * 4
	)

	for _, rows := range []int{19, 21, 60, 200} {
		enc := NewEncoder(DefaultThreshold)
		dst := newDecodeStream(t, size)

		frame := newPageFrame(size)
		var buf bytes.Buffer
		if err := enc.Encode(frame, &buf); err != nil {
			t.Fatalf("rows=%d: first frame: %v", rows, err)
		}
		dst.feed(buf.Bytes())

		// A dark bar across the full width, then gone again.
		for r := 40; r < 40+rows; r++ {
			drawStroke(frame, width, r, 0, width)
		}
		for step, f := range [][]byte{frame, newPageFrame(size)} {
			buf.Reset()
			if err := enc.Encode(f, &buf); err != nil {
				t.Fatalf("rows=%d step %d: encode: %v", rows, step, err)
			}
			dst.feed(buf.Bytes())
			if !bytes.Equal(dst.frame, f) {
				t.Fatalf("rows=%d step %d: decoded framebuffer differs from source (types %v)", rows, step, dst.types)
			}
		}
		if dst.types[1] == FrameTypeFullZstd {
			t.Fatalf("rows=%d: expected a delta for the bar, got a full frame", rows)
		}
	}
}
