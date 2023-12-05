package forward

import (
	"context"
	"reflect"
	"testing"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	restclient "k8s.io/client-go/rest"
)

func TestNewConfigWithImpersonate(t *testing.T) {
	user := &user.DefaultInfo{
		Name:   "test",
		UID:    "123",
		Groups: []string{"group1", "group2", "system:unauthenticated", "system:authenticated"},
		Extra:  map[string][]string{"aa": {"bb"}},
	}

	type args struct {
		ctx    context.Context
		config *restclient.Config
	}
	tests := []struct {
		name    string
		args    args
		want    *restclient.Config
		wantErr bool
	}{
		{
			name: "normal",
			args: args{
				ctx:    request.WithUser(request.NewContext(), user),
				config: &restclient.Config{},
			},
			want: &restclient.Config{Impersonate: restclient.ImpersonationConfig{
				UserName: "test",
				Groups:   []string{"group1", "group2"},
			}},
			wantErr: false,
		},
		{
			name: "no user err",
			args: args{
				ctx:    request.NewContext(),
				config: &restclient.Config{},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewConfigWithImpersonate(tt.args.ctx, tt.args.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewConfigWithImpersonate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewConfigWithImpersonate() got = %v, want %v", got, tt.want)
			}
		})
	}
}
