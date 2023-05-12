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

package policyrc

import (
	"fmt"
	"sync"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
)

type (
	ObjectKey common.QualifiedName
	PolicyKey common.QualifiedName
	PolicySet []PolicyKey
)

type Counter struct {
	// For now this uses a global mutex.
	// We can use sync.Map.swap on knownPolicies and shard policyCounts if performance is an issue.
	mu                   sync.Mutex
	knownPolicies        map[ObjectKey]PolicySet
	policyCounts         map[PolicyKey]int64
	flagPolicyNeedUpdate func([]PolicyKey)
}

func NewCounter(flagPolicyNeedUpdate func([]PolicyKey)) *Counter {
	return &Counter{
		knownPolicies:        map[ObjectKey]PolicySet{},
		policyCounts:         map[PolicyKey]int64{},
		flagPolicyNeedUpdate: flagPolicyNeedUpdate,
	}
}

// Observe an update to the policy set referenced by an object.
func (counter *Counter) Update(object ObjectKey, newPolicies PolicySet) {
	var flaggedPolicies []PolicyKey
	defer func() {
		// Execute the flag after mutex unlock to reduce contention
		counter.flagPolicyNeedUpdate(flaggedPolicies)
	}()

	counter.mu.Lock()
	defer counter.mu.Unlock()

	previousPolicies := counter.knownPolicies[object]
	if len(newPolicies) > 0 {
		counter.knownPolicies[object] = newPolicies
	} else {
		delete(counter.knownPolicies, object)
	}

	for _, previousPolicy := range previousPolicies {
		counter.policyCounts[previousPolicy] -= 1
		if counter.policyCounts[previousPolicy] < 0 {
			// this should never happen because this field has been incremented in the previous assignment to knownPolicies[key]
			panic(fmt.Sprintf("counter.policyCounts[%v] must be non-negative", previousPolicy))
		}
		flaggedPolicies = append(flaggedPolicies, previousPolicy)
	}

	for _, newPolicy := range newPolicies {
		counter.policyCounts[newPolicy] += 1
	}
	flaggedPolicies = append(flaggedPolicies, newPolicies...)
}

func (counter *Counter) GetPolicyCounts(keys []PolicyKey) []int64 {
	counter.mu.Lock()
	defer counter.mu.Unlock()

	results := make([]int64, len(keys))
	for i, key := range keys {
		results[i] = counter.policyCounts[key] // assume to be 0 if empty
	}

	return results
}
