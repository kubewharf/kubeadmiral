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

package util

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedtypesv1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/types/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	utilunstructured "github.com/kubewharf/kubeadmiral/pkg/controllers/util/unstructured"
)

const (
	ReplicaPath        = "/spec/replicas"
	MaxSurgePath       = "/spec/strategy/rollingUpdate/maxSurge"
	MaxUnavailablePath = "/spec/strategy/rollingUpdate/maxUnavailable"
	Nil                = "nil"
)

var (
	MaxSurgePathSlice = []string{
		common.SpecField,
		common.StrategyField,
		common.RollingUpdateField,
		common.MaxSurgeField,
	}
	MaxUnavailablePathSlice = []string{
		common.SpecField,
		common.StrategyField,
		common.RollingUpdateField,
		common.MaxUnavailableField,
	}
)

type RolloutPlan struct {
	Replicas          *int32
	MaxSurge          *int32
	MaxUnavailable    *int32
	OnlyPatchReplicas bool
}

func (p RolloutPlan) String() string {
	r, s, u := Nil, Nil, Nil
	if p.Replicas != nil {
		r = fmt.Sprintf("%d", *p.Replicas)
	}
	if p.MaxSurge != nil {
		s = fmt.Sprintf("%d", *p.MaxSurge)
	}
	if p.MaxUnavailable != nil {
		u = fmt.Sprintf("%d", *p.MaxUnavailable)
	}
	return fmt.Sprintf("%s,%s,%s,%t", r, s, u, p.OnlyPatchReplicas)
}

func (p RolloutPlan) toOverrides() fedtypesv1a1.OverridePatches {
	overrides := fedtypesv1a1.OverridePatches{}
	if p.Replicas != nil {
		overrides = append(overrides, fedtypesv1a1.OverridePatch{Path: ReplicaPath, Value: *p.Replicas})
	}
	if p.MaxSurge != nil {
		overrides = append(overrides, fedtypesv1a1.OverridePatch{Path: MaxSurgePath, Value: *p.MaxSurge})
	}
	if p.MaxUnavailable != nil {
		overrides = append(overrides, fedtypesv1a1.OverridePatch{Path: MaxUnavailablePath, Value: *p.MaxUnavailable})
	}
	return overrides
}

func (p *RolloutPlan) correctFencepost(t *TargetInfo, defaultIsSurge bool) {
	completed := t.UpdateCompleted()
	isSurge := t.IsSurge()
	flip := t.Flip(defaultIsSurge)

	if completed && !flip {
		// If the new replica set is saturated, set maxSurge & maxUnavailable to the final value.
		// If there are unavailable instances in the new replica set, they will be part of maxUnavailable
		p.MaxSurge = nil
		p.MaxUnavailable = nil
	} else if *p.MaxSurge == 0 && *p.MaxUnavailable == 0 {
		// Like deployment controller, we set one of them to one if both maxSurge & maxUnavailable is zero
		var one int32 = 1
		if isSurge {
			p.MaxSurge = &one
		} else {
			p.MaxUnavailable = &one
		}
	}
}

type RolloutPlans map[string]*RolloutPlan

func (r RolloutPlans) String() string {
	var strs []string
	for k, v := range r {
		strs = append(strs, fmt.Sprintf("%s:%v", k, v))
	}
	return strings.Join(strs, "; ")
}

func (r RolloutPlans) GetRolloutOverrides(clusterName string) fedtypesv1a1.OverridePatches {
	p, ok := r[clusterName]
	if !ok {
		return fedtypesv1a1.OverridePatches{}
	}
	return p.toOverrides()
}

type Targets []*TargetInfo

func (s Targets) CurrentReplicas() int32 {
	var currentReplicas int32
	for _, t := range s {
		currentReplicas += t.Status.Replicas
	}
	return currentReplicas
}

func (s Targets) DesiredReplicas() int32 {
	var desiredReplicas int32
	for _, t := range s {
		desiredReplicas += t.DesiredReplicas
	}
	return desiredReplicas
}

