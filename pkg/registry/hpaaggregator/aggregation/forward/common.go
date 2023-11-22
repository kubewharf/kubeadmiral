package forward

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	restclient "k8s.io/client-go/rest"
	apiinstall "k8s.io/kubernetes/pkg/apis/core/install"
	"k8s.io/kubernetes/pkg/printers"
	printersinternal "k8s.io/kubernetes/pkg/printers/internalversion"
	printerstorage "k8s.io/kubernetes/pkg/printers/storage"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)

	unversionedVersion = schema.GroupVersion{Group: "", Version: "v1"}
	unversionedTypes   = []runtime.Object{
		&metav1.Status{},
		&metav1.WatchEvent{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	}

	tableConvertor = printerstorage.TableConvertor{
		TableGenerator: printers.NewTableGenerator().With(printersinternal.AddHandlers),
	}
)

func init() {
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})
	apiinstall.Install(scheme)

	scheme.AddUnversionedTypes(unversionedVersion, unversionedTypes...)
}

func NewConfigWithImpersonate(ctx context.Context, config *restclient.Config) (*restclient.Config, error) {
	requester, exist := request.UserFrom(ctx)
	if !exist {
		return nil, errors.New("no user found for request")
	}

	newConfig := restclient.CopyConfig(config)
	newConfig.Impersonate.UserName = requester.GetName()
	for _, group := range requester.GetGroups() {
		if group != user.AllAuthenticated && group != user.AllUnauthenticated {
			newConfig.Impersonate.Groups = append(newConfig.Impersonate.Groups, group)
		}
	}
	return newConfig, nil
}
