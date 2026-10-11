package remarkable

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// Paper Pro screen buffer size (1632 x 2154 x 4), repeated here so the test
// runs on any architecture.
const rmppFrameSize = 1632 * 2154 * 4

// Chunk sizes observed in xochitl on a Paper Pro (goMarkableStream PR #159).
const (
	chunkSmall  = 3526656
	chunkTiny   = 221184
	chunkScreen = 14102528
)

// fakeMem emulates /proc/[pid]/mem: only the chunk headers written with
// putChunks are readable, everything else fails like a guard page does.
type fakeMem map[int64]uint64

func (m fakeMem) ReadAt(p []byte, off int64) (int, error) {
	v, ok := m[off]
	if !ok || len(p) != 8 {
		return 0, errors.New("input/output error")
	}
	binary.LittleEndian.PutUint64(p, v)
	return 8, nil
}

// putChunks lays mmapped malloc chunks back to back starting at addr.
func (m fakeMem) putChunks(addr int64, sizes ...int64) {
	for _, s := range sizes {
		m[addr+8] = uint64(s) | chunkFlagIsMapped
		addr += s
	}
}

func mustParse(t *testing.T, maps string) []memRegion {
	t.Helper()
	regions, err := parseMaps(strings.NewReader(maps))
	if err != nil {
		t.Fatal(err)
	}
	return regions
}

// Tail of /proc/[pid]/maps reported on a Paper Pro with firmware 3.27.3.0.
// The first card0 line is inferred: the report was `grep -A2 dri/card0`, so the
// 21 MB region is by construction the line after a card0 mapping.
const mapsFW327 = `ffffa0206000-ffffa03b3000 rw-s 00000000 00:06 275                        /dev/dri/card0
ffffa03b3000-ffffa17e0000 rw-p 00000000 00:00 0
ffffa17e0000-ffffa17f0000 ---p 00000000 00:00 0
ffffa17f0000-ffffa1ff0000 rw-p 00000000 00:00 0
ffffa2b3a000-ffffa2ce7000 rw-s 00000000 00:06 275                        /dev/dri/card0
ffffa2ce7000-ffffa2cf7000 ---p 00000000 00:00 0
ffffa2cf7000-ffffa34f7000 rw-p 00000000 00:00 0
`

func TestParseMaps(t *testing.T) {
	regions := mustParse(t, mapsFW327)
	if len(regions) != 7 {
		t.Fatalf("got %d regions, want 7", len(regions))
	}
	big := regions[1]
	if big.start != 0xffffa03b3000 || big.end != 0xffffa17e0000 {
		t.Errorf("wrong bounds: %x-%x", big.start, big.end)
	}
	if !big.afterCard0 || !big.anonymousReadable() {
		t.Errorf("21 MB region should be anonymous, readable and after card0: %+v", big)
	}
	// The region is exactly two small chunks plus the screen chunk.
	if big.size() != 2*chunkSmall+chunkScreen {
		t.Errorf("region size %d does not match the expected chunk layout", big.size())
	}
	guard := regions[5]
	if guard.anonymousReadable() {
		t.Errorf("guard page must not be considered readable: %+v", guard)
	}
	if !guard.afterCard0 {
		t.Errorf("guard page follows the last card0 mapping")
	}
}

// Firmware 3.27: the last card0 mapping is followed by a guard page, and the
// screen buffer sits in a region after an earlier card0 mapping.
func TestFindFramebufferFW327(t *testing.T) {
	regions := mustParse(t, mapsFW327)
	const base = 0xffffa03b3000

	layouts := map[string]struct {
		sizes []int64
		want  int64
	}{
		"screen last":   {[]int64{chunkSmall, chunkSmall, chunkScreen}, base + 2*chunkSmall},
		"screen middle": {[]int64{chunkSmall, chunkScreen, chunkSmall}, base + chunkSmall},
		"screen first":  {[]int64{chunkScreen, chunkSmall, chunkSmall}, base},
	}
	for name, l := range layouts {
		t.Run(name, func(t *testing.T) {
			mem := fakeMem{}
			mem.putChunks(base, l.sizes...)
			got := findFramebufferCandidates(mem, regions, rmppFrameSize)
			if len(got) != 1 {
				t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
			}
			if got[0].addr != l.want || got[0].chunkSize != chunkScreen {
				t.Errorf("got addr=%x size=%d, want addr=%x size=%d", got[0].addr, got[0].chunkSize, l.want, chunkScreen)
			}
		})
	}
}