func (s Targets) AvailableReplicas() int32 {
	var totalAvailable int32
	for _, t := range s {
		totalAvailable += t.Status.AvailableReplicas
	}
	return totalAvailable
}

func (s Targets) ActualReplicas() int32 {
	var totalActual int32
	for _, t := range s {
		totalActual += t.Status.ActualReplicas
	}
	return totalActual
}

type TargetStatus struct {
	Replicas                    int32 // dp.Spec.Replicas
	ActualReplicas              int32 // dp.Status.Replicas
	AvailableReplicas           int32 // dp.Status.AvailableReplicas
	UpdatedReplicas             int32 // latestreplicaset.kubeadmiral.io/replicas if it's up-to-date, else 0
	UpdatedAvailableReplicas    int32 // latestreplicaset.kubeadmiral.io/available-replicas if it's up-to-date, else 0
	CurrentNewReplicas          int32 // the replicas of new replicaset which belong to current deployment
	CurrentNewAvailableReplicas int32 // the available replicas of new replicaset which belong to current deployment
	Updated                     bool  // whether pod template is up to date in current dp with which in fedDp
	MaxSurge                    int32 // maxSurge in current dp
	MaxUnavailable              int32 // maxUnavailable in current dp
}

type TargetInfo struct {
	ClusterName     string
	Status          TargetStatus
	DesiredReplicas int32
}

func (t *TargetInfo) String() string {
	return fmt.Sprintf("%s:%d->%d,%d/%d,%d/%d,%d/%d,%d,%d,%t", t.ClusterName, t.Status.Replicas, t.DesiredReplicas,
		t.Status.UpdatedAvailableReplicas, t.Status.UpdatedReplicas,
		t.Status.CurrentNewAvailableReplicas, t.Status.CurrentNewReplicas,
		t.Status.AvailableReplicas, t.Status.ActualReplicas,
		t.Status.MaxSurge, t.Status.MaxUnavailable, t.Status.Updated)
}

func (t *TargetInfo) MaxSurge(maxSurge, leastSurge int32) (int32, int32) {
	res := Int32Min(maxSurge+leastSurge, t.ReplicasToUpdate())
	if res < 0 {
		res = 0
	}
	more := res - leastSurge
	// impossible in normal cases
	// normalize to zero to get a more strict plan, try the best to correct the unexpected situation
	if more < 0 {
		more = 0
	}
	if maxSurge < 0 && leastSurge > t.Status.MaxSurge && res > t.Status.MaxSurge {
		res = t.Status.MaxSurge
	}
	return res, more
}

func (t *TargetInfo) MaxUnavailable(maxUnavailable, leastUnavailable int32) (int32, int32) {
	res := Int32Min(maxUnavailable+leastUnavailable, t.ReplicasToUpdatedAvailable())
	if res < 0 {
		res = 0
	}
	more := res - leastUnavailable
	// impossible in normal cases
	// normalize to zero to get a more strict plan, try the best to correct the unexpected situation
	if more < 0 {
		more = 0
	}
	if maxUnavailable < 0 && leastUnavailable > t.Status.MaxUnavailable && res > t.Status.MaxUnavailable {
		res = t.Status.MaxUnavailable
	}
	return res, more
}

func (t *TargetInfo) MaxScaleOut(maxScaleOut, leastSurge int32) (int32, int32) {
	res := Int32Min(maxScaleOut+leastSurge, t.DesiredReplicas-t.Status.Replicas)
	if res < 0 {
		res = 0
	}
	more := res - leastSurge
	if more < 0 {
		more = 0
	}
	return res, more
}

func (t *TargetInfo) MaxScaleIn(maxScaleIn, leastUnavailable int32) (int32, int32) {
	res := Int32Min(maxScaleIn+leastUnavailable, t.Status.Replicas-t.DesiredReplicas)
	// impossible
	if res > t.Status.Replicas {
		res = t.Status.Replicas
	}
	if res < 0 {
		res = 0
	}
	more := res - leastUnavailable
	if more < 0 {
		more = 0
	}
	return res, more
}

