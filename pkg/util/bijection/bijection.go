/*
Copyright 2023 The KubeAdmiral Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package bijection

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
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

func NewOneToManyRelation[T1, T2 comparable]() *OneToManyRelation[T1, T2] {
	return &OneToManyRelation[T1, T2]{
		lock:      sync.RWMutex{},
		t1ToT2Map: map[T1]sets.Set[T2]{},
		t2ToT1Map: map[T2]T1{},
	}
}

type OneToManyRelation[T1, T2 comparable] struct {
	lock sync.RWMutex

	t1ToT2Map map[T1]sets.Set[T2]
	t2ToT1Map map[T2]T1
}

func (o *OneToManyRelation[T1, T2]) LookupByT1(key T1) (value sets.Set[T2], exists bool) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	val, exists := o.t1ToT2Map[key]
	if !exists {
		return nil, false
	}

	return val, true
}

func (o *OneToManyRelation[T1, T2]) LookupByT2(key T2) (value T1, exists bool) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	val, exists := o.t2ToT1Map[key]
	if !exists {
		return *new(T1), false
	}

	return val, true
}

func (o *OneToManyRelation[T1, T2]) Add(t1 T1, t2 T2) error {
	o.lock.Lock()
	defer o.lock.Unlock()

	if val, ok := o.t1ToT2Map[t1]; ok {
		if val.Has(t2) {
			return fmt.Errorf("%v is already mapped to %v", t1, t2)
		}
	}

	if val, ok := o.t2ToT1Map[t2]; ok {
		return fmt.Errorf("%v is already mapped to %v", t2, val)
	}

	set := o.t1ToT2Map[t1]
	if set == nil {
		set = sets.New(t2)
	}
	set.Insert(t2)
	o.t1ToT2Map[t1] = set

	o.t2ToT1Map[t2] = t1

	return nil
}

// DeleteT1 It is a dangerous action, please use DeleteT2 instead of it.
func (o *OneToManyRelation[T1, T2]) DeleteT1(key T1) bool {
	o.lock.Lock()
	defer o.lock.Unlock()

	val, ok := o.t1ToT2Map[key]
	if !ok {
		return false
	}

	delete(o.t1ToT2Map, key)
	for subVal := range val {
		delete(o.t2ToT1Map, subVal)
	}

	return true
}

func (o *OneToManyRelation[T1, T2]) DeleteT2(key T2) bool {
	o.lock.Lock()
	defer o.lock.Unlock()

	val, ok := o.t2ToT1Map[key]
	if !ok {
		return false
	}

	delete(o.t2ToT1Map, key)
	o.t1ToT2Map[val].Delete(key)

	if len(o.t1ToT2Map[val]) == 0 {
		delete(o.t1ToT2Map, val)
	}

	return true
}
