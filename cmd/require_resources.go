package cmd

import (
	"errors"
	"fmt"
)

// errResourcesBelowMin is the sentinel returned when a --require-resources
// or --min-resources gate trips. Surfaced to stderr by Execute(); commands
// emit normal output before returning, so the gate is an exit-code signal,
// not a render-suppressing toggle.
var errResourcesBelowMin = errors.New("resources below required minimum")

// gateResourceCount checks whether the post-filter resource count meets the
// floor implied by --require-resources / --min-resources. First non-nil of
// (--min-resources > 0, --require-resources) wins; both unset = no-op.
// Returns errResourcesBelowMin (wrapped with diagnostic context) when the
// count is below the floor.
func gateResourceCount(count int, requireResources bool, minResources uint64) error {
	min := uint64(0)
	switch {
	case minResources > 0:
		min = minResources
	case requireResources:
		min = 1
	default:
		return nil
	}
	if uint64(count) < min {
		return fmt.Errorf("%w: have %d, want >= %d (run a fresh scan or relax filters)", errResourcesBelowMin, count, min)
	}
	return nil
}
