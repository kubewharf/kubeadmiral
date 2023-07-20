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

package eventhandlers

import (
	"reflect"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// NewTriggerOnAllChanges returns a cache.ResourceEventHandlerFuncs that will call the given function on all object
// changes. The given function will also be called on receiving cache.DeletedFinalStateUnknown deletion events.
func NewTriggerOnAllChanges(triggerFunc func(runtime.Object)) *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(old interface{}) {
			if deleted, ok := old.(cache.DeletedFinalStateUnknown); ok {
				old = deleted.Obj
				if old == nil {
					return
				}
			}
			oldObj := old.(runtime.Object)
			triggerFunc(oldObj)
		},
		AddFunc: func(cur interface{}) {
			curObj := cur.(runtime.Object)
			triggerFunc(curObj)
		},
		UpdateFunc: func(old, cur interface{}) {
			if !reflect.DeepEqual(old, cur) {
				curObj := cur.(runtime.Object)
				triggerFunc(curObj)
			}
		},
	}
}
