// Package delta provides delta compression for BGRA framebuffer streaming.
// It encodes only changed pixels between frames, significantly reducing bandwidth
// for e-ink usage where typically only 1-5% of pixels change between frames.
package delta

import (
	"encoding/binary"
	"io"
	"sync"
	"unsafe"

	"github.com/cespare/xxhash/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/owulveryck/goMarkableStream/internal/debug"
	"github.com/owulveryck/goMarkableStream/internal/trace"
)

// newFullFrameEncoder builds an encoder for whole-framebuffer compression.
//
// Concurrency is pinned to 1 on purpose: by default zstd fans out to GOMAXPROCS
// goroutines and allocates per-goroutine state, which costs ~20MB of retained
// buffers per encoder. On a dual-core device that fan-out also steals the core
// the framebuffer reader needs, so it buys nothing.
func newFullFrameEncoder() *zstd.Encoder {
	enc, _ := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(1),
	)
	return enc
}

// newDeltaEncoder builds an encoder for delta run streams. Delta payloads are
// small and highly local (long repeats of untouched background), so a bounded
// window keeps nearly all of the compression ratio while shrinking the retained
// encoder state by roughly 4x compared to the full-frame encoder.
func newDeltaEncoder() *zstd.Encoder {
	enc, _ := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(deltaWindowSize),
	)
	return enc
}

// zstdEncoderPool reuses zstd encoders to avoid allocation overhead.
// Full frames and deltas use separate pools so that an encoder sized for a 10MB
// framebuffer is never recycled for a few-KB delta.
var (
	zstdEncoderPool      = sync.Pool{New: func() any { return newFullFrameEncoder() }}
	zstdDeltaEncoderPool = sync.Pool{New: func() any { return newDeltaEncoder() }}
)

// ResetEncoderPool replaces the zstd encoder pools with fresh ones,
// allowing old encoders to be garbage collected.
func ResetEncoderPool() {
	zstdEncoderPool = sync.Pool{New: func() any { return newFullFrameEncoder() }}
	zstdDeltaEncoderPool = sync.Pool{New: func() any { return newDeltaEncoder() }}
}

const (
	// Frame type constants for wire protocol
	FrameTypeFull           = 0x00 // Deprecated: uncompressed full frame
	FrameTypeDelta          = 0x01
	FrameTypeFullCompressed = 0x02 // Gzip-compressed full frame (legacy)
	FrameTypeFullZstd       = 0x03 // Zstd-compressed full frame
	FrameTypeDeltaZstd      = 0x04 // Zstd-compressed delta frame

	// DefaultThreshold is the default change ratio above which a full frame is sent
	DefaultThreshold = 0.30

	// minDeltaCompressSize is the delta payload size below which compression is
	// skipped: zstd framing overhead plus CPU cost outweighs the saving, and such
	// frames are negligible on the wire anyway.
	minDeltaCompressSize = 1024

	// Falling back to a full frame means pushing the whole framebuffer through
	// zstd (~10MB, i.e. ~10x the CPU of a typical delta on a Cortex-A9), so it is
	// only worth it when the saving is substantial in both relative and absolute
	// terms. A delta must be at least fullFrameAdvantage times bigger than the
	// last full frame *and* above minFullSwitchSize on the wire before we switch.
	fullFrameAdvantage = 2
	minFullSwitchSize  = 32 * 1024

	// deltaWindowSize bounds the zstd match window used for delta payloads.
	deltaWindowSize = 256 * 1024

	// Maximum values for short run encoding
	maxShortOffset = 0xFFFF // 64KB - 1
	maxShortLength = 127    // 7 bits

	// maxLongLength is the most pixels one long run can carry (15 bits): about
	// 20 rows of the Paper Pro. A longer change, such as a banner or menu
	// across the full width, goes out as several runs (see appendLongRun).
	// The 24-bit offset needs no such care: a frame is below 16 MiB, as the
	// 24-bit payload length in the frame header already requires.
	maxLongLength = 0x7FFF

	// bytesPerPixel defines the pixel format for delta encoding.
	// Hardcoded to 4 for BGRA32 format (matches remarkable.BytesPerPixelBGRA).
	// All current reMarkable devices use BGRA in streaming mode:
	// - RM2 firmware 3.24+ uses BGRA
	// - RMPP uses BGRA
	// - RM2 legacy firmware uses gray16 but is converted to BGRA server-side
	bytesPerPixel = 4
)

