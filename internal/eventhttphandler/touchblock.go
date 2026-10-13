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
	// holds maps each open block request to its page (its ?client= id). A
	// request has its own entry, so one that drop ended cannot, when it
	// returns later, remove a newer request of the same page.
	holds  map[uint64]string
	nextID uint64
	held   bool
	letGo  *time.Timer // gives the panel back once the grace has passed
	// letGoGen numbers the current letGo: a timer that fired just before it
	// was stopped finds its number outdated and does nothing.
	letGoGen uint64
	// lastHolder is the page whose request ended last, while the panel
	// stays taken: that page may still end the block at once.
	lastHolder string
}

// acquire adds a hold for one request of the page, taking the panel unless
// it is still held. A refused or failed grab adds none.
func (b *touchBlock) acquire(client string) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.grab == nil {
		return 0, errors.New("blocking the touch panel is not available")
	}
	if b.allow != nil {
		if err := b.allow(); err != nil {
			return 0, err
		}
	}
	b.stopLetGo()
	if !b.held {
		if err := b.grab(true); err != nil {
			return 0, err
		}
		b.held = true
		log.Print("Touch: the tablet ignores finger input (a presentation asked for it)")
	}
	if b.holds == nil {
		b.holds = make(map[uint64]string)
	}
	b.nextID++
	b.holds[b.nextID] = client
	return b.nextID, nil
}

// release ends the hold of a request, unless drop already did; after the
// last hold of any page, the panel is given back once blockGrace has passed
// without a new one.
func (b *touchBlock) release(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	client, ok := b.holds[id]
	if !ok {
		return
	}
	delete(b.holds, id)
	if len(b.holds) == 0 && b.held {
		b.lastHolder = client
		b.letGoGen++
		gen := b.letGoGen
		b.letGo = time.AfterFunc(blockGrace, func() { b.giveBack(gen) })
	}
}

// stopLetGo cancels a pending give-back, including one whose timer has
// already fired and waits for b.mu; b.mu is held.
func (b *touchBlock) stopLetGo() {
	if b.letGo != nil {
		b.letGo.Stop()
		b.letGo = nil
	}
	b.letGoGen++
}

// drop ends the holds of a page that turned the block off, and reports
// whether other pages still hold the panel. Without them, the panel is given
// back now, without the grace, if this page held it or was the last to let
// go; another page's turning off changes nothing it did not hold.
func (b *touchBlock) drop(client string) (others bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	had := false
	if client != "" {
		for id, c := range b.holds {
			if c == client {
				delete(b.holds, id)
				had = true
			}
		}
	}
	if len(b.holds) > 0 {
		return true
	}
	if !had && (client == "" || b.lastHolder != client) {
		return false
	}
	b.stopLetGo()
	if b.held {
		b.giveBackLocked()
	}
	return false
}

func (b *touchBlock) giveBack(gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen != b.letGoGen || len(b.holds) > 0 || !b.held {
		return // stopped, or asked for again meanwhile
	}
	b.letGo = nil
	b.giveBackLocked()
}

// giveBackLocked hands the panel back to xochitl; b.mu is held.
func (b *touchBlock) giveBackLocked() {
	// EVIOCGRAB(0) fails only when the panel is not ours, or when its file
	// is closed, which gives it back too: either way it is no longer held.
	b.held = false
	b.lastHolder = ""
	if err := b.grab(false); err != nil {
		log.Printf("Touch: giving the panel back: %v", err)
		return
	}
	log.Print("Touch: the tablet reacts to finger input again")
}
