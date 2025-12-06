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