func (t *TargetInfo) LeastSurge() int32 {
	res := t.Status.ActualReplicas - t.Status.Replicas
	if res < 0 {
		res = 0
	}
	if !t.DuringUpdating() {
		return res
	}
	return Int32Max(res, Int32Min(t.Status.MaxSurge, res+t.ReplicasToUpdateCurrently()))
}

func (t *TargetInfo) LeastUnavailable() int32 {
	res := t.Status.Replicas - t.Status.AvailableReplicas
	if res < 0 {
		res = 0
	}
	if !t.DuringUpdating() {
		return res
	}
	return Int32Max(res, Int32Min(t.Status.MaxUnavailable, t.ReplicasToUpdatedAvailableCurrently()))
}

func (t *TargetInfo) ReplicasToUpdate() int32 {
	res := t.Status.Replicas - t.Status.UpdatedReplicas
	if res < 0 {
		res = 0
	}
	return res
}

func (t *TargetInfo) ReplicasToUpdatedAvailable() int32 {
	res := t.Status.Replicas - t.Status.UpdatedAvailableReplicas
	if res < 0 {
		res = 0
	}
	return res
}

func (t *TargetInfo) ReplicasToUpdateCurrently() int32 {
	res := t.Status.Replicas - t.Status.CurrentNewReplicas
	if res < 0 {
		res = 0
	}
	return res
}

func (t *TargetInfo) ReplicasToUpdatedAvailableCurrently() int32 {
	res := t.Status.Replicas - t.Status.CurrentNewAvailableReplicas
	if res < 0 {
		res = 0
	}
	return res
}

func (t *TargetInfo) DuringUpdating() bool {
	// todo: only return t.Status.CurrentNewReplicas < t.Status.Replicas after we get the real currentNewReplicas
	if t.Status.CurrentNewReplicas < t.Status.Replicas {
		return true
	}
	if t.Status.Updated && t.ReplicasToUpdate() > 0 {
		return true
	}
	return false
}

func (t *TargetInfo) UpdateCompleted() bool {
	return t.ReplicasToUpdate() == 0
}

func (t *TargetInfo) IsSurge() bool {
	return t.Status.MaxSurge != 0 && t.Status.MaxUnavailable == 0
}

func (t *TargetInfo) Flip(defaultIsSurge bool) bool {
	// a temporary fix to avoid unexpected flipping
	// todo: avoiding this nasty judgment by restricting the replicas changes to be used only for scaling
	return t.IsSurge() && !defaultIsSurge && t.ReplicasToUpdatedAvailable() > 0
}

func (t *TargetInfo) SkipPlanForUpdate(maxSurge, maxUnavailable int32) bool {
	return maxSurge <= 0 && maxUnavailable <= 0 && !t.Status.Updated && !t.DuringUpdating() && t.LeastSurge() <= 0 &&
		t.LeastUnavailable() <= 0
}

func (t *TargetInfo) SkipPlanForUpdateForThoseToScaleIn(maxSurge, maxUnavailable, leastUnavailable int32) bool {
	if maxSurge <= 0 && maxUnavailable <= 0 && !t.Status.Updated && !t.DuringUpdating() {
		if leastUnavailable > 0 {
			return false
		}
		leastSurge := t.LeastSurge()
		if t.DesiredReplicas < t.Status.Replicas {
			leastSurge = 0
		}
		if leastSurge > 0 {
			return false
		}
		return true
	}
	return false
}

func (t *TargetInfo) SkipPlanForScaleIn(maxUnavailable int32) bool {
	return maxUnavailable <= 0 && t.LeastUnavailable() <= 0
}

func (t *TargetInfo) SkipPlanForScaleOut(maxSurge int32) bool {
	return maxSurge <= 0 && t.LeastSurge() <= 0
}

