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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"k8s.io/apiserver/pkg/audit"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/rest"
)

func TestNewRequestForProxy(t *testing.T) {
	auditCtx := audit.WithAuditContext(request.NewContext())
	audit.WithAuditID(auditCtx, "123")
	req := httptest.NewRequest("", "https://192.168.0.1:443", nil)
	reqWithAudit := req.WithContext(auditCtx)

	type args struct {
		location *url.URL
		req      *http.Request
		info     *request.RequestInfo
	}
	tests := []struct {
		name       string
		args       args
		cancelable bool
	}{
		{
			name: "resource request",
			args: args{
				location: &url.URL{Host: "1.2.3.4"},
				req:      reqWithAudit,
				info:     &request.RequestInfo{IsResourceRequest: true, Path: "/1/2/3/4"},
			},
			cancelable: false,
		},
		{
			name: "non-resource request",
			args: args{
				location: &url.URL{Host: "1.2.3.4"},
				req:      reqWithAudit,
				info:     &request.RequestInfo{IsResourceRequest: false, Path: "/1/2/3/"},
			},
			cancelable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cancel := NewRequestForProxy(tt.args.location, tt.args.req, tt.args.info)
			cancel()
			ctx := got.Context()
			select {
			case <-ctx.Done():
				if !tt.cancelable {
					t.Errorf("should not be canceled")
				}
			default:
				if tt.cancelable {
					t.Errorf("should be canceled")
				}
			}
		})
	}
}

func Test_forwardHandler_Handler(t *testing.T) {
	isHPA := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isHPA && !strings.Contains(r.URL.Query().Get(labelSelectorQueryKey), labelSelectorQueryValue) {
			w.WriteHeader(500)
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	user := &user.DefaultInfo{
		Name:   "test",
		Groups: []string{"group1", "group2"},
	}
	config := &rest.Config{
		Host: ts.URL,
	}

	type fields struct {
		config *rest.Config
	}
	type args struct {
		info  *request.RequestInfo
		isHPA bool
	}
	tests := []struct {
		name     string
		fields   fields
		args     args
		rawQuery string
	}{
		// TODO: Add test cases.
		{
			name: "get hpa",
			fields: fields{
				config: config,
			},
			args: args{
				info:  &request.RequestInfo{Verb: "get", IsResourceRequest: true},
				isHPA: true,
			},
		},
		{
			name: "list hpa with label selector",
			fields: fields{
				config: config,
			},
			args: args{
				info:  &request.RequestInfo{Verb: "list", IsResourceRequest: true},
				isHPA: true,
			},
			rawQuery: "labelSelector=aa%3Dbb",
		},
		{
			name: "non hpa",
			fields: fields{
				config: config,
			},
			args: args{
				info:  &request.RequestInfo{Verb: "list", IsResourceRequest: true},
				isHPA: false,
			},
			rawQuery: "labelSelector=aa%3Dbb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &forwardHandler{
				config: tt.fields.config,
			}
			isHPA = tt.args.isHPA
			handler, err := f.Handler(tt.args.info, tt.args.isHPA)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			w := httptest.NewRecorder()
			ctx := request.WithUser(request.NewContext(), user)
			req := httptest.NewRequest("", "http://a.b.c", nil)
			req = req.WithContext(ctx)
			req.URL.RawQuery = tt.rawQuery

			handler.ServeHTTP(w, req)

			if tt.args.isHPA && w.Code == 500 {
				t.Errorf("should query with label selector")
			}
		})
	}
}

func Test_responder_Error(t *testing.T) {
	r := &responder{
		info: nil,
		user: rest.ImpersonationConfig{},
	}

	w := httptest.NewRecorder()
	err := "fake err"
	r.Error(w, nil, errors.New(err))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("Error() got code = %v, want code: %v", w.Code, http.StatusInternalServerError)
	}
	if e := strings.TrimSpace(w.Body.String()); e != err {
		t.Errorf("Error() got body = %v, want body: %v", e, err)
	}
}
