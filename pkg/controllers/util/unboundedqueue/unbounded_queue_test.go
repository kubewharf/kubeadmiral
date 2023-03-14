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

package unboundedqueue_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubewharf/kubeadmiral/pkg/controllers/util/unboundedqueue"
)

func TestOverflow(t *testing.T) {
	assert := assert.New(t)

	uq := unboundedqueue.New(3)
	recv := []interface{}{}

	uq.Send(int(1))
	recv = append(recv, <-uq.Receiver())

	uq.Send(int(2))
	uq.Send(int(3))
	uq.Send(int(4))
	recv = append(recv, <-uq.Receiver())
	recv = append(recv, <-uq.Receiver())
	recv = append(recv, <-uq.Receiver())

	uq.Send(int(5))
	uq.Send(int(6))
	uq.Send(int(7))
	uq.Send(int(8))
	uq.Send(int(9))
	recv = append(recv, <-uq.Receiver())
	recv = append(recv, <-uq.Receiver())
	recv = append(recv, <-uq.Receiver())
	recv = append(recv, <-uq.Receiver())
	recv = append(recv, <-uq.Receiver())

	for i, v := range recv {
		assert.Equal(i, v.(int)-1)
	}
}