func unstructuredObjToTargetInfo(clusterName string, unstructuredObj *unstructured.Unstructured, desiredReplicas int32,
	desiredRevision string, typeConfig *fedcorev1a1.FederatedTypeConfig,
) (*TargetInfo, error) {
	if unstructuredObj == nil {
		return &TargetInfo{
			ClusterName:     clusterName,
			DesiredReplicas: desiredReplicas,
		}, nil
	}

	replicas, err := utilunstructured.GetInt64FromPath(unstructuredObj, typeConfig.Spec.PathDefinition.ReplicasSpec, nil)
	if err != nil || replicas == nil {
		return nil, errors.Errorf("failed to retrieve replicas, err: %v", err)
	}
	maxSurge, maxUnavailable, err := RetrieveFencepost(
		unstructuredObj,
		MaxSurgePathSlice,
		MaxUnavailablePathSlice,
		int32(*replicas),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve fencepost")
	}
	revision, ok := unstructuredObj.GetAnnotations()[common.CurrentRevisionAnnotation]
	if !ok {
		return nil, errors.Errorf("failed to retrieve annotation %s", common.CurrentRevisionAnnotation)
	}
	// consider it has been updated as long as the template is updated. We don't wait for the refresh of
	// latestreplicaset annotations since the latency due to asynchronous updates may bring some problems
	updated := revision == desiredRevision
	currentNewReplicas, currentNewAvailableReplicas, err := retrieveNewReplicaSetInfo(unstructuredObj)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve new replicaSet info")
	}

	updatedReplicas, updatedAvailableReplicas := currentNewReplicas, currentNewAvailableReplicas
	if !updated {
		updatedReplicas, updatedAvailableReplicas = 0, 0
	}

	actualReplicasOption, err := utilunstructured.GetInt64FromPath(
		unstructuredObj,
		typeConfig.Spec.PathDefinition.ReplicasSpec,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve actual replicas")
	}
	var actualReplicas int32
	if actualReplicasOption != nil {
		actualReplicas = int32(*actualReplicasOption)
	}

	availableReplicasOption, err := utilunstructured.GetInt64FromPath(
		unstructuredObj,
		typeConfig.Spec.PathDefinition.AvailableReplicasStatus,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve actual available replicas")
	}
	var availableReplicas int32
	if availableReplicasOption != nil {
		availableReplicas = int32(*availableReplicasOption)
	}

	t := &TargetInfo{
		ClusterName: clusterName,
		Status: TargetStatus{
			Replicas:                    int32(*replicas),
			ActualReplicas:              actualReplicas,
			AvailableReplicas:           availableReplicas,
			UpdatedReplicas:             updatedReplicas,
			UpdatedAvailableReplicas:    updatedAvailableReplicas,
			CurrentNewReplicas:          currentNewReplicas,
			CurrentNewAvailableReplicas: currentNewAvailableReplicas,
			Updated:                     updated,
			MaxSurge:                    maxSurge,
			MaxUnavailable:              maxUnavailable,
		},
		DesiredReplicas: desiredReplicas,
	}
	return t, nil
}

type RolloutPlanner struct {
	typeConfig     *fedcorev1a1.FederatedTypeConfig
	Key            string
	Targets        Targets
	MaxSurge       int32
	MaxUnavailable int32
	Replicas       int32
	Revision       string
}

