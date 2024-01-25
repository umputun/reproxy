package proxy

import (
	"math/rand"
	"sync"
)

// RoundRobinSelector is a simple round-robin selector, thread-safe
type RoundRobinSelector struct {
	lastSelected int
	mu           sync.Mutex
}

// Select returns next backend index
func (r *RoundRobinSelector) Select(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	selected := r.lastSelected
	r.lastSelected = (r.lastSelected + 1) % n
	return selected
}

// RandomSelector is a random selector, thread-safe
type RandomSelector struct{}

// Select returns random backend index
func (r *RandomSelector) Select(n int) int {
	return rand.Intn(n) //nolint:gosec // no need for crypto/rand here
}

// FailoverSelector is a selector with failover, thread-safe
type FailoverSelector struct{}

// Select returns next backend index
func (r *FailoverSelector) Select(_ int) int {
	return 0 // dead server won't be in the list, we can safely pick the first one
}

// LBSelectorFunc is a functional adapted for LBSelector to select backend from the list
type LBSelectorFunc func(n int) int

// Select returns backend index
func (f LBSelectorFunc) Select(n int) int {
	return f(n)
}
