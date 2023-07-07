package tools

import (
	"fmt"
	"sync"
)

func NewBijectionMap[T1, T2 comparable]() *BijectionMap[T1, T2] {
	return &BijectionMap[T1, T2]{
		lock:       sync.RWMutex{},
		forwardMap: map[T1]T2{},
		reverseMap: map[T2]T1{},
	}
}

type BijectionMap[T1, T2 comparable] struct {
	lock sync.RWMutex

	forwardMap map[T1]T2
	reverseMap map[T2]T1
}

func (m *BijectionMap[T1, T2]) Lookup(key T1) (value T2, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	val, exists := m.forwardMap[key]
	if !exists {
		return *new(T2), false
	}

	return val, true
}

func (m *BijectionMap[T1, T2]) ReverseLookup(key T2) (value T1, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	val, exists := m.reverseMap[key]
	if !exists {
		return *new(T1), false
	}

	return val, true
}

func (m *BijectionMap[T1, T2]) Add(key1 T1, key2 T2) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if val, ok := m.forwardMap[key1]; ok {
		return fmt.Errorf("%v is already mapped to %v", key1, val)
	}

	if val, ok := m.reverseMap[key2]; ok {
		return fmt.Errorf("%v is already mapped to %v", key2, val)
	}

	m.forwardMap[key1] = key2
	m.reverseMap[key2] = key1

	return nil
}

func (m *BijectionMap[T1, T2]) Delete(key T1) bool {
	m.lock.Lock()
	defer m.lock.Unlock()

	val, ok := m.forwardMap[key]
	if !ok {
		return false
	}

	delete(m.forwardMap, key)
	delete(m.reverseMap, val)

	return true
}

func (m *BijectionMap[T1, T2]) ReverseDelete(key T2) bool {
	m.lock.Lock()
	defer m.lock.Unlock()

	val, ok := m.reverseMap[key]
	if !ok {
		return false
	}

	delete(m.reverseMap, key)
	delete(m.forwardMap, val)

	return true
}