func NewRolloutPlanner(
	key string,
	typeConfig *fedcorev1a1.FederatedTypeConfig,
	federatedResource *unstructured.Unstructured,
	replicas int32,
) (*RolloutPlanner, error) {
	pathPrefix := []string{common.SpecField, common.TemplateField}
	maxSurgePath := append(pathPrefix, MaxSurgePathSlice...)
	maxUnavailablePath := append(pathPrefix, MaxUnavailablePathSlice...)
	maxSurge, maxUnavailable, err := RetrieveFencepost(federatedResource, maxSurgePath, maxUnavailablePath, replicas)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve maxSurge or maxUnavailable from federated resource")
	}
	desiredRevision, ok := federatedResource.GetAnnotations()[common.CurrentRevisionAnnotation]
	if !ok {
		return nil, errors.Errorf(
			"failed to retrieve annotation %s from federated resource",
			common.CurrentRevisionAnnotation,
		)
	}
	return &RolloutPlanner{
		typeConfig:     typeConfig,
		Key:            key,
		MaxSurge:       maxSurge,
		MaxUnavailable: maxUnavailable,
		Replicas:       replicas,
		Revision:       desiredRevision,
	}, nil
}

func (p *RolloutPlanner) RegisterTarget(
	clusterName string,
	targetObj *unstructured.Unstructured,
	desiredReplicas int32,
) error {
	t, err := unstructuredObjToTargetInfo(clusterName, targetObj, desiredReplicas, p.Revision, p.typeConfig)
	if err != nil {
		return err
	}
	p.Targets = append(p.Targets, t)
	return nil
}

func (p *RolloutPlanner) IsScalingEvent() bool {
	_, targetsToScaleOut, targetsToScaleIn := sortTargets(p.Targets)
	// create / scale out / scale in
	if len(targetsToScaleOut) != 0 && len(targetsToScaleIn) != 0 {
		return false
	}
	if len(targetsToScaleOut) == 0 && len(targetsToScaleIn) == 0 {
		return false
	}
	for _, t := range p.Targets {
		if !t.UpdateCompleted() {
			return false
		}
		if t.Flip(p.IsSurge()) {
			return false
		}
	}
	return true
}

func (p *RolloutPlanner) PlanScale() RolloutPlans {
	plans := make(map[string]*RolloutPlan)
	for _, t := range p.Targets {
		plans[t.ClusterName] = &RolloutPlan{}
	}
	return plans
}

func (p *RolloutPlanner) String() string {
	var ts []string
	for _, t := range p.Targets {
		ts = append(ts, fmt.Sprintf("%v", t))
	}
	return fmt.Sprintf("%s[%d,%d,%d,%s]: %v",
		p.Key, p.Replicas, p.MaxSurge, p.MaxUnavailable, p.Revision, strings.Join(ts, "; "))
}

func (p *RolloutPlanner) RemainingMaxSurge() int32 {
	// maxSurge := p.Replicas + p.MaxSurge - p.Targets.ActualReplicas()
	// maxSurge := p.MaxSurge - (p.Targets.ActualReplicas() - p.Replicas)
	var replicas, occupied int32
	for _, t := range p.Targets {
		replicas += t.Status.Replicas
		occupied += t.LeastSurge()
	}
	return p.MaxSurge - (replicas - p.Replicas) - occupied
}

func (p *RolloutPlanner) RemainingMaxUnavailable() int32 {
	// maxUnavailable := p.Targets.AvailableReplicas() - (p.Replicas - p.MaxUnavailable)
	// maxUnavailable := p.MaxUnavailable - (p.Replicas - p.Targets.AvailableReplicas())
	var replicas, occupied int32
	for _, t := range p.Targets {
		replicas += t.Status.Replicas
		occupied += t.LeastUnavailable()
	}
	return p.MaxUnavailable - (p.Replicas - replicas) - occupied
}

func (p *RolloutPlanner) IsSurge() bool {
	return p.MaxSurge != 0 && p.MaxUnavailable == 0
}