// Encoder holds the state for delta encoding between frames.
type Encoder struct {
	prevFrame     []byte
	prevFrameHash uint64 // XXHash64 of previous frame for fast equality check
	threshold     float64
	hasPrev       bool
	lastChanged   bool // true when previous frame had changes (hybrid hash strategy)
	// Reusable buffers to avoid allocations
	headerBuf     [5]byte     // Max header size (long run: 5 bytes)
	frameHeader   [4]byte     // Frame header buffer
	runsBuf       []changeRun // Reusable runs slice
	compressedBuf []byte      // Reusable buffer for ZSTD compression output
	maskBuf       []byte      // Reusable buffer for block comparison mask
	writeBuf      []byte      // Reusable buffer for coalesced delta frame writes
	deltaZstdBuf  []byte      // Reusable buffer for ZSTD-compressed delta frames
	prevChecksum  [16]byte    // XOR-fold checksum of previous frame (ARM32 idle detection)

	// lastFullSize is the total wire size (header included) of the most recent
	// full frame sent. It is the reference used to decide whether a delta is
	// actually cheaper than resending the whole screen, without having to
	// compress 10MB on every frame just to answer the question.
	lastFullSize int
}

// NewEncoder creates a new delta encoder with the given threshold.
// Threshold is the change ratio (0.0-1.0) above which a full frame is sent.
func NewEncoder(threshold float64) *Encoder {
	if threshold <= 0 || threshold > 1.0 {
		threshold = DefaultThreshold
	}
	return &Encoder{
		threshold: threshold,
	}
}

// changeRun represents a contiguous run of changed pixels.
// Uses slice reference to avoid copying data.
type changeRun struct {
	offset int    // byte offset from previous run end (or frame start)
	length int    // number of changed pixels
	data   []byte // slice into current frame (not a copy)
}

// Encode writes the current frame to w, using delta encoding if beneficial.
// Returns nil on success, or an error if writing fails.
func (e *Encoder) Encode(current []byte, w io.Writer) error {
	_, err := e.EncodeWithSize(current, w)
	return err
}

// EncodeWithSize writes the current frame to w, using delta encoding if beneficial.
// Returns the number of bytes written and nil on success, or 0 and an error if writing fails.
func (e *Encoder) EncodeWithSize(current []byte, w io.Writer) (n int, err error) {
	span := trace.BeginSpan("delta_encode")
	defer func() {
		frameType := "delta"
		if !e.hasPrev {
			frameType = "full"
		}
		trace.EndSpan(span, map[string]any{
			"bytes_written": n,
			"frame_type":    frameType,
		})
	}()

	frameSize := len(current)

	// First frame or no previous: send full frame
	if !e.hasPrev || len(e.prevFrame) != frameSize {
		e.prevFrame = make([]byte, frameSize)
		copy(e.prevFrame, current)
		if useHashEarlyExit {
			e.prevFrameHash = xxhash.Sum64(current)
		}
		e.hasPrev = true
		debug.Log("Delta: first frame, sending full")
		return e.writeFullFrame(current, w)
	}

	// Compare frames and copy current → prev in a single pass.
	// This merges two memory scans into one, reducing bandwidth pressure.
	runs := e.compareAndCopyFrames(current)

	// Calculate total changed bytes
	changedBytes := 0
	for _, run := range runs {
		changedBytes += len(run.data)
	}

	// No changes - send empty delta frame (copy already skipped via hash early exit)
	if changedBytes == 0 {
		debug.Log("Delta: no changes, sending empty delta")
		return e.writeDeltaFrame(runs, 0, w)
	}

	// Calculate change ratio
	changeRatio := float64(changedBytes) / float64(frameSize)

	// Cheap guard before doing any work: a delta covering more than `threshold`
	// of the screen is expensive to assemble and compress, and is very unlikely
	// to beat a compressed full frame.
	// prevFrame was already updated during compareAndCopyFrames.
	if changeRatio > e.threshold {
		debug.Log("Delta: changeRatio=%.2f%%, runs=%d, sending full", changeRatio*100, len(runs))
		return e.writeFullFrame(current, w)
	}

	// Assemble the delta frame, then compress the run stream. Delta payloads are
	// BGRA stroke data (long runs of identical background) and compress by
	// roughly 25x on real content, so this dominates the bandwidth budget.
	deltaSize := e.calculateDeltaSize(runs)
	frame := e.assembleDeltaFrame(runs, deltaSize)
	if compressed := e.compressDeltaFrame(frame[4:]); compressed != nil {
		frame = compressed
	}

	// Now that the true wire size is known, compare it against the alternative:
	// resending the whole screen zstd-compressed. Comparing against the
	// *uncompressed* frame size (as a naive check would) is meaningless — a full
	// frame compresses ~33x, so a large raw delta can easily be several times
	// bigger than simply starting over.
	if e.lastFullSize > 0 && len(frame) > minFullSwitchSize && len(frame) >= fullFrameAdvantage*e.lastFullSize {
		debug.Log("Delta: changeRatio=%.2f%%, delta=%d >= full=%d, sending full",
			changeRatio*100, len(frame), e.lastFullSize)
		return e.writeFullFrame(current, w)
	}

	debug.Log("Delta: changeRatio=%.2f%%, runs=%d, sending delta (%d bytes)",
		changeRatio*100, len(runs), len(frame))
	return w.Write(frame)
}

