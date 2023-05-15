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

package policyrc_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/policyrc"
)

const (
	NumGoroutines  = 8
	NumPolicies    = 16
	NumObjects     = 64
	NumRepetitions = 4
)

// In this test, the counter observes that each object is updated to use different policies concurrently.
// We expect that each object is mapped to the same or different policy eventually,
// but the sum of refcounts should be equal to the number of objects.
func TestUpdate(t *testing.T) {
	assert := assert.New(t)

	var flagged sync.Map

	counter := policyrc.NewCounter(func(keys []policyrc.PolicyKey) {
		for _, key := range keys {
			flagged.Store(key, struct{}{})
		}
	})

	type update struct {
		name   policyrc.ObjectKey
		policy []policyrc.PolicyKey
	}

	updates := make([]update, 0, NumPolicies*NumObjects)
	for policy := 0; policy < NumPolicies; policy++ {
		for object := 0; object < NumObjects; object++ {
			for repetition := 0; repetition < NumRepetitions; repetition++ {
				updates = append(updates, update{
					name:   policyrc.ObjectKey{Name: fmt.Sprint(object)},
					policy: []policyrc.PolicyKey{{Name: fmt.Sprint(policy)}},
				})
			}
		}
	}

	rand.Shuffle(len(updates), func(i, j int) {
		tmp := updates[i]
		updates[i] = updates[j]
		updates[j] = tmp
	})

	var wg sync.WaitGroup
	for goroutineId := 0; goroutineId < NumGoroutines; goroutineId++ {
		wg.Add(1)
		go func(goroutineId int) {
			defer wg.Done()

			keys := make([]policyrc.PolicyKey, NumPolicies)
			for policyId := range keys {
				keys[policyId].Name = fmt.Sprint(policyId)
			}

			batchSize := len(updates) / NumGoroutines
			batch := updates[(batchSize * goroutineId):(batchSize * (goroutineId + 1))]
			for _, update := range batch {
				counter.Update(update.name, update.policy)
			}
		}(goroutineId)
	}

	wg.Wait()

	keys := []policyrc.PolicyKey{}
	flagged.Range(func(key, value interface{}) (continue_ bool) {
		keys = append(keys, key.(policyrc.PolicyKey))
		return true
	})
	assert.Equal(NumPolicies, len(keys))

	sum := int64(0)
	for _, value := range counter.GetPolicyCounts(keys) {
		sum += value
	}
	assert.Equal(sum, int64(NumObjects))
}