// ComputeRolloutPlans compute maxUnavailable, maxSurge, replicas during rollout process. It returns a map that
// contains all the targets which are planned according to current status. Nil in a plan means the corresponding field
// won't be overridden by the rollout plan and should be set with the original value. If there's no plan for a target,
// it means "don't rollout it, it should wait for it's turn".
func (p *RolloutPlanner) Plan() RolloutPlans {
	targetsToUpdate, targetsToScaleOut, targetsToScaleIn := sortTargets(p.Targets)
	plans := make(map[string]*RolloutPlan)

	if p.IsScalingEvent() {
		return p.PlanScale()
	}

	// the remaining maxSurge & maxUnavailable that can be dispatched to deployments. If there are clusters that are
	// not ready, or that we failed to get deployment from, the maxSurge/maxUnavailble will be increased/decreased
	maxSurge, maxUnavailable := p.RemainingMaxSurge(), p.RemainingMaxUnavailable()

	// execution sequence (try to upgrade before scale out and scale in before upgrade):
	// 1. upgrade targets waiting to be scaled out
	// 2. scale in targets waiting to be scaled in
	// 3. upgrade targets that only need to be upgraded
	// 4. scale out targets waiting to be scaled out
	// 5. upgrade targets waiting to be scaled in
	for _, t := range targetsToScaleOut {
		if t.SkipPlanForUpdate(maxSurge, maxUnavailable) {
			continue
		}
		s, sm := t.MaxSurge(maxSurge, t.LeastSurge())
		u, um := t.MaxUnavailable(maxUnavailable, t.LeastUnavailable())
		maxSurge -= sm
		maxUnavailable -= um
		r := t.Status.Replicas
		plan := &RolloutPlan{Replicas: &r}
		plan.MaxSurge = &s
		plan.MaxUnavailable = &u
		plan.correctFencepost(t, p.IsSurge())
		plans[t.ClusterName] = plan
	}

	for _, t := range targetsToScaleIn {
		if t.SkipPlanForScaleIn(maxUnavailable) {
			continue
		}
		// we tend to scale in those that are already unavailable
		leastUnavailable := t.LeastUnavailable()
		if t.DuringUpdating() {
			// if it' during updating (for example, the maxUnavailable is enough for scale in and updating coming next,
			// so we set the replica and maxUnavailable; but a fed weight adjusting followed so we have to scale in again
			// even though it's being updated), scaling will be performed proportionally and may not cover the
			// unavailable instances as expected.
			leastUnavailable = 0
		}
		scale, more := t.MaxScaleIn(maxUnavailable, leastUnavailable)
		maxUnavailable -= more
		plan := &RolloutPlan{OnlyPatchReplicas: true}
		r := t.Status.Replicas - scale
		plan.Replicas = &r
		plans[t.ClusterName] = plan
	}

	for _, t := range targetsToUpdate {
		if t.SkipPlanForUpdate(maxSurge, maxUnavailable) {
			continue
		}
		s, sm := t.MaxSurge(maxSurge, t.LeastSurge())
		u, um := t.MaxUnavailable(maxUnavailable, t.LeastUnavailable())
		maxSurge -= sm
		maxUnavailable -= um
		plan := &RolloutPlan{}
		plan.MaxSurge = &s
		plan.MaxUnavailable = &u
		plan.correctFencepost(t, p.IsSurge())
		plans[t.ClusterName] = plan
	}

	for _, t := range targetsToScaleOut {
		if t.SkipPlanForScaleOut(maxSurge) {
			continue
		}
		// make sure new rs exists to avoid too much unnecessary work
		if !t.Status.Updated && t.Status.Replicas != 0 {
			continue
		}
		leastSurge := t.LeastSurge()
		if t.DuringUpdating() {
			leastSurge = 0
		}
		scale, more := t.MaxScaleOut(maxSurge, leastSurge)
		maxSurge -= more
		plan, ok := plans[t.ClusterName]
		if !ok || plan == nil {
			plan = &RolloutPlan{}
		}
		r := t.Status.Replicas + scale
		plan.Replicas = &r
		plans[t.ClusterName] = plan
	}

	for _, t := range targetsToScaleIn {
		plan, ok := plans[t.ClusterName]
		if !ok || plan == nil {
			r := t.Status.Replicas
			plan = &RolloutPlan{Replicas: &r}
		}
		// we have already scale in some unavailable instances in the second step, exclude them
		leastUnavailable := t.LeastUnavailable()
		if !t.DuringUpdating() {
			leastUnavailable -= t.Status.Replicas - *plan.Replicas
			if leastUnavailable < 0 {
				leastUnavailable = 0
			}
		}
		if t.SkipPlanForUpdateForThoseToScaleIn(maxSurge, maxUnavailable, leastUnavailable) {
			continue
		}

		plan.OnlyPatchReplicas = false
		s, sm := t.MaxSurge(maxSurge, t.LeastSurge())
		u, um := t.MaxUnavailable(maxUnavailable, leastUnavailable)
		maxSurge -= sm
		maxUnavailable -= um
		plan.MaxSurge = &s
		plan.MaxUnavailable = &u
		plan.correctFencepost(t, p.IsSurge())
		plans[t.ClusterName] = plan
	}
	if err := validatePlans(p, plans); err != nil {
		klog.Errorf("Failed to generate rollout plan for %s: %v. Current status: %s", p.Key, err, p)
		return RolloutPlans{}
	}
	return plans
}

