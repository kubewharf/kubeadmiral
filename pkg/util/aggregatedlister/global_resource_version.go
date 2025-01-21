/*
Copyright 2025 The KubeAdmiral Authors.

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

package aggregatedlister

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"sync"
)

type GlobalResourceVersion struct {
	sync.RWMutex

	clusterRVs map[string]string
	isZero     bool
}

// NewGlobalResourceVersionWithCapacity Only used for Get Verb
func NewGlobalResourceVersionWithCapacity(capacity int) *GlobalResourceVersion {
	return &GlobalResourceVersion{
		clusterRVs: make(map[string]string, capacity),
	}
}

func NewGlobalResourceVersionFromString(s string) *GlobalResourceVersion {
	g := &GlobalResourceVersion{
		clusterRVs: map[string]string{},
	}
	if s == "" {
		return g
	}
	if s == "0" {
		g.isZero = true
		return g
	}

	decoded, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return g
	}
	_ = json.Unmarshal(decoded, &g.clusterRVs)
	return g
}

func (g *GlobalResourceVersion) Set(cluster, rv string) {
	g.Lock()
	defer g.Unlock()
	g.clusterRVs[cluster] = rv
	if rv != "0" {
		g.isZero = false
	}
}

func (g *GlobalResourceVersion) Get(cluster string) string {
	g.RLock()
	defer g.RUnlock()
	if g.isZero {
		return "0"
	}
	return g.clusterRVs[cluster]
}

func (g *GlobalResourceVersion) String() string {
	g.RLock()
	defer g.RUnlock()
	if g.isZero {
		return "0"
	}
	if len(g.clusterRVs) == 0 {
		return ""
	}
	buf := marshalRvs(g.clusterRVs)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func (g *GlobalResourceVersion) Clone() *GlobalResourceVersion {
	ret := &GlobalResourceVersion{
		isZero:     g.isZero,
		clusterRVs: make(map[string]string, len(g.clusterRVs)),
	}
	for k, v := range g.clusterRVs {
		ret.clusterRVs[k] = v
	}
	return ret
}

func marshalRvs(rvs map[string]string) []byte {
	if len(rvs) == 0 {
		return nil
	}

	type onWireRvs struct {
		Cluster         string
		ResourceVersion string
	}

	slice := make([]onWireRvs, 0, len(rvs))

	for clusterName, version := range rvs {
		obj := onWireRvs{clusterName, version}
		slice = append(slice, obj)
	}

	sort.Slice(slice, func(i, j int) bool {
		return slice[i].Cluster < slice[j].Cluster
	})

	encoded := make([]byte, 0)
	encoded = append(encoded, '{')
	for i, n := 0, len(slice); i < n; i++ {
		encoded = append(encoded, '"')
		encoded = append(encoded, slice[i].Cluster...)
		encoded = append(encoded, `":"`...)
		encoded = append(encoded, slice[i].ResourceVersion...)
		encoded = append(encoded, '"')

		if i != n-1 {
			encoded = append(encoded, ',')
		}
	}
	encoded = append(encoded, '}')

	return encoded
}
