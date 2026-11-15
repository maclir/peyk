package agent

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include "power_darwin.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// graphicsCapability is IOPMrootDomain's kIOPMSystemCapabilityGraphics. A
// Power Nap dark wake runs the CPU and network without it, and the laptop
// must stay offline through one.
const graphicsCapability = 2

// macOS lets a sleep proceed without the acknowledgement after about 30 seconds.
const sleepAckWait = 20 * time.Second

var power struct {
	once   sync.Once
	err    error
	events chan PowerEvent
	// sleeps ends a wait for full wake that a later sleep made moot.
	sleeps atomic.Uint64
}

// WatchPower reports the Mac going to sleep and waking fully. It registers
// once per process; later calls share the first registration.
func WatchPower() (<-chan PowerEvent, error) {
	power.once.Do(func() {
		power.events = make(chan PowerEvent)
		registered := make(chan C.int)
		go func() {
			// The run loop that delivers the notifications is the thread's own.
			runtime.LockOSThread()
			rc := C.peyk_power_register()
			registered <- rc
			if rc == 0 {
				C.peyk_power_loop()
			}
		}()
		if <-registered != 0 {
			power.err = errors.New("register for system power notifications")
		}
	})
	return power.events, power.err
}

//export peykPower
func peykPower(kind C.int, id C.long) {
	switch kind {
	case C.PEYK_WILL_SLEEP:
		power.sleeps.Add(1)
		allow := sync.OnceFunc(func() { C.peyk_power_allow(id) })
		select {
		case power.events <- PowerEvent{Asleep: true, Done: allow}:
		case <-time.After(sleepAckWait):
			allow()
		}
	case C.PEYK_POWERED_ON:
		go awaitFullWake(power.sleeps.Load())
	}
}

// awaitFullWake reports a wake once the display is in use. No notification
// marks the step from a dark wake to a full one, so it polls.
func awaitFullWake(sleep uint64) {
	for power.sleeps.Load() == sleep {
		if c := C.peyk_power_capabilities(); c < 0 || c&graphicsCapability != 0 {
			power.events <- PowerEvent{}
			return
		}
		time.Sleep(2 * time.Second)
	}
}
