//go:build !darwin

package agent

// WatchPower reports nothing off macOS; presence alone tells a sleeping
// laptop from a running one.
func WatchPower() (<-chan PowerEvent, error) { return nil, nil }