// compareAndCopyFrames compares current frame with previous, copies current → prev
// during the same pass, and returns change runs.
// This merges two memory-bandwidth-heavy operations (compare + copy) into one,
// reducing total memory traffic by ~25% for changed frames.
//
// Uses a hybrid hash strategy: hash is only computed when the previous frame was
// unchanged (idle mode). During active drawing, hash is skipped entirely (~41% CPU
// savings for changed frames). When drawing stops and compare+copy finds no changes,
// hash is recomputed once to re-enable fast early exit for subsequent idle frames.
//
// Reuses internal buffer to minimize allocations.
// The returned slice references data in 'current', so current must not be modified
// until after the runs are processed.
func (e *Encoder) compareAndCopyFrames(current []byte) []changeRun {
	span := trace.BeginSpan("delta_compare")
	earlyExit := false
	hashSkipped := false
	defer func() {
		changedBytes := 0
		for _, run := range e.runsBuf {
			changedBytes += len(run.data)
		}
		changeRatio := 0.0
		if len(current) > 0 {
			changeRatio = float64(changedBytes) / float64(len(current))
		}
		trace.EndSpan(span, map[string]any{
			"frame_size":    len(current),
			"runs":          len(e.runsBuf),
			"changed_bytes": changedBytes,
			"change_ratio":  changeRatio,
			"early_exit":    earlyExit,
			"hash_skipped":  hashSkipped,
		})
	}()

	// Reuse runs buffer, reset length but keep capacity
	e.runsBuf = e.runsBuf[:0]

	prev := e.prevFrame
	frameLen := len(current)

	// Handle empty buffers - no comparison needed
	if frameLen == 0 || len(prev) == 0 {
		return e.runsBuf
	}

	// Safety check: buffers should have same length (caller's responsibility)
	// but prevent panic if they don't
	if len(prev) != frameLen {
		return e.runsBuf
	}

	// Early exit strategy for idle frames.
	// During active drawing (lastChanged=true), skip early-exit checks and
	// go straight to compare+copy. When idle (lastChanged=false), use a
	// fast read-only scan to detect unchanged frames cheaply.
	//
	// On platforms with fast xxhash assembly (arm64, amd64), use hash-based
	// early exit. On ARM32, use NEON hasAnyChange scan instead — xxhash has
	// no ARM32 assembly, making pure-Go 64-bit multiplications ~6x slower
	// than the NEON compare+copy pass.
	nblocks := frameLen / blockSize
	remainder := frameLen - nblocks*blockSize

	if !e.lastChanged {
		if useHashEarlyExit {
			// Hash-based early exit (arm64, amd64)
			currentHash := xxhash.Sum64(current)
			if currentHash == e.prevFrameHash {
				earlyExit = true
				return e.runsBuf
			}
			e.prevFrameHash = currentHash
		} else if nblocks > 0 {
			// Single-buffer NEON checksum early exit (ARM32).
			// checksumChanged reads only current (10.5MB) instead of both
			// src+dst (21MB), halving memory bandwidth for unchanged frames.
			// Uses XOR-fold with position-dependent rotation.
			if !checksumChanged(unsafe.Pointer(&current[0]), nblocks, unsafe.Pointer(&e.prevChecksum[0])) {
				earlyExit = true
				return e.runsBuf
			}
		}
	} else {
		hashSkipped = true
	}

	// SIMD-accelerated compare+copy in 128-byte blocks.
	// On arm/arm64 this uses NEON vector instructions; on other platforms a scalar fallback.
	// The mask records which blocks contain any changed bytes.

	// Ensure mask buffer is large enough (reuse across calls)
	if cap(e.maskBuf) < nblocks {
		e.maskBuf = make([]byte, nblocks)
	}
	mask := e.maskBuf[:nblocks]

	// Compare and copy all full blocks
	compareAndCopyBlocks(unsafe.Pointer(&prev[0]), unsafe.Pointer(&current[0]), mask, nblocks)

	// Build change runs from block mask, computing relative offsets inline.
	// blockSize (128) is a multiple of bytesPerPixel (4), so block boundaries
	// are always pixel-aligned — no alignment fixup needed.
	//
	// Process the mask 8 bytes at a time via uint64 to reduce iterations
	// from nblocks to nblocks/8. The common case (all-zero word = 8 unchanged
	// blocks) is handled with a single comparison. On Cortex-A9's in-order
	// pipeline, this reduces branch misprediction overhead significantly.
	runStart := -1 // block index of current run start, or -1
	lastEnd := 0   // end of previous run in bytes (for relative offset computation)

	nwords := nblocks / 8
	maskPtr := unsafe.Pointer(&mask[0])

	for w := range nwords {
		word := *(*uint64)(unsafe.Add(maskPtr, w*8))
		if word == 0 {
			// 8 consecutive unchanged blocks — close any open run
			if runStart != -1 {
				endBlock := w * 8
				startByte := runStart * blockSize
				endByte := endBlock * blockSize
				e.runsBuf = append(e.runsBuf, changeRun{
					offset: startByte - lastEnd,
					length: (endByte - startByte) / bytesPerPixel,
					data:   current[startByte:endByte],
				})
				lastEnd = endByte
				runStart = -1
			}
		} else {
			// At least one changed block in this word — scan individual bytes
			base := w * 8
			for j := range 8 {
				i := base + j
				if mask[i] != 0 {
					if runStart == -1 {
						runStart = i
					}
				} else if runStart != -1 {
					startByte := runStart * blockSize
					endByte := i * blockSize
					e.runsBuf = append(e.runsBuf, changeRun{
						offset: startByte - lastEnd,
						length: (endByte - startByte) / bytesPerPixel,
						data:   current[startByte:endByte],
					})
					lastEnd = endByte
					runStart = -1
				}
			}
		}
	}

	// Handle remaining mask bytes (nblocks % 8)
	for i := nwords * 8; i < nblocks; i++ {
		if mask[i] != 0 {
			if runStart == -1 {
				runStart = i
			}
		} else if runStart != -1 {
			startByte := runStart * blockSize
			endByte := i * blockSize
			e.runsBuf = append(e.runsBuf, changeRun{
				offset: startByte - lastEnd,
				length: (endByte - startByte) / bytesPerPixel,
				data:   current[startByte:endByte],
			})
			lastEnd = endByte
			runStart = -1
		}
	}

	// Handle remainder bytes after last full block (compare and copy)
	remStart := nblocks * blockSize
	remChanged := false
	for i := remStart; i < frameLen; i++ {
		c := current[i]
		p := prev[i]
		prev[i] = c
		if c != p {
			remChanged = true
		}
	}

	// Finalize any open run
	if runStart != -1 {
		startByte := runStart * blockSize
		endByte := nblocks * blockSize
		if remChanged {
			endByte = frameLen
		}
		alignedEnd := ((endByte + bytesPerPixel - 1) / bytesPerPixel) * bytesPerPixel
		alignedEnd = min(alignedEnd, frameLen)
		e.runsBuf = append(e.runsBuf, changeRun{
			offset: startByte - lastEnd,
			length: (alignedEnd - startByte) / bytesPerPixel,
			data:   current[startByte:alignedEnd],
		})
	} else if remChanged && remainder > 0 {
		alignedStart := (remStart / bytesPerPixel) * bytesPerPixel
		alignedEnd := ((frameLen + bytesPerPixel - 1) / bytesPerPixel) * bytesPerPixel
		alignedEnd = min(alignedEnd, frameLen)
		e.runsBuf = append(e.runsBuf, changeRun{
			offset: alignedStart - lastEnd,
			length: (alignedEnd - alignedStart) / bytesPerPixel,
			data:   current[alignedStart:alignedEnd],
		})
	}

	// Update hybrid hash state
	if len(e.runsBuf) == 0 {
		// No changes — transition to idle mode.
		// Compute hash/checksum so the next identical frame can early-exit.
		if e.lastChanged {
			if useHashEarlyExit {
				e.prevFrameHash = xxhash.Sum64(current)
			} else if nblocks > 0 {
				// ARM32: compute and store XOR-fold checksum for idle detection.
				// The return value is ignored — we just need prevChecksum populated.
				checksumChanged(unsafe.Pointer(&current[0]), nblocks, unsafe.Pointer(&e.prevChecksum[0]))
			}
		}
		e.lastChanged = false
	} else {
		e.lastChanged = true
	}

	return e.runsBuf
}

