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

package forward

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/handlers"
	"k8s.io/apiserver/pkg/endpoints/request"
	restclient "k8s.io/client-go/rest"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/printers"
	"github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/printers/internalversion"
	printerstorage "github.com/kubewharf/kubeadmiral/pkg/lifted/kubernetes/pkg/printers/storage"
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
		TableGenerator: printers.NewTableGenerator().With(internalversion.AddHandlers),
	}
	scope = &handlers.RequestScope{
		Namer: &handlers.ContextBasedNaming{
			Namer:         runtime.Namer(meta.NewAccessor()),
			ClusterScoped: false,
		},
		Serializer:       codecs,
		Kind:             common.PodGVK,
		TableConvertor:   tableConvertor,
		Convertor:        scheme,
		MetaGroupVersion: metav1.SchemeGroupVersion,
		Resource:         common.PodGVR,
	}
)

func init() {
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(corev1.SchemeGroupVersion))

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
