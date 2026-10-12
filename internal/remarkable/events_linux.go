//go:build linux

package remarkable

import (
	"context"
	"log"
	"os"
	"syscall"
	"time"
	"unsafe"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/pubsub"
)

// EventScanner ...
type EventScanner struct {
	pen, touch *os.File
	closed     bool
}

// NewEventScanner ...
func NewEventScanner() *EventScanner {
	pen, err := os.OpenFile(PenInputDevice, os.O_RDONLY, 0o644)
	if err != nil {
		log.Fatalf("failed to read pen position: %v", err)
	}
	touch, err := os.OpenFile(TouchInputDevice, os.O_RDONLY, 0o644)
	if err != nil {
		log.Fatalf("failed to read touch position: %v", err)
	}
	return &EventScanner{
		pen:   pen,
		touch: touch,
	}
}

// Close closes the input device files. Safe to call multiple times.
func (e *EventScanner) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true

	var firstErr error
	if err := e.pen.Close(); err != nil {
		firstErr = err
	}
	if err := e.touch.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// StartAndPublish ...
func (e *EventScanner) StartAndPublish(ctx context.Context, pubsub *pubsub.PubSub) {
	// Start a goroutine to read events and send them on the channel
	go func() {
		for {
			// Check context before blocking read
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Blocking read with timeout to allow context cancellation checks
			e.pen.SetReadDeadline(time.Now().Add(1 * time.Second))
			ev, err := readEvent(e.pen)

			if err != nil {
				// Check if it's a timeout (expected)
				if os.IsTimeout(err) {
					continue
				}
				log.Println(err)
				continue
			}

			pubsub.Publish(events.InputEventFromSource{
				Source:     events.Pen,
				InputEvent: ev,
			})
		}
	}()
	// Start a goroutine to read events and send them on the channel
	go func() {
		for {
			// Check context before blocking read
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Blocking read with timeout to allow context cancellation checks
			e.touch.SetReadDeadline(time.Now().Add(1 * time.Second))
			ev, err := readEvent(e.touch)

			if err != nil {
				// Check if it's a timeout (expected)
				if os.IsTimeout(err) {
					continue
				}
				log.Println(err)
				continue
			}

			pubsub.Publish(events.InputEventFromSource{
				Source:     events.Touch,
				InputEvent: ev,
			})
		}
	}()
}

// eviocgrab is EVIOCGRAB from linux/input.h: _IOW('E', 0x90, int).
const eviocgrab = 1<<30 | 4<<16 | 'E'<<8 | 0x90

// GrabTouch takes the touch panel for this program alone, or gives it back.
// While it is taken, xochitl sees no finger input, so a swipe no longer
// turns its page; this program still reads every touch, and the pen is a
// separate device. The kernel gives the panel back when the program exits.
func (e *EventScanner) GrabTouch(grab bool) error {
	var arg uintptr
	if grab {
		arg = 1
	}
	// Control rather than Fd: Fd would switch the file to blocking mode and
	// end the read deadlines the reader relies on.
	conn, err := e.touch.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, eviocgrab, arg); errno != 0 {
			ioctlErr = errno
		}
	}); err != nil {
		return err
	}
	return ioctlErr
}

func readEvent(inputDevice *os.File) (events.InputEvent, error) {
	var ev events.InputEvent
	_, err := inputDevice.Read((*(*[unsafe.Sizeof(ev)]byte)(unsafe.Pointer(&ev)))[:])
	return ev, err

}