// compareFrames compares current frame with previous and returns change runs.
// Unlike compareAndCopyFrames, it does NOT copy current → prev.
// This is used by tests and benchmarks that manage prevFrame directly.
func (e *Encoder) compareFrames(current []byte) []changeRun {
	e.runsBuf = e.runsBuf[:0]

	prev := e.prevFrame
	frameLen := len(current)

	if frameLen == 0 || len(prev) == 0 || len(prev) != frameLen {
		return e.runsBuf
	}

	currentHash := xxhash.Sum64(current)
	if e.hasPrev && currentHash == e.prevFrameHash {
		return e.runsBuf
	}
	e.prevFrameHash = currentHash

	numQwords := frameLen / 8
	var runStart int = -1
	var lastDiffEnd int
	currPtr := unsafe.Pointer(&current[0])
	prevPtr := unsafe.Pointer(&prev[0])

	for i := range numQwords {
		offset := i * 8
		currQword := *(*uint64)(unsafe.Add(currPtr, offset))
		prevQword := *(*uint64)(unsafe.Add(prevPtr, offset))

		if currQword != prevQword {
			if runStart == -1 {
				runStart = offset
			}
			lastDiffEnd = offset + 8
		} else if runStart != -1 {
			alignedStart := (runStart / bytesPerPixel) * bytesPerPixel
			alignedEnd := ((lastDiffEnd + bytesPerPixel - 1) / bytesPerPixel) * bytesPerPixel
			alignedEnd = min(alignedEnd, frameLen)
			e.runsBuf = append(e.runsBuf, changeRun{
				offset: alignedStart,
				length: (alignedEnd - alignedStart) / bytesPerPixel,
				data:   current[alignedStart:alignedEnd],
			})
			runStart = -1
		}
	}

	for i := numQwords * 8; i < frameLen; i++ {
		if current[i] != prev[i] {
			if runStart == -1 {
				runStart = i
			}
			lastDiffEnd = i + 1
		}
	}

	if runStart != -1 {
		alignedStart := (runStart / bytesPerPixel) * bytesPerPixel
		alignedEnd := ((lastDiffEnd + bytesPerPixel - 1) / bytesPerPixel) * bytesPerPixel
		alignedEnd = min(alignedEnd, frameLen)
		e.runsBuf = append(e.runsBuf, changeRun{
			offset: alignedStart,
			length: (alignedEnd - alignedStart) / bytesPerPixel,
			data:   current[alignedStart:alignedEnd],
		})
	}

	lastEnd := 0
	for i := range e.runsBuf {
		absOffset := e.runsBuf[i].offset
		e.runsBuf[i].offset = absOffset - lastEnd
		lastEnd = absOffset + len(e.runsBuf[i].data)
	}

	return e.runsBuf
}

