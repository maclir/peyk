//go:build !darwin

package agent

// WatchPower reports nothing off macOS; presence alone tells a sleeping
// laptop from a running one.
func WatchPower() (<-chan PowerEvent, error) { return nil, nil }

// Asleep reports nothing off macOS.
func Asleep() bool { return false }

// StayAwake is a no-op off macOS.
func StayAwake() func() { return func() {} }
