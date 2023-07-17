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
