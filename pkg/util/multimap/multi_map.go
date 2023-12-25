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

package multimap

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
)

func NewMultiMap[T1, T2 comparable]() *MultiMap[T1, T2] {
	return &MultiMap[T1, T2]{
		lock:      sync.RWMutex{},
		t1ToT2Map: map[T1]sets.Set[T2]{},
		t2ToT1Map: map[T2]T1{},
	}
}

type MultiMap[T1, T2 comparable] struct {
	lock sync.RWMutex

	t1ToT2Map map[T1]sets.Set[T2]
	t2ToT1Map map[T2]T1
}

func (o *MultiMap[T1, T2]) LookupByT1(key T1) (value sets.Set[T2], exists bool) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	val, exists := o.t1ToT2Map[key]

	return val, exists
}

func (o *MultiMap[T1, T2]) LookupByT2(key T2) (value T1, exists bool) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	val, exists := o.t2ToT1Map[key]
	if !exists {
		return *new(T1), false
	}

	return val, true
}

func (o *MultiMap[T1, T2]) Add(t1 T1, t2 T2) error {
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
func (o *MultiMap[T1, T2]) DeleteT1(key T1) bool {
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

func (o *MultiMap[T1, T2]) DeleteT2(key T2) bool {
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