// Firmware 3.25 layout from goMarkableStream issue #158: a 17.6 MB region
// after a run of card0 mappings, then a last card0 mapping followed by an
// unrelated 4.3 MB region.
func TestFindFramebufferFW325(t *testing.T) {
	regions := mustParse(t, `ffff7e573000-ffff7e720000 rw-s 00000000 00:06 287                        /dev/dri/card0
ffff7e720000-ffff7f7f0000 rw-p 00000000 00:00 0
ffff7f7f0000-ffff7f800000 ---p 00000000 00:00 0
ffff84022000-ffff841cf000 rw-s 00000000 00:06 287                        /dev/dri/card0
ffff841cf000-ffff845f0000 rw-p 00000000 00:00 0
ffff845f0000-ffff84600000 ---p 00000000 00:00 0
`)
	mem := fakeMem{}
	mem.putChunks(0xffff7e720000, chunkSmall, chunkScreen)
	mem.putChunks(0xffff841cf000, 4329472)

	got := findFramebufferCandidates(mem, regions, rmppFrameSize)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	if want := int64(0xffff7e720000 + chunkSmall); got[0].addr != want {
		t.Errorf("got addr=%x, want %x", got[0].addr, want)
	}
}

// Older layout (walk recorded in PR #159): the buffer is in the region right
// after the last card0 mapping. The result must match the original algorithm.
func TestFindFramebufferOlderLayout(t *testing.T) {
	regions := mustParse(t, `ffff813d0000-ffff8157d000 rw-s 00000000 00:06 287                        /dev/dri/card0
ffff8157d000-ffff829e0000 rw-p 00000000 00:00 0
ffff829e0000-ffff829f0000 ---p 00000000 00:00 0
`)
	mem := fakeMem{}
	mem.putChunks(0xffff8157d000, chunkSmall, chunkTiny, chunkSmall, chunkScreen)

	got := findFramebufferCandidates(mem, regions, rmppFrameSize)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
	// 0xffff81c6d000 is the address confirmed as correct in PR #159.
	if got[0].addr != 0xffff81c6d000 {
		t.Errorf("got addr=%x, want ffff81c6d000", got[0].addr)
	}
}

// A buffer that is not next to any card0 mapping is still found, but ranked
// after one that is.
func TestCandidateRanking(t *testing.T) {
	regions := mustParse(t, `ffff10000000-ffff12000000 rw-p 00000000 00:00 0
ffff20000000-ffff201ad000 rw-s 00000000 00:06 275                        /dev/dri/card0
ffff201ad000-ffff22000000 rw-p 00000000 00:00 0
`)
	mem := fakeMem{}
	mem.putChunks(0xffff10000000, chunkScreen)
	mem.putChunks(0xffff201ad000, chunkScreen)

	got := findFramebufferCandidates(mem, regions, rmppFrameSize)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	if got[0].addr != 0xffff201ad000 || got[1].addr != 0xffff10000000 {
		t.Errorf("wrong order: %x then %x", got[0].addr, got[1].addr)
	}
}

// With buffers next to two card0 mappings, the one after the last mapping
// comes first, as in the original algorithm, even if the other is smaller.
func TestCandidateRankingPrefersLastCard0(t *testing.T) {
	regions := mustParse(t, `ffff10000000-ffff101ad000 rw-s 00000000 00:06 275                        /dev/dri/card0
ffff101ad000-ffff12000000 rw-p 00000000 00:00 0
ffff20000000-ffff201ad000 rw-s 00000000 00:06 275                        /dev/dri/card0
ffff201ad000-ffff22000000 rw-p 00000000 00:00 0
`)
	mem := fakeMem{}
	mem.putChunks(0xffff101ad000, chunkScreen)
	mem.putChunks(0xffff201ad000, chunkSmall, chunkScreen+pageSize)

	got := findFramebufferCandidates(mem, regions, rmppFrameSize)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	if got[0].addr != 0xffff201ad000+chunkSmall || got[1].addr != 0xffff101ad000 {
		t.Errorf("wrong order: %x then %x", got[0].addr, got[1].addr)
	}
}

