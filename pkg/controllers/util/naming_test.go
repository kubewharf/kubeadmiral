package util

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateFederatedObjectName(t *testing.T) {
	type args struct {
		objectName string
		ftcName    string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "generate federated object name",
			args: args{
				objectName: "foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "foo-roles.rbac.authorization.k8s.io",
		},
		{
			name: "generate federated object name with :",
			args: args{
				objectName: "system:foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-2728495308",
		},
		{
			name: "generate federated object name with $",
			args: args{
				objectName: "system$foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-4258037882",
		},
		{
			name: "generate federated object name with %",
			args: args{
				objectName: "system%foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-1244789457",
		},
		{
			name: "generate federated object name with #",
			args: args{
				objectName: "system#foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-1128546011",
		},
		{
			name: "generate federated object name with upper case letter",
			args: args{
				objectName: "system#Foo",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo-roles.rbac.authorization.k8s.io-1133665787",
		},
		{
			name: "generate federated object name with number",
			args: args{
				objectName: "system.foo123",
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: "system.foo123-roles.rbac.authorization.k8s.io",
		},
		{
			name: "generate federated object name for source object with long name",
			args: args{
				objectName: strings.Repeat("foo", 80),
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: strings.Repeat("foo", 80) + "-r-3980386512",
		},
		{
			name: "generate federated object name with transformation and truncation",
			args: args{
				objectName: strings.Repeat("system#foo", 25),
				ftcName:    "roles.rbac.authorization.k8s.io",
			},
			want: strings.Repeat("system.foo", 24) + "sys-552681660",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, GenerateFederatedObjectName(tt.args.objectName, tt.args.ftcName), "GenerateFederatedObjectName(%v, %v)", tt.args.ftcName, tt.args.objectName)
		})
	}
}
