package bijection

import (
	"fmt"
	"sync"
)

func NewBijection[T1, T2 comparable]() *Bijection[T1, T2] {
	return &Bijection[T1, T2]{
		lock:      sync.RWMutex{},
		t1ToT2Map: map[T1]T2{},
		t2ToT1Map: map[T2]T1{},
	}
}

type Bijection[T1, T2 comparable] struct {
	lock sync.RWMutex

	t1ToT2Map map[T1]T2
	t2ToT1Map map[T2]T1
}

func (m *Bijection[T1, T2]) LookupByT1(key T1) (value T2, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	val, exists := m.t1ToT2Map[key]
	if !exists {
		return *new(T2), false
	}

	return val, true
}

func (m *Bijection[T1, T2]) LookupByT2(key T2) (value T1, exists bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	val, exists := m.t2ToT1Map[key]
	if !exists {
		return *new(T1), false
	}

	return val, true
}

func (m *Bijection[T1, T2]) Add(t1 T1, t2 T2) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if val, ok := m.t1ToT2Map[t1]; ok {
		return fmt.Errorf("%v is already mapped to %v", t1, val)
	}

	if val, ok := m.t2ToT1Map[t2]; ok {
		return fmt.Errorf("%v is already mapped to %v", t2, val)
	}

	m.t1ToT2Map[t1] = t2
	m.t2ToT1Map[t2] = t1

	return nil
}

func (m *Bijection[T1, T2]) DeleteT1(key T1) bool {
	m.lock.Lock()
	defer m.lock.Unlock()

	val, ok := m.t1ToT2Map[key]
	if !ok {
		return false
	}

	delete(m.t1ToT2Map, key)
	delete(m.t2ToT1Map, val)

	return true
}

func (m *Bijection[T1, T2]) DeleteT2(key T2) bool {
	m.lock.Lock()
	defer m.lock.Unlock()

	val, ok := m.t2ToT1Map[key]
	if !ok {
		return false
	}

	delete(m.t2ToT1Map, key)
	delete(m.t1ToT2Map, val)

	return true
}