func sortTargets(targets []*TargetInfo) ([]*TargetInfo, []*TargetInfo, []*TargetInfo) {
	// sort the list to first update the targets that are already in update process
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].ClusterName < targets[j].ClusterName
	})
	var toUpdate, toScaleOut, toScaleIn []*TargetInfo
	for _, t := range targets {
		change := t.DesiredReplicas - t.Status.Replicas
		switch {
		case change < 0:
			toScaleIn = append(toScaleIn, t)
		case change > 0:
			toScaleOut = append(toScaleOut, t)
		default:
			toUpdate = append(toUpdate, t)
		}
	}
	return toUpdate, toScaleOut, toScaleIn
}

func Int32Min(a, b int32) int32 {
	if b < a {
		return b
	}
	return a
}

func Int32Max(a, b int32) int32 {
	if b > a {
		return b
	}
	return a
}

func resolveFenceposts(maxSurge, maxUnavailable *intstrutil.IntOrString, desired int32) (int32, int32, error) {
	surge, err := intstrutil.GetValueFromIntOrPercent(
		intstrutil.ValueOrDefault(maxSurge, intstrutil.FromInt(0)),
		int(desired),
		true,
	)
	if err != nil {
		return 0, 0, err
	}
	unavailable, err := intstrutil.GetValueFromIntOrPercent(
		intstrutil.ValueOrDefault(maxUnavailable, intstrutil.FromInt(0)),
		int(desired),
		false,
	)
	if err != nil {
		return 0, 0, err
	}

	if surge == 0 && unavailable == 0 {
		// Validation should never allow the user to explicitly use zero values for both maxSurge
		// maxUnavailable. Due to rounding down maxUnavailable though, it may resolve to zero.
		// If both fenceposts resolve to zero, then we should set maxUnavailable to 1 on the
		// theory that surge might not work due to quota.
		unavailable = 1
	}

	return int32(surge), int32(unavailable), nil
}