// calculateDeltaSize calculates the size of the delta payload.
func (e *Encoder) calculateDeltaSize(runs []changeRun) int {
	size := 0
	for _, run := range runs {
		if run.offset <= maxShortOffset && run.length <= maxShortLength {
			// Short run: 1 byte length + 2 bytes offset + pixel data
			size += 1 + 2 + len(run.data)
		} else {
			// Long runs: 2 bytes length + 3 bytes offset each, + pixel data
			pieces := (run.length + maxLongLength - 1) / maxLongLength
			size += pieces*(2+3) + len(run.data)
		}
	}
	return size
}

// writeFullFrame writes a zstd-compressed full frame with header.
// Returns the total number of bytes written.
func (e *Encoder) writeFullFrame(data []byte, w io.Writer) (int, error) {
	span := trace.BeginSpan("zstd_compress")
	inputSize := len(data)

	// Compress data with zstd using pooled encoder
	enc := zstdEncoderPool.Get().(*zstd.Encoder)
	defer func() {
		enc.Reset(nil) // Clear internal buffers before returning to pool
		zstdEncoderPool.Put(enc)
	}()

	// Reuse compressed buffer (reset length, keep capacity)
	e.compressedBuf = e.compressedBuf[:0]
	e.compressedBuf = enc.EncodeAll(data, e.compressedBuf)

	compressionRatio := 0.0
	if inputSize > 0 {
		compressionRatio = float64(len(e.compressedBuf)) / float64(inputSize)
	}

	trace.EndSpan(span, map[string]any{
		"input_size":        inputSize,
		"output_size":       len(e.compressedBuf),
		"compression_ratio": compressionRatio,
	})

	// Write header with zstd compressed type
	e.frameHeader[0] = FrameTypeFullZstd
	// Payload length in 24-bit little-endian
	payloadLen := len(e.compressedBuf)
	e.frameHeader[1] = byte(payloadLen & 0xFF)
	e.frameHeader[2] = byte((payloadLen >> 8) & 0xFF)
	e.frameHeader[3] = byte((payloadLen >> 16) & 0xFF)

	// Record the wire cost of resending the whole screen, so subsequent frames
	// can tell whether a delta is actually the cheaper option.
	e.lastFullSize = 4 + payloadLen

	if _, err := w.Write(e.frameHeader[:]); err != nil {
		return 0, err
	}
	n, err := w.Write(e.compressedBuf)
	if err != nil {
		return 4, err
	}
	return 4 + n, nil
}

