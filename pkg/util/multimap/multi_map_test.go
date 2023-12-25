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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMultiMap(t *testing.T) {
	relation := NewMultiMap[int, string]()

	// 1. add data to MultiMap
	// add key value [[1,A],[1,B],[2,C]]
	err := relation.Add(1, "A")
	assert.NoError(t, err)
	err = relation.Add(1, "B")
	assert.NoError(t, err)
	err = relation.Add(2, "C")
	assert.NoError(t, err)

	// add same key value [1,A], return error
	err = relation.Add(1, "A")
	assert.Error(t, err)

	// 2.check data in MultiMap
	// check LookupByT1
	set, exists := relation.LookupByT1(1)
	assert.True(t, exists)
	assert.Equal(t, 2, set.Len())
	assert.True(t, set.Has("A"))
	assert.True(t, set.Has("B"))

	set, exists = relation.LookupByT1(2)
	assert.True(t, exists)
	assert.Equal(t, 1, set.Len())
	assert.True(t, set.Has("C"))

	// query key which not exist in T1, return false
	_, exists = relation.LookupByT1(3)
	assert.False(t, exists)

	// check LookupByT2
	value, exists := relation.LookupByT2("A")
	assert.True(t, exists)
	assert.Equal(t, 1, value)

	value, exists = relation.LookupByT2("C")
	assert.True(t, exists)
	assert.Equal(t, 2, value)

	// query key which not exist in T2, return false
	_, exists = relation.LookupByT2("D")
	assert.False(t, exists)

	// 	3.delete data in MultiMap
	// delete key[1] in T1 and key [C] in T2
	deleted := relation.DeleteT1(1)
	assert.True(t, deleted)

	deleted = relation.DeleteT2("C")
	assert.True(t, deleted)

	// check LookupByT1 and LookupByT2
	_, exists = relation.LookupByT1(1)
	assert.False(t, exists)

	_, exists = relation.LookupByT2("C")
	assert.False(t, exists)

	// delete key which not exist in T1, return false
	deleted = relation.DeleteT1(5)
	assert.False(t, deleted)

	// delete key which not exist in T2, return false
	deleted = relation.DeleteT2("F")
	assert.False(t, deleted)
}
