//go:build linux && arm64

package remarkable

import (
	"fmt"
	"log"
	"os"
)

// getFramePointer locates the framebuffer in memory for RMPP.
//
// RMPP uses a modern GPU/DRM display stack (/dev/dri/card0) rather than
// the classic framebuffer device. The composed screen image lives in a
// malloc'ed buffer inside xochitl, so it has to be found by inspection:
//  1. Read every region of /proc/[pid]/maps
//  2. Walk the malloc chunk headers of the anonymous regions
//  3. Pick the chunk whose size matches the screen buffer
//
// The size searched for is the buffer as it is stored in memory, so it
// includes the row padding of the Paper Pro Move.
//
// See fbscan.go for the details and for why the position of the buffer
// relative to the /dev/dri/card0 mappings cannot be assumed any more.
//
// Set RK_FB_CANDIDATE=<n> (FramebufferCandidate) to force the n-th candidate
// from the startup log if the automatic choice shows the wrong picture.
func getFramePointer(pid string) (int64, error) {
	mapsFile, err := os.Open(fmt.Sprintf("/proc/%s/maps", pid))
	if err != nil {
		return 0, fmt.Errorf("cannot open maps file: %w", err)
	}
	regions, err := parseMaps(mapsFile)
	mapsFile.Close()
	if err != nil {
		return 0, fmt.Errorf("failed to get memory range: %w", err)
	}

	mem, err := os.Open(fmt.Sprintf("/proc/%s/mem", pid))
	if err != nil {
		return 0, fmt.Errorf("cannot open memory file: %w", err)
	}
	defer mem.Close()

	candidates := findFramebufferCandidates(mem, regions, FramebufferStorageSizeBytes)
	if len(candidates) == 0 {
		return 0, fmt.Errorf("failed to calculate frame pointer: no memory chunk of at least %d bytes found in %d regions of xochitl (pid %s)",
			FramebufferStorageSizeBytes, len(regions), pid)
	}

	for i, c := range candidates {
		log.Printf("framebuffer candidate %d: addr=0x%x size=%d (chunk %d of region %x-%x, after card0: %v)",
			i, c.addr, c.chunkSize, c.chunkIndex, c.region.start, c.region.end, c.region.afterCard0)
	}

	choice, err := chooseCandidate(candidates, FramebufferCandidate)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate frame pointer: %w", err)
	}
	log.Printf("using framebuffer candidate %d", choice)

	return candidates[choice].addr, nil
}
