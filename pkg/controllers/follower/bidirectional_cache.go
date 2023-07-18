//go:build exclude
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

package follower

import (
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
)

// TODO: unit test this
func newBidirectionalCache[V1, V2 comparable]() *bidirectionalCache[V1, V2] {
	return &bidirectionalCache[V1, V2]{
		cache:        make(map[V1]sets.Set[V2]),
		reverseCache: make(map[V2]sets.Set[V1]),
	}
}

type bidirectionalCache[V1, V2 comparable] struct {
	sync.RWMutex
	cache        map[V1]sets.Set[V2]
	reverseCache map[V2]sets.Set[V1]
}

func (c *bidirectionalCache[V1, V2]) lookup(key V1) sets.Set[V2] {
	c.RLock()
	defer c.RUnlock()
	return c.cache[key].Clone()
}

func (c *bidirectionalCache[V1, V2]) reverseLookup(key V2) sets.Set[V1] {
	c.RLock()
	defer c.RUnlock()
	return c.reverseCache[key].Clone()
}

func (c *bidirectionalCache[V1, V2]) update(key V1, newValues sets.Set[V2]) {
	c.Lock()
	defer c.Unlock()

	oldValues := c.cache[key]
	if newValues.Len() > 0 {
		c.cache[key] = newValues
	} else {
		delete(c.cache, key)
	}

	staleValues := oldValues.Difference(newValues)
	addedValues := newValues.Difference(oldValues)

	for value := range staleValues {
		keySet, found := c.reverseCache[value]
		if !found {
			continue
		}

		keySet.Delete(key)
		if keySet.Len() == 0 {
			delete(c.reverseCache, value)
		}
	}

	for value := range addedValues {
		keySet, found := c.reverseCache[value]
		if !found {
			keySet = sets.New[V1]()
			c.reverseCache[value] = keySet
		}

		keySet.Insert(key)
	}
}