// writeDeltaFrame assembles an uncompressed delta frame and writes it with a
// single w.Write() call. Used for the empty-delta keepalive.
func (e *Encoder) writeDeltaFrame(runs []changeRun, payloadSize int, w io.Writer) (int, error) {
	return w.Write(e.assembleDeltaFrame(runs, payloadSize))
}

// compressDeltaFrame zstd-compresses a delta run stream and returns a complete
// frame (4-byte FrameTypeDeltaZstd header + compressed payload), or nil when
// compression is not worth it and the caller should send the payload as-is.
func (e *Encoder) compressDeltaFrame(payload []byte) []byte {
	if len(payload) < minDeltaCompressSize {
		return nil
	}

	span := trace.BeginSpan("zstd_compress_delta")

	enc := zstdDeltaEncoderPool.Get().(*zstd.Encoder)
	defer func() {
		enc.Reset(nil) // Clear internal buffers before returning to pool
		zstdDeltaEncoderPool.Put(enc)
	}()

	// Reserve the 4 header bytes up front so EncodeAll appends straight after
	// them: the frame is emitted with a single Write and no extra copy.
	e.deltaZstdBuf = append(e.deltaZstdBuf[:0], 0, 0, 0, 0)
	e.deltaZstdBuf = enc.EncodeAll(payload, e.deltaZstdBuf)

	compressedLen := len(e.deltaZstdBuf) - 4
	trace.EndSpan(span, map[string]any{
		"input_size":  len(payload),
		"output_size": compressedLen,
	})

	// Incompressible payload: sending it raw avoids the client-side inflate.
	if compressedLen >= len(payload) {
		return nil
	}

	e.deltaZstdBuf[0] = FrameTypeDeltaZstd
	e.deltaZstdBuf[1] = byte(compressedLen & 0xFF)
	e.deltaZstdBuf[2] = byte((compressedLen >> 8) & 0xFF)
	e.deltaZstdBuf[3] = byte((compressedLen >> 16) & 0xFF)
	return e.deltaZstdBuf
}

