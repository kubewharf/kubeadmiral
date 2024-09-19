/*
Copyright 2024 The KubeAdmiral Authors.

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

package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/rest"

	apis "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/util/informermanager/fake"
	"github.com/kubewharf/kubeadmiral/pkg/util/mock"
)

func TestConnectCluster(t *testing.T) {
	const (
		testGroup = "group"
		testUser  = "user"
	)

	s := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/proxy" ||
			req.Header.Get("Impersonate-Group") != testGroup ||
			req.Header.Get("Impersonate-User") != testUser {
			t.Errorf("bad request: %v, %v", req.URL.Path, req.Header)
			return
		}
		fmt.Fprintf(rw, "ok")
	}))
	defer s.Close()

	type args struct {
		ctx     context.Context
		cluster *apis.FederatedCluster
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "apiEndpoint is empty",
			args: args{
				cluster: &apis.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cluster"},
					Spec:       apis.FederatedClusterSpec{},
				},
			},
			wantErr: true,
			want:    "",
		},
		{
			name: "apiEndpoint is invalid",
			args: args{
				cluster: &apis.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cluster"},
					Spec:       apis.FederatedClusterSpec{APIEndpoint: "h :/ invalid"},
				},
			},
			wantErr: true,
			want:    "",
		},
		{
			name: "no user found for request",
			args: args{
				ctx: context.TODO(),
				cluster: &apis.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cluster"},
					Spec: apis.FederatedClusterSpec{
						APIEndpoint: s.URL,
					},
				},
			},
			wantErr: false,
			want:    "Internal Server Error: \"\": no user found for request\n",
		},
		{
			name: "proxy success",
			args: args{
				ctx: request.WithUser(request.NewContext(), &user.DefaultInfo{Name: testUser, Groups: []string{testGroup, user.AllAuthenticated, user.AllUnauthenticated}}),
				cluster: &apis.FederatedCluster{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cluster"},
					Spec: apis.FederatedClusterSpec{
						APIEndpoint: s.URL,
						Insecure:    true,
					},
				},
			},
			wantErr: false,
			want:    "ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.args.ctx
			if ctx == nil {
				ctx = context.TODO()
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/xxx", nil)
			if err != nil {
				t.Fatal(err)
			}

			resp := httptest.NewRecorder()

			informer := &fake.FakeFederatedInformerManager{
				RestConfigs: map[string]*rest.Config{
					"cluster": {
						Host: s.URL,
						TLSClientConfig: rest.TLSClientConfig{
							Insecure: true,
						},
					},
				},
			}

			h, err := ConnectCluster(tt.args.cluster, "proxy", mock.NewResponder(resp), informer)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConnectCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			h.ServeHTTP(resp, req)
			if t.Failed() {
				return
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Error(err)
				return
			}
			if got := string(body); got != tt.want {
				t.Errorf("Connect() got = %v, want %v", got, tt.want)
			}
		})
	}
}
