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

package annotation

// CopySubmap copies keys matched by matcher from src to dest and returns number of entries changed in dest.
// src and dest may be nil.
// If dest is non-nil, dest == newDest. Otherwise, if any key in keys exist in src, newDest is a new map.
func CopySubmap(src, dest map[string]string, matcher func(key string) bool) (newDest map[string]string, numChanges int) {
	iteration := func(key string) {
		srcValue, srcExists := src[key]
		destValue, destExists := dest[key]

		if srcExists && (!destExists || srcValue != destValue) {
			if dest == nil {
				dest = make(map[string]string)
			}

			dest[key] = srcValue
			numChanges += 1
		} else if !srcExists && destExists {
			delete(dest, key)
			numChanges += 1
		}
	}

	for key := range src {
		if matcher(key) {
			iteration(key)
		}
	}

	// if the same key is in both src and dest, it is already synced in dest, so we don't need to worry about double count.
	for key := range dest {
		if matcher(key) {
			iteration(key)
		}
	}

	return dest, numChanges
}