func TestChooseCandidate(t *testing.T) {
	near := fbCandidate{addr: 1, region: memRegion{afterCard0: true}}
	far := fbCandidate{addr: 2}

	tests := map[string]struct {
		candidates []fbCandidate
		override   int
		want       int
		wantErr    bool
	}{
		"automatic takes the first":        {[]fbCandidate{near, far}, -1, 0, false},
		"automatic refuses a far buffer":   {[]fbCandidate{far}, -1, 0, true},
		"override may pick a far buffer":   {[]fbCandidate{far}, 0, 0, false},
		"override picks by index":          {[]fbCandidate{near, far}, 1, 1, false},
		"override out of range":            {[]fbCandidate{near, far}, 2, 0, true},
		"no candidates":                    {nil, -1, 0, true},
		"no candidates even with override": {nil, 0, 0, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := chooseCandidate(tt.candidates, tt.override)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// The walk is bounded by the region, not by a chunk count, so a screen
// buffer behind many small chunks is still found.
func TestWalkChunksManyChunks(t *testing.T) {
	const n = 100
	start := int64(0x10000000)
	region := memRegion{start: start, end: start + n*chunkTiny + chunkScreen, perms: "rw-p"}
	sizes := make([]int64, 0, n+1)
	for i := 0; i < n; i++ {
		sizes = append(sizes, chunkTiny)
	}
	mem := fakeMem{}
	mem.putChunks(start, append(sizes, chunkScreen)...)

	got := walkChunks(mem, region, rmppFrameSize)
	if len(got) != 1 || got[0].addr != start+n*chunkTiny || got[0].chunkIndex != n {
		t.Fatalf("got %+v, want the screen chunk at index %d", got, n)
	}
}

// boundedMem fails the test if the walk reads outside its region.
type boundedMem struct {
	fakeMem
	t      *testing.T
	region memRegion
}

func (m boundedMem) ReadAt(p []byte, off int64) (int, error) {
	if off < m.region.start || off+int64(len(p)) > m.region.end {
		m.t.Errorf("read at %x is outside %x-%x", off, m.region.start, m.region.end)
	}
	return m.fakeMem.ReadAt(p, off)
}

// A chunk must hold the whole image after its 16-byte header. Here the frame
// size is exactly page aligned, as the Paper Pro Move's is.
func TestWalkChunksNeedsRoomForHeader(t *testing.T) {
	const frame = 1590 * pageSize
	region := memRegion{start: 0x1000000, end: 0x1000000 + 4*frame, perms: "rw-p"}

	mem := fakeMem{}
	mem.putChunks(region.start, frame, frame+pageSize)
	got := walkChunks(mem, region, frame)
	if len(got) != 1 || got[0].addr != region.start+frame {
		t.Fatalf("got %+v, want only the chunk at %x", got, region.start+frame)
	}
}

func TestWalkChunksStopsSafely(t *testing.T) {
	region := memRegion{start: 0x1000000, end: 0x1000000 + 2*chunkScreen, perms: "rw-p"}

	cases := map[string]fakeMem{
		"unreadable memory":    {},
		"zero size":            {region.start + 8: 0},
		"not mmapped":          {region.start + 8: chunkScreen},
		"not page aligned":     {region.start + 8: (chunkScreen + 16) | chunkFlagIsMapped},
		"larger than region":   {region.start + 8: (4 * chunkScreen) | chunkFlagIsMapped},
		"too big for a screen": {region.start + 8: (6866 * pageSize) | chunkFlagIsMapped}, // first page-aligned size >= 2 frames
		"too small":            {region.start + 8: chunkSmall | chunkFlagIsMapped},
		// After a valid chunk, a garbage size that passes the flag and
		// alignment checks would overflow offset+size.
		"huge size": {
			region.start + 8:              chunkSmall | chunkFlagIsMapped,
			region.start + chunkSmall + 8: 0x7ffffffffffff000 | chunkFlagIsMapped,
		},
	}
	for name, mem := range cases {
		t.Run(name, func(t *testing.T) {
			if got := walkChunks(boundedMem{mem, t, region}, region, rmppFrameSize); len(got) != 0 {
				t.Errorf("expected no candidate, got %+v", got)
			}
		})
	}
}
