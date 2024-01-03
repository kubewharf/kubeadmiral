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

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

const ClusterResourcePluginsAnnotationKey = common.DefaultPrefix + "cluster-resource-plugins"

type ClusterHandle struct {
	DynamicLister map[schema.GroupVersionResource]cache.GenericLister
}

type Plugin interface {
	CollectClusterResources(
		ctx context.Context,
		nodes []*corev1.Node,
		pods []*corev1.Pod,
		handle ClusterHandle,
	) (allocatable, available corev1.ResourceList, err error)

	ClusterResourcesToCollect() sets.Set[schema.GroupVersionResource]
}

var defaultPlugins = map[string]Plugin{}

func ResolvePlugins(annotations map[string]string) (map[string]Plugin, error) {
	if len(annotations) == 0 || annotations[ClusterResourcePluginsAnnotationKey] == "" {
		return nil, nil
	}
	pluginNames := []string{}
	err := json.Unmarshal([]byte(annotations[ClusterResourcePluginsAnnotationKey]), &pluginNames)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal plugins from %s: %w",
			annotations[ClusterResourcePluginsAnnotationKey], err)
	}

	plugins := map[string]Plugin{}
	for _, name := range pluginNames {
		if plugin, exists := defaultPlugins[name]; exists {
			plugins[name] = plugin
		}
	}
	return plugins, nil
}

func PluginsHash(plugins map[string]Plugin) string {
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)

	hash := fnv.New64()
	_, _ = hash.Write([]byte(strings.Join(names, ",")))
	return strconv.FormatUint(hash.Sum64(), 16)
}
