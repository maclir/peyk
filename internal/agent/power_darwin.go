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
			return
		}
		if Asleep() {
			go awaitFullWake(power.sleeps.Load())
		}
	})
	return power.events, power.err
}

// Asleep reports a dark wake: the Mac runs with its lid closed.
func Asleep() bool {
	c := C.peyk_power_capabilities()
	return c >= 0 && c&graphicsCapability == 0
}

// StayAwake holds PreventUserIdleSystemSleep and PreventSystemSleep until the
// returned function is called. Taking them before stopping the agent closes
// the gap where macOS can start Idle Sleep on battery before the replacement
// agent's caffeinate registers.
func StayAwake() func() {
	idle, sys := holdPowerAssertions()
	return sync.OnceFunc(func() {
		releasePowerAssertion(idle)
		releasePowerAssertion(sys)
	})
}

func holdPowerAssertions() (uint32, uint32) {
	var idle, sys C.uint32_t
	C.peyk_power_stay_awake(&idle, &sys)
	return uint32(idle), uint32(sys)
}

func releasePowerAssertion(id uint32) {
	C.peyk_power_release(C.uint32_t(id))
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
