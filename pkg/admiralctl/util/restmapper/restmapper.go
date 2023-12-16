package restmapper

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

func GetRESTMapper(restConfig *rest.Config) (meta.RESTMapper, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		panic(err.Error())
	}

	grl, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		panic(err.Error())
	}

	return restmapper.NewDiscoveryRESTMapper(grl), nil
}

// ConvertGVKToGVR convert GVK to GVR
func ConvertGVKToGVR(restMapper meta.RESTMapper, gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	restMapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	return restMapping.Resource, nil
}
