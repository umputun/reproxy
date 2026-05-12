package proxy

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoundRobinSelector_Select(t *testing.T) {
	selector := &RoundRobinSelector{}

	testCases := []struct {
		name     string
		len      int
		expected int
	}{
		{"First call", 3, 0},
		{"Second call", 3, 1},
		{"Third call", 3, 2},
		{"Back to zero", 3, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := selector.Select(tc.len)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestRoundRobinSelector_SelectShrinkingN(t *testing.T) {
	// reproduces issue #250: when the alive-backend count shrinks between calls
	// (e.g. a backend health-check flips dead), the stale lastSelected can exceed
	// the new n, causing matchHandler to index out of range and panic.
	selector := &RoundRobinSelector{}

	// advance internal state with n=3 so the next return position is 2
	assert.Equal(t, 0, selector.Select(3))
	assert.Equal(t, 1, selector.Select(3))

	// one backend goes unhealthy: n shrinks to 2. result must remain a valid index.
	got := selector.Select(2)
	assert.GreaterOrEqual(t, got, 0)
	assert.Less(t, got, 2, "Select(2) returned %d, out of range for slice of length 2", got)
}

func TestRoundRobinSelector_SelectConcurrent(t *testing.T) {
	selector := &RoundRobinSelector{}
	l := 3
	numGoroutines := 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	results := &sync.Map{}

	for range numGoroutines {
		go func() {
			defer wg.Done()
			result := selector.Select(l)
			results.Store(result, struct{}{})
		}()
	}

	wg.Wait()

	// check that all possible results are present in the map.
	for i := range l {
		_, ok := results.Load(i)
		assert.True(t, ok, "expected to find %d in the results", i)
	}
}

func TestRandomSelector_Select(t *testing.T) {
	selector := &RandomSelector{}

	testCases := []struct {
		name string
		len  int
	}{
		{"First call", 5},
		{"Second call", 5},
		{"Third call", 5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := selector.Select(tc.len)
			assert.True(t, result >= 0 && result < tc.len)
		})
	}
}

func TestFailoverSelector_Select(t *testing.T) {
	selector := &FailoverSelector{}

	testCases := []struct {
		name     string
		len      int
		expected int
	}{
		{"First call", 5, 0},
		{"Second call", 5, 0},
		{"Third call", 5, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := selector.Select(tc.len)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestLBSelectorFunc_Select(t *testing.T) {
	selector := LBSelectorFunc(func(n int) int {
		return n - 1 // simple selection logic for testing
	})

	testCases := []struct {
		name     string
		len      int
		expected int
	}{
		{"First call", 5, 4},
		{"Second call", 3, 2},
		{"Third call", 1, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := selector.Select(tc.len)
			assert.Equal(t, tc.expected, result)
		})
	}
}