func RetrieveFencepost(unstructuredObj *unstructured.Unstructured, maxSurgePath []string, maxUnavailablePath []string,
	replicas int32,
) (int32, int32, error) {
	var maxSurge, maxUnavailable *intstrutil.IntOrString
	if ms, ok, err := unstructured.NestedString(unstructuredObj.Object, maxSurgePath...); ok && err == nil {
		maxSurge = &intstrutil.IntOrString{Type: intstrutil.String, StrVal: ms}
	} else {
		if ms, ok, err2 := unstructured.NestedInt64(unstructuredObj.Object, maxSurgePath...); ok && err2 == nil {
			maxSurge = &intstrutil.IntOrString{Type: intstrutil.Int, IntVal: int32(ms)}
		} else {
			klog.V(4).Infof("Failed to retrieve maxSurge from %s/%s: %v, %v",
				unstructuredObj.GetNamespace(), unstructuredObj.GetName(), err, err2)
		}
	}
	if mu, ok, err := unstructured.NestedString(unstructuredObj.Object, maxUnavailablePath...); ok && err == nil {
		maxUnavailable = &intstrutil.IntOrString{Type: intstrutil.String, StrVal: mu}
	} else {
		if mu, ok, err2 := unstructured.NestedInt64(unstructuredObj.Object, maxUnavailablePath...); ok && err2 == nil {
			maxUnavailable = &intstrutil.IntOrString{Type: intstrutil.Int, IntVal: int32(mu)}
		} else {
			klog.V(4).Infof("Failed to retrieve maxUnavailable from %s/%s: %v, %v",
				unstructuredObj.GetNamespace(), unstructuredObj.GetName(), err, err2)
		}
	}

	ms, mu, err := resolveFenceposts(maxSurge, maxUnavailable, replicas)
	if err != nil {
		return 0, 0, err
	}
	if ms < 0 {
		ms = 0
	}
	if mu < 0 {
		mu = 0
	}
	return ms, mu, nil
}

func retrieveNewReplicaSetInfo(unstructuredObj *unstructured.Unstructured) (int32, int32, error) {
	ann, ok := unstructuredObj.GetAnnotations()[LatestReplicasetReplicasAnnotation]
	if !ok || ann == "" {
		return 0, 0, errors.Errorf("missing annotation %s", LatestReplicasetReplicasAnnotation)
	}
	replicas, err := strconv.ParseInt(ann, 10, 32)
	if err != nil {
		return 0, 0, err
	}
	ann, ok = unstructuredObj.GetAnnotations()[LatestReplicasetAvailableReplicasAnnotation]
	if !ok || ann == "" {
		return 0, 0, errors.Errorf(
			"missing annotation %s", LatestReplicasetAvailableReplicasAnnotation)
	}
	availableReplicas, err := strconv.ParseInt(ann, 10, 32)
	if err != nil {
		return 0, 0, err
	}
	// todo: make sure the latestreplicaset annotations describe the current pod template of deployment
	// a simple way to tell if the latestreplicaset annotations is up to date with current deployment.
	lastRsName, lastRsNameExists := unstructuredObj.GetAnnotations()[common.LastReplicasetName]
	rsName, rsNameExists := unstructuredObj.GetAnnotations()[LatestReplicasetNameAnnotation]
	if !rsNameExists {
		return 0, 0, errors.Errorf("missing annotation %s", LatestReplicasetNameAnnotation)
	}
	rsNameOutdated := lastRsNameExists && rsNameExists && lastRsName == rsName
	if rsNameOutdated {
		// paused=true may also result in this situation
		replicas, availableReplicas = 0, 0
	}
	return int32(replicas), int32(availableReplicas), nil
}

func validatePlans(p *RolloutPlanner, plans RolloutPlans) error {
	var planned, desired, current, maxUnavailable int32
	for _, t := range p.Targets {
		desired += t.DesiredReplicas
		cluster := t.ClusterName
		r := t.Status.Replicas
		current += r
		if p, ok := plans[cluster]; ok {
			if p == nil {
				return errors.Errorf("invalid plan for %s: %v", cluster, p)
			}
			if p.Replicas != nil {
				r = *p.Replicas
			} else {
				r = t.DesiredReplicas
			}
			if p.MaxUnavailable != nil {
				if p.MaxSurge == nil || *p.MaxSurge != 0 || *p.MaxUnavailable != 1 {
					maxUnavailable += *p.MaxUnavailable
				}
			}
		}
		planned += r
	}
	if p.Replicas-desired > p.MaxUnavailable {
		return errors.Errorf("desired replicas deviates too much from the initial replicas, maybe some " +
			"clusters are not ready")
	}
	l, h := desired, current
	if desired > current {
		l, h = current, desired
	}
	if l-planned > p.MaxUnavailable || planned-h > p.MaxSurge {
		return errors.Errorf("invalid plan: %v", plans)
	}
	return nil
}
