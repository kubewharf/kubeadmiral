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

package scheduler

import (
	"reflect"
	"testing"

	fedcore "github.com/kubewharf/kubeadmiral/pkg/apis/core"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
)

func getBase() *fedcore.EnabledPlugins {
	return &fedcore.EnabledPlugins{
		FilterPlugins:   []string{"a", "b", "c"},
		ScorePlugins:    []string{"a", "b", "c"},
		SelectPlugins:   []string{"a", "b", "c"},
		ReplicasPlugins: []string{"a", "b", "c"},
	}
}

func TestApplyProfile(t *testing.T) {
	tests := []struct {
		name           string
		base           *fedcore.EnabledPlugins
		profile        *fedcorev1a1.SchedulingProfile
		expectedResult *fedcore.EnabledPlugins
	}{
		{
			name: "enable filter plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Filter: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "d",
								},
								{
									Name: "e",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c", "d", "e"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable some filter plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Filter: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"b"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable all filter plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Filter: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable filter plugins and disable some filter plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Filter: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "y",
								},
								{
									Name: "z",
								},
							},
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"b", "y", "z"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable filter plugins and disable all filter plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Filter: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "y",
								},
								{
									Name: "z",
								},
							},
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"y", "z"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable score plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Score: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "d",
								},
								{
									Name: "e",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"a", "b", "c", "d", "e"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable some score plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Score: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"b"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable all score plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Score: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable score plugins and disable some score plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Score: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "y",
								},
								{
									Name: "z",
								},
							},
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"b", "y", "z"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable score plugins and disable all score plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Score: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "y",
								},
								{
									Name: "z",
								},
							},
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"y", "z"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable select plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Select: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "d",
								},
								{
									Name: "e",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"a", "b", "c", "d", "e"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable some select plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Select: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"b"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable all select plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Select: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable select plugins and disable some select plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Select: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "y",
								},
								{
									Name: "z",
								},
							},
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"b", "y", "z"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "enable select plugins and disable all select plugins",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Select: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "y",
								},
								{
									Name: "z",
								},
							},
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "*",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"y", "z"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable and enable multiple extension points 1",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Filter: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "*",
								},
							},
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "h",
								},
								{
									Name: "i",
								},
								{
									Name: "j",
								},
							},
						},
						Select: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "y",
								},
								{
									Name: "z",
								},
							},
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"h", "i", "j"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"b", "c", "y", "z"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable and enable multiple extension points 2",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Score: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "a",
								},
								{
									Name: "b",
								},
								{
									Name: "c",
								},
							},
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "z",
								},
							},
						},
						Select: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"z"},
				SelectPlugins:   []string{"a", "b"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name: "disable and enable multiple extension points 3",
			base: getBase(),
			profile: &fedcorev1a1.SchedulingProfile{
				Spec: fedcorev1a1.SchedulingProfileSpec{
					Plugins: &fedcorev1a1.Plugins{
						Filter: fedcorev1a1.PluginSet{
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "m",
								},
							},
						},
						Score: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "c",
								},
							},
							Enabled: []fedcorev1a1.Plugin{
								{
									Name: "z",
								},
							},
						},
						Select: fedcorev1a1.PluginSet{
							Disabled: []fedcorev1a1.Plugin{
								{
									Name: "c",
								},
							},
						},
					},
				},
			},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c", "m"},
				ScorePlugins:    []string{"a", "b", "z"},
				SelectPlugins:   []string{"a", "b"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
		{
			name:    "empty profile",
			base:    getBase(),
			profile: &fedcorev1a1.SchedulingProfile{},
			expectedResult: &fedcore.EnabledPlugins{
				FilterPlugins:   []string{"a", "b", "c"},
				ScorePlugins:    []string{"a", "b", "c"},
				SelectPlugins:   []string{"a", "b", "c"},
				ReplicasPlugins: []string{"a", "b", "c"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applyProfile(test.base, test.profile)

			if !reflect.DeepEqual(test.base.FilterPlugins, test.expectedResult.FilterPlugins) {
				t.Errorf(
					"expected filter plugins to be %v, but got %v",
					test.base.FilterPlugins,
					test.expectedResult.FilterPlugins,
				)
			}
			if !reflect.DeepEqual(test.base.ScorePlugins, test.expectedResult.ScorePlugins) {
				t.Errorf("expected score plugins to be %v, but got %v", test.base.ScorePlugins, test.expectedResult.ScorePlugins)
			}
			if !reflect.DeepEqual(test.base.SelectPlugins, test.expectedResult.SelectPlugins) {
				t.Errorf(
					"expected select plugins to be %v, but got %v",
					test.base.SelectPlugins,
					test.expectedResult.SelectPlugins,
				)
			}
			if !reflect.DeepEqual(test.base.ReplicasPlugins, test.expectedResult.ReplicasPlugins) {
				t.Errorf(
					"expected replicas plugins to be %v, but got %v",
					test.base.ReplicasPlugins,
					test.expectedResult.ReplicasPlugins,
				)
			}
		})
	}
}
