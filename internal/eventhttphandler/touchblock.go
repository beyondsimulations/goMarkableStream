package eventhttphandler

import (
	"errors"
	"log"
	"sync"
	"time"
)

// How long one /gestures?block=1 request holds the touch panel before it
// ends and the browser asks again, and how long the panel stays taken after
// the last holder leaves. A page that went away without closing its
// connection (a laptop asleep) lets go within blockLease+blockGrace; a page
// that asks again in time keeps the panel without a gap.
var (
	blockLease = 30 * time.Second
	blockGrace = 5 * time.Second
)

// touchBlock keeps the touch panel to this program while at least one
// browser asks for it, so xochitl ignores the fingers: swiping through
// slides no longer turns the notebook's page or adds one at its end.
type touchBlock struct {
	mu    sync.Mutex
	grab  func(bool) error // nil where the panel cannot be taken
	allow func() error     // nil, or why the block is refused right now
	// holders counts the open block requests of each page (its ?client=
	// id); a renewal overlaps the request it replaces.
	holders map[string]int
	held    bool
	letGo   *time.Timer // gives the panel back once the grace has passed
}

// acquire adds one holder for the page, taking the panel unless it is
// still held. A refused or failed grab adds none.
func (b *touchBlock) acquire(client string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.grab == nil {
		return errors.New("blocking the touch panel is not available")
	}
	if b.allow != nil {
		if err := b.allow(); err != nil {
			return err
		}
	}
	if b.letGo != nil {
		b.letGo.Stop()
		b.letGo = nil
	}
	if !b.held {
		if err := b.grab(true); err != nil {
			return err
		}
		b.held = true
		log.Print("Touch: the tablet ignores finger input (a presentation asked for it)")
	}
	if b.holders == nil {
		b.holders = make(map[string]int)
	}
	b.holders[client]++
	return nil
}

// release removes one of the page's holders, unless drop already did;
// after the last holder of any page, the panel is given back once
// blockGrace has passed without a new one.
func (b *touchBlock) release(client string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.holders[client] == 0 {
		return
	}
	b.holders[client]--
	if b.holders[client] == 0 {
		delete(b.holders, client)
	}
	if len(b.holders) == 0 && b.held {
		b.letGo = time.AfterFunc(blockGrace, b.giveBack)
	}
}

// drop ends the page's holds at once, because it turned the block off,
// and reports whether other pages still hold the panel. Without them the
// panel is given back now, without the grace.
func (b *touchBlock) drop(client string) (others bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.holders, client)
	if len(b.holders) > 0 {
		return true
	}
	if b.letGo != nil {
		b.letGo.Stop()
		b.letGo = nil
	}
	if b.held {
		b.giveBackLocked()
	}
	return false
}

func (b *touchBlock) giveBack() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.holders) > 0 || !b.held {
		return // asked for again meanwhile
	}
	b.letGo = nil
	b.giveBackLocked()
}

// giveBackLocked hands the panel back to xochitl; b.mu is held.
func (b *touchBlock) giveBackLocked() {
	// EVIOCGRAB(0) fails only when the panel is not ours, or when its file
	// is closed, which gives it back too: either way it is no longer held.
	b.held = false
	if err := b.grab(false); err != nil {
		log.Printf("Touch: giving the panel back: %v", err)
		return
	}
	log.Print("Touch: the tablet reacts to finger input again")
}
