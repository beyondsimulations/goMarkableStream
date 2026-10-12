package remarkable

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// This file holds the device-independent part of the Paper Pro framebuffer
// search, so it can be unit-tested on any architecture.
//
// Background: on the Paper Pro, xochitl keeps the composed screen image in a
// large malloc'ed buffer. glibc serves an allocation of that size with its own
// anonymous mmap, and writes a chunk header in front of it:
//
//	offset 0: prev_size (8 bytes)
//	offset 8: size      (8 bytes, low 3 bits are flags; bit 1 = IS_MMAPPED)
//
// Several such chunks can sit back to back inside one anonymous "rw-p" region
// of /proc/[pid]/maps. The screen buffer is the chunk whose size is just above
// width x height x 4.
//
// Older firmware placed that region right after the LAST /dev/dri/card0
// mapping. Firmware 3.25+ no longer does: the last card0 mapping can be
// followed by a thread-stack guard page (unreadable, hence "input/output
// error") or by an unrelated region. So instead of assuming a position, walk
// the chunk headers of every candidate region and pick the chunk by size.
//
// Without an override, a chunk in a region that directly follows a card0
// mapping is used. The region after the last card0 mapping is tried first, as
// the original algorithm did, so layouts it handled resolve to the same
// address.
//
// On the Paper Pro Move a thread stack and a malloc arena sit between the
// last card0 mapping and the buffer's region (six lines of maps, observed
// 2026-10-12), so no chunk follows card0 directly. There the buffer is
// recognised by its size instead: a chunk of exactly the screen image plus
// its header, rounded up to a page, in a region after a card0 mapping. That
// size is a fingerprint however far the region lands; a buffer that only
// roughly fits is not taken.

const (
	chunkHeaderSize   = 16
	chunkFlagMask     = 0x7
	chunkFlagIsMapped = 0x2
	pageSize          = 4096
)

// exactChunkSize is the size glibc gives an mmapped chunk holding exactly
// frameSize bytes: the data plus its header, rounded up to a page.
func exactChunkSize(frameSize int64) int64 {
	return (frameSize + chunkHeaderSize + pageSize - 1) / pageSize * pageSize
}

// FramebufferCandidate forces the n-th framebuffer candidate listed in the
// startup log (RK_FB_CANDIDATE). A negative value selects automatically.
var FramebufferCandidate = -1

// memRegion is one line of /proc/[pid]/maps.
type memRegion struct {
	start, end int64
	perms      string
	path       string
	// afterCard0 is true when the previous line of the maps file is a
	// /dev/dri/card0 mapping.
	afterCard0 bool
	// card0Distance counts the lines since the last /dev/dri/card0 mapping
	// above this one (1 when afterCard0), or 0 when there is none.
	card0Distance int
}

func (r memRegion) size() int64 { return r.end - r.start }

// anonymousReadable reports whether the region is private anonymous memory we
// are allowed to read.
func (r memRegion) anonymousReadable() bool {
	return r.path == "" && strings.HasPrefix(r.perms, "rw") && strings.HasSuffix(r.perms, "p")
}

// fbCandidate is a chunk that could hold the screen image.
type fbCandidate struct {
	addr       int64 // address of the chunk header (what the stream reads from)
	chunkSize  int64
	region     memRegion
	chunkIndex int
	// exactSize marks a chunk of exactly the screen image plus its header,
	// rounded up to a page, somewhere after a card0 mapping; like one right
	// after card0, it may be picked automatically.
	exactSize bool
}

// parseMaps reads a /proc/[pid]/maps stream.
func parseMaps(r io.Reader) ([]memRegion, error) {
	var regions []memRegion
	lastCard0 := -1
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		bounds := strings.Split(fields[0], "-")
		if len(bounds) != 2 {
			continue
		}
		start, err1 := strconv.ParseUint(bounds[0], 16, 64)
		end, err2 := strconv.ParseUint(bounds[1], 16, 64)
		if err1 != nil || err2 != nil || end <= start {
			continue
		}
		path := ""
		if len(fields) >= 6 {
			path = strings.Join(fields[5:], " ")
		}
		distance := 0
		if lastCard0 >= 0 {
			distance = len(regions) - lastCard0
		}
		regions = append(regions, memRegion{
			start:         int64(start),
			end:           int64(end),
			perms:         fields[1],
			path:          path,
			afterCard0:    distance == 1,
			card0Distance: distance,
		})
		if strings.Contains(path, "/dev/dri/card0") {
			lastCard0 = len(regions) - 1
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading maps file: %w", err)
	}
	return regions, nil
}

