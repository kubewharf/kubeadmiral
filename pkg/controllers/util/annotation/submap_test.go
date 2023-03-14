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

package annotation_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/annotation"
)

func TestCopySubmap(t *testing.T) {
	testCases := map[string]struct {
		src  map[string]string
		dest map[string]string
		keys []string

		newDest    map[string]string
		numChanges int
	}{
		"src and dest both nil": {
			src:        nil,
			dest:       nil,
			keys:       []string{"foo"},
			newDest:    nil,
			numChanges: 0,
		},
		"src is nil, keys should be removed from dest": {
			src:        nil,
			dest:       map[string]string{"foo": "bar", "qux": "corge"},
			keys:       []string{"foo"},
			newDest:    map[string]string{"qux": "corge"},
			numChanges: 1,
		},
		"dest is nil, keys should be added to dest": {
			src:        map[string]string{"foo": "bar"},
			dest:       nil,
			keys:       []string{"foo"},
			newDest:    map[string]string{"foo": "bar"},
			numChanges: 1,
		},
		"keys in dest should be added and removed": {
			src:        map[string]string{"foo": "bar"},
			dest:       map[string]string{"qux": "corge", "grault": "waldo"},
			keys:       []string{"foo", "qux"},
			newDest:    map[string]string{"foo": "bar", "grault": "waldo"},
			numChanges: 2,
		},
		"key is different in src and dest": {
			src:        map[string]string{"foo": "bar"},
			dest:       map[string]string{"foo": "waldo", "qux": "corge"},
			keys:       []string{"foo"},
			newDest:    map[string]string{"foo": "bar", "qux": "corge"},
			numChanges: 1, // make sure we don't double count
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			newDest, numChanges := annotation.CopySubmap(testCase.src, testCase.dest, func(key string) bool {
				for _, expectKey := range testCase.keys {
					if expectKey == key {
						return true
					}
				}
				return false
			})
			assert.Truef(reflect.DeepEqual(testCase.newDest, newDest), "Expect newDest %v to be %v", newDest, testCase.newDest)
			assert.Equalf(testCase.numChanges, numChanges, "Expect numChanges %s to be %s", numChanges, testCase.numChanges)
		})
	}
}