// assembleDeltaFrame builds the entire delta frame (4-byte header + all runs)
// into a single reusable buffer. This eliminates per-run write overhead
// (2N+1 calls → 1 call) which reduces syscall and interface dispatch costs,
// especially on ARM32. The returned slice is valid until the next call.
func (e *Encoder) assembleDeltaFrame(runs []changeRun, payloadSize int) []byte {
	totalSize := 4 + payloadSize

	// Reuse write buffer, grow if needed
	if cap(e.writeBuf) < totalSize {
		e.writeBuf = make([]byte, totalSize)
	}
	buf := e.writeBuf[:totalSize]

	// Frame header
	buf[0] = FrameTypeDelta
	buf[1] = byte(payloadSize & 0xFF)
	buf[2] = byte((payloadSize >> 8) & 0xFF)
	buf[3] = byte((payloadSize >> 16) & 0xFF)

	pos := 4
	for _, run := range runs {
		if run.offset <= maxShortOffset && run.length <= maxShortLength {
			// Short run: 1 byte length + 2 bytes offset LE + pixel data
			buf[pos] = byte(run.length)
			binary.LittleEndian.PutUint16(buf[pos+1:pos+3], uint16(run.offset))
			pos += 3
			copy(buf[pos:], run.data)
			pos += len(run.data)
		} else {
			pos = appendLongRun(buf, pos, run.offset, run.data)
		}
	}

	return buf[:pos]
}

// appendLongRun writes data as long runs into buf at pos and returns the new
// position. A long run holds at most maxLongLength pixels, so a longer one is
// split: the first piece carries the run's offset, the rest follow directly
// (offset 0). Writing one oversized run instead would drop the length's high
// bits, and the client would read the rest of the pixels as run headers.
func appendLongRun(buf []byte, pos, offset int, data []byte) int {
	for len(data) > 0 {
		pixels := min(len(data)/bytesPerPixel, maxLongLength)
		// Long run: 2 bytes length (0x80|high, low) + 3 bytes offset LE + pixel data
		buf[pos] = 0x80 | byte((pixels>>8)&0x7F)
		buf[pos+1] = byte(pixels & 0xFF)
		buf[pos+2] = byte(offset & 0xFF)
		buf[pos+3] = byte((offset >> 8) & 0xFF)
		buf[pos+4] = byte((offset >> 16) & 0xFF)
		pos += 5
		pos += copy(buf[pos:], data[:pixels*bytesPerPixel])
		data = data[pixels*bytesPerPixel:]
		offset = 0
	}
	return pos
}

// Reset clears the encoder state, forcing the next frame to be a full frame.
func (e *Encoder) Reset() {
	e.hasPrev = false
	e.lastChanged = false
}

// ReleaseMemory releases large buffers held by the encoder to reduce memory usage.
// After calling this, the encoder remains usable but will reallocate buffers as needed.
func (e *Encoder) ReleaseMemory() {
	e.hasPrev = false
	e.lastChanged = false
	e.prevFrame = nil
	e.runsBuf = nil
	e.compressedBuf = nil
	e.maskBuf = nil
	e.writeBuf = nil
	e.deltaZstdBuf = nil
	e.lastFullSize = 0
}
