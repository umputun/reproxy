package memory

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultCleanupInterval = time.Minute * 15

var (
	ErrNotExists = fmt.Errorf("not exists")
)

type item struct {
	value   string
	timeout time.Time
}

type Memory struct {
	mx    *sync.Mutex
	store map[string]*item
}

func New() *Memory {
	m := &Memory{
		mx:    &sync.Mutex{},
		store: map[string]*item{},
	}

	return m
}

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

func (m *Memory) Delete(key string) error {
	m.mx.Lock()
	defer m.mx.Unlock()

	delete(m.store, key)

	return nil
}

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
