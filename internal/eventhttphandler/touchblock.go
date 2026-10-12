package eventhttphandler

import (
	"errors"
	"log"
	"sync"
)

// touchBlock keeps the touch panel to this program while at least one
// browser asks for it (/gestures?block=1), so xochitl ignores the fingers:
// swiping through slides no longer turns the notebook's page or adds one at
// its end. Each request holds the block until it ends, so closing the page
// gives the panel back.
type touchBlock struct {
	mu    sync.Mutex
	grab  func(bool) error // nil where the panel cannot be taken
	users int
}

// acquire adds one holder, taking the panel for the first. A failed grab
// adds none.
func (b *touchBlock) acquire() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.users == 0 {
		if b.grab == nil {
			return errors.New("blocking the touch panel is not available")
		}
		if err := b.grab(true); err != nil {
			return err
		}
		log.Print("Touch: the tablet ignores finger input (a presentation asked for it)")
	}
	b.users++
	return nil
}

// release removes one holder; the last gives the panel back.
func (b *touchBlock) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.users == 0 {
		return
	}
	b.users--
	if b.users > 0 {
		return
	}
	if err := b.grab(false); err != nil {
		log.Printf("Touch: giving the panel back failed: %v", err)
		return
	}
	log.Print("Touch: the tablet reacts to finger input again")
}
