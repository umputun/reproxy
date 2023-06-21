package memory

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultCleanupInterval = time.Minute * 15

var (
	// ErrNotExists is returned when key not exists
	ErrNotExists = fmt.Errorf("not exists")
)

type item struct {
	value   string
	timeout time.Time
}

// Memory is a simple in-memory key-value storage
type Memory struct {
	mx    *sync.Mutex
	store map[string]*item
}

// New creates new Memory
func New() *Memory {
	m := &Memory{
		mx:    &sync.Mutex{},
		store: map[string]*item{},
	}

	return m
}

// Start starts cleanup goroutine
func (m *Memory) Start(ctx context.Context) {
	go m.cleanup(ctx)
}

func (m *Memory) cleanup(ctx context.Context) {
	t := time.NewTicker(defaultCleanupInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mx.Lock()
			now := time.Now()
			for k, v := range m.store {
				if !v.timeout.IsZero() && v.timeout.Before(now) {
					delete(m.store, k)
				}
			}
			m.mx.Unlock()
		}
	}
}

// Set sets key-value pair with timeout
func (m *Memory) Set(key, value string, timeout time.Duration) error {
	m.mx.Lock()
	defer m.mx.Unlock()

	i := &item{
		value: value,
	}
	if timeout > 0 {
		i.timeout = time.Now().Add(timeout)
	}

	m.store[key] = i

	return nil
}

// Update updates value for key
func (m *Memory) Update(key, value string) error {
	m.mx.Lock()
	defer m.mx.Unlock()

	i, ok := m.store[key]
	if !ok {
		return ErrNotExists
	}

	if !i.timeout.IsZero() && i.timeout.Before(time.Now()) {
		delete(m.store, key)
		return ErrNotExists
	}

	i.value = value

	return nil
}

// Delete deletes key
func (m *Memory) Delete(key string) error {
	m.mx.Lock()
	defer m.mx.Unlock()

	delete(m.store, key)

	return nil
}

// Get gets value for key
func (m *Memory) Get(key string) (string, error) {
	m.mx.Lock()
	defer m.mx.Unlock()

	i, ok := m.store[key]
	if !ok {
		return "", ErrNotExists
	}

	if !i.timeout.IsZero() && i.timeout.Before(time.Now()) {
		delete(m.store, key)
		return "", ErrNotExists
	}

	return i.value, nil
}