// walkChunks follows the malloc chunk headers inside one region and returns
// the chunks large enough to hold a screen image of frameSize bytes after
// their header, and smaller than two images.
// It never reads outside the region, and every step advances by at least a
// page, so the walk ends.
func walkChunks(mem io.ReaderAt, region memRegion, frameSize int64) []fbCandidate {
	var found []fbCandidate
	header := make([]byte, 8)
	var offset int64
	for i := 0; ; i++ {
		if offset+chunkHeaderSize > region.size() {
			break
		}
		if _, err := mem.ReadAt(header, region.start+offset+8); err != nil {
			break
		}
		raw := binary.LittleEndian.Uint64(header)
		size := int64(raw &^ chunkFlagMask)
		// A valid chunk here is mmapped, page aligned and fits in the region
		// (compared without adding, so a garbage size cannot overflow).
		if raw&chunkFlagIsMapped == 0 || size <= 0 || size%pageSize != 0 || size > region.size()-offset {
			break
		}
		if size >= frameSize+chunkHeaderSize && size < 2*frameSize {
			found = append(found, fbCandidate{
				addr:       region.start + offset,
				chunkSize:  size,
				region:     region,
				chunkIndex: i,
			})
		}
		offset += size
	}
	return found
}

// findFramebufferCandidates returns every chunk that could be the screen
// buffer, best guess first. Chunks in regions that follow a /dev/dri/card0
// mapping come first, the region after the last card0 mapping leading and
// chunks in walk order within a region, like the original algorithm. Chunks of
// exactly the screen's size further after a card0 mapping come next, the
// closest first. Other chunks follow, the one closest in size to frameSize
// first.
func findFramebufferCandidates(mem io.ReaderAt, regions []memRegion, frameSize int64) []fbCandidate {
	var near, exactFit, far []fbCandidate
	exact := exactChunkSize(frameSize)
	for i := len(regions) - 1; i >= 0; i-- {
		region := regions[i]
		if !region.anonymousReadable() || region.size() < frameSize {
			continue
		}
		for _, c := range walkChunks(mem, region, frameSize) {
			switch {
			case region.afterCard0:
				near = append(near, c)
			case c.chunkSize == exact && region.card0Distance > 0:
				c.exactSize = true
				exactFit = append(exactFit, c)
			default:
				far = append(far, c)
			}
		}
	}
	sort.SliceStable(exactFit, func(i, j int) bool {
		return exactFit[i].region.card0Distance < exactFit[j].region.card0Distance
	})
	sort.Slice(far, func(i, j int) bool {
		if far[i].chunkSize != far[j].chunkSize {
			return far[i].chunkSize < far[j].chunkSize
		}
		return far[i].addr < far[j].addr
	})
	return append(append(near, exactFit...), far...)
}

// chooseCandidate returns the index of the candidate to stream from.
// A negative override selects automatically, and then only a chunk next to a
// /dev/dri/card0 mapping, or one of exactly the screen's size, is accepted:
// without one, xochitl has most likely not set up its display yet, and
// failing lets the service retry instead of streaming unrelated memory.
func chooseCandidate(candidates []fbCandidate, override int) (int, error) {
	if len(candidates) == 0 {
		return 0, fmt.Errorf("no framebuffer candidate found")
	}
	if override >= 0 {
		if override >= len(candidates) {
			return 0, fmt.Errorf("RK_FB_CANDIDATE=%d is not a candidate number between 0 and %d", override, len(candidates)-1)
		}
		return override, nil
	}
	if !candidates[0].region.afterCard0 && !candidates[0].exactSize {
		return 0, fmt.Errorf("no framebuffer candidate next to a /dev/dri/card0 mapping or of exactly the screen's size (xochitl may still be starting); "+
			"%d candidate(s) elsewhere, set RK_FB_CANDIDATE to use one", len(candidates))
	}
	return 0, nil
}

// pageLikeness samples the image in a candidate chunk (BGRA, after the
// 16-byte header): the share of opaque pixels and of near-white ones. A
// screen buffer is opaque and, on e-ink, mostly white; other memory is not.
// For the startup log, so a wrong pick shows; it does not choose.
func pageLikeness(mem io.ReaderAt, c fbCandidate, frameSize int64) (opaque, white float64) {
	const samples = 512
	pixel := make([]byte, 4)
	var read, opaqueN, whiteN int
	pixels := frameSize / 4
	for i := int64(0); i < samples; i++ {
		off := c.addr + chunkHeaderSize + (i*pixels/samples)*4
		if _, err := mem.ReadAt(pixel, off); err != nil {
			continue
		}
		read++
		if pixel[3] == 0xff {
			opaqueN++
		}
		if pixel[0] >= 0xf0 && pixel[1] >= 0xf0 && pixel[2] >= 0xf0 {
			whiteN++
		}
	}
	if read == 0 {
		return 0, 0
	}
	return float64(opaqueN) / float64(read), float64(whiteN) / float64(read)
}
