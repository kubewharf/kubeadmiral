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

package custommatchers

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/onsi/gomega/format"
	"k8s.io/apimachinery/pkg/api/equality"
)

type SemanticEqualityMatcher struct {
	Expected interface{}
}

func SemanticallyEqual(expected interface{}) *SemanticEqualityMatcher {
	return &SemanticEqualityMatcher{
		Expected: expected,
	}
}

func (matcher *SemanticEqualityMatcher) Match(actual interface{}) (success bool, err error) {
	return equality.Semantic.DeepEqual(actual, matcher.Expected), nil
}

func (matcher *SemanticEqualityMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf(
		"Expected\n%s\nto be semantically equal to\n%s\ndiff:\n%s",
		format.Object(actual, 1),
		format.Object(matcher.Expected, 1),
		cmp.Diff(actual, matcher.Expected),
	)
}

func (matcher *SemanticEqualityMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return format.Message(actual, "not to be semantically equal to", matcher.Expected)
}
