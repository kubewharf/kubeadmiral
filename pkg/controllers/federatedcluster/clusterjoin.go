/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

package federatedcluster

import (
	"context"
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/stats"
	"github.com/kubewharf/kubeadmiral/pkg/util/logging"
	"github.com/kubewharf/kubeadmiral/pkg/util/metrics"
)

const (
	MemberServiceAccountName = "kubeadmiral-member"
	FederatedClusterUID      = common.DefaultPrefix + "federated-cluster-uid"

	ServiceAccountTokenKey = "service-account-token-data"

	serviceAccountSecretTimeout = 30 * time.Second
)

const (
	ClusterJoinedReason  = "ClusterJoined"
	ClusterJoinedMessage = "cluster has joined the federation"

	TokenNotObtainedReason  = "TokenNotObtained"
	TokenNotObtainedMessage = "Service account token has not been obtained from the cluster"

	JoinTimeoutExceededReason          = "JoinTimeoutExceeded"
	JoinTimeoutExceededMessageTemplate = "Timeout exceeded when joining the federation, message from last attempt: %v"

	ClusterUnjoinableReason  = "ClusterUnjoinable"
	ClusterUnjoinableMessage = "Cluster is already managed by a KubeAdmiral control plane"
)

const (
	EventReasonJoinClusterTimeoutExceeded = "JoinClusterTimeoutExceeded"
	EventReasonJoinClusterError           = "JoinClusterError"
	EventReasonJoinClusterSuccess         = "JoinClusterSuccess"
	EventReasonClusterUnjoinable          = "ClusterUnjoinable"
)

const (
	joinSuccess = "success"
	joinFailure = "failed"
)

// Processes a cluster that has not joined.
// If either condition or joinPerformed returned is non-nil, the caller should merge them into
// the cluster status and update the cluster.
// The returned condition (if not-nil) will have status, reason and message set. The other fields
// should be added by the caller.
// The returned err is for informational purpose only and the caller should not abort on non-nil error.
func (c *FederatedClusterController) handleNotJoinedCluster(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
) (updated *fedcorev1a1.FederatedCluster, condition *fedcorev1a1.ClusterCondition, joinPerformed *bool, err error) {
	logger := klog.FromContext(ctx)

	joinedCondition := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterJoined)

	// 1. check for join timeout

	if joinedCondition != nil &&
		joinedCondition.Status == corev1.ConditionFalse &&
		time.Since(joinedCondition.LastTransitionTime.Time) > c.clusterJoinTimeout {
		// Join timed out
		logger.Error(nil, "Cluster join timed out")
		c.metrics.Duration(metrics.ClusterJoinedDuration, cluster.CreationTimestamp.Time,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "result", Value: joinFailure},
			stats.Tag{Name: "reason", Value: EventReasonJoinClusterTimeoutExceeded})
		c.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeWarning,
			EventReasonJoinClusterTimeoutExceeded,
			"Cluster join timed out",
		)
		return cluster, &fedcorev1a1.ClusterCondition{
			Status:  corev1.ConditionFalse,
			Reason:  JoinTimeoutExceededReason,
			Message: fmt.Sprintf(JoinTimeoutExceededMessageTemplate, joinedCondition.Message),
		}, nil, nil
	}

	// 2. The remaining steps require a cluster kube client, attempt to create one

	_, clusterKubeClient, err := c.getClusterClient(ctx, cluster)
	if err != nil {
		logger.Error(err, "Failed to create cluster client")
		msg := fmt.Sprintf("Failed to create cluster client: %v", err.Error())
		c.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeWarning, EventReasonJoinClusterError, msg,
		)
		return cluster, &fedcorev1a1.ClusterCondition{
			Status:  corev1.ConditionFalse,
			Reason:  TokenNotObtainedReason,
			Message: msg,
		}, nil, err
	}

	// 3. Get or create system namespace in the cluster, this will also tell us if the cluster is unjoinable

	ctx, logger = logging.InjectLoggerValues(ctx, "fed-system-namespace", c.fedSystemNamespace)

	logger.V(2).Info("Get system namespace in cluster")
	memberFedNamespace, err := clusterKubeClient.CoreV1().Namespaces().Get(ctx, c.fedSystemNamespace, metav1.GetOptions{ResourceVersion: "0"})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("Failed to get namespace: %v", err.Error())
			logger.Error(err, "Failed to get namespace")
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning, EventReasonJoinClusterError, msg,
			)
			return cluster, &fedcorev1a1.ClusterCondition{
				Status:  corev1.ConditionFalse,
				Reason:  TokenNotObtainedReason,
				Message: msg,
			}, nil, err
		}

		// Ensure the value is nil and not garbage.
		memberFedNamespace = nil
	}

	if memberFedNamespace != nil && memberFedNamespace.Annotations[FederatedClusterUID] != string(cluster.UID) {
		// Namespace exists and is not created by us - the cluster is managed by another control plane.
		msg := "Cluster is unjoinable (check if cluster is already joined to another federation)"
		logger.Error(nil, msg, "UID", memberFedNamespace.Annotations[FederatedClusterUID], "clusterUID", string(cluster.UID))
		c.metrics.Duration(metrics.ClusterJoinedDuration, cluster.CreationTimestamp.Time,
			stats.Tag{Name: "cluster_name", Value: cluster.Name},
			stats.Tag{Name: "result", Value: joinFailure},
			stats.Tag{Name: "reason", Value: EventReasonClusterUnjoinable})
		c.eventRecorder.Eventf(
			cluster,
			corev1.EventTypeWarning,
			EventReasonClusterUnjoinable,
			msg,
		)
		return cluster, &fedcorev1a1.ClusterCondition{
				Status:  corev1.ConditionFalse,
				Reason:  ClusterUnjoinableReason,
				Message: msg,
			},
			// Cluster is managed by another control plane - no need to perform clean-up on removal
			pointer.Bool(false), nil
	}

	// Either the namespace doesn't exist or it is created by us.
	// Clean-up on removal is required.
	joinPerformed = pointer.Bool(true)

	// If ns doesn't exist, create one
	if memberFedNamespace == nil {
		memberFedNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: c.fedSystemNamespace,
				Annotations: map[string]string{
					FederatedClusterUID: string(cluster.UID),
				},
			},
		}

		logger.V(1).Info("Create system namespace in cluster")
		memberFedNamespace, err = clusterKubeClient.CoreV1().Namespaces().Create(ctx, memberFedNamespace, metav1.CreateOptions{})
		if err != nil {
			msg := fmt.Sprintf("Failed to create system namespace: %v", err.Error())
			logger.Error(err, "Failed to create system namespace")
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning, EventReasonJoinClusterError, msg,
			)
			return cluster, &fedcorev1a1.ClusterCondition{
				Status:  corev1.ConditionFalse,
				Reason:  TokenNotObtainedReason,
				Message: msg,
			}, joinPerformed, err
		}
	}

	// 4. If the cluster uses service account token, we have an additional step to create the corresponding required resources

	if cluster.Spec.UseServiceAccountToken {
		logger.V(2).Info("Get and save cluster token")
		err = c.getAndSaveClusterToken(ctx, cluster, clusterKubeClient, memberFedNamespace)

		if err != nil {
			msg := fmt.Sprintf("Failed to get and save cluster token: %v", err.Error())
			logger.Error(err, "Failed to get and save cluster token")
			c.eventRecorder.Eventf(
				cluster,
				corev1.EventTypeWarning, EventReasonJoinClusterError, msg,
			)
			return cluster, &fedcorev1a1.ClusterCondition{
				Status:  corev1.ConditionFalse,
				Reason:  TokenNotObtainedReason,
				Message: msg,
			}, joinPerformed, err
		}
	}

	// 5. Cluster is joined, update condition

	logger.V(2).Info("Cluster joined successfully")
	c.metrics.Duration(metrics.ClusterJoinedDuration, cluster.CreationTimestamp.Time,
		stats.Tag{Name: "cluster_name", Value: cluster.Name},
		stats.Tag{Name: "result", Value: joinSuccess},
		stats.Tag{Name: "reason", Value: EventReasonJoinClusterSuccess})
	c.eventRecorder.Eventf(
		cluster,
		corev1.EventTypeNormal,
		EventReasonJoinClusterSuccess,
		"Cluster joined successfully",
	)
	return cluster, &fedcorev1a1.ClusterCondition{
		Status:  corev1.ConditionTrue,
		Reason:  ClusterJoinedReason,
		Message: ClusterJoinedMessage,
	}, joinPerformed, nil
}

func (c *FederatedClusterController) getAndSaveClusterToken(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	clusterKubeClient kubernetes.Interface,
	memberSystemNamespace *corev1.Namespace,
) error {
	logger := klog.FromContext(ctx)

	logger.V(2).Info("Creating authorized service account")
	saTokenSecretName, err := createAuthorizedServiceAccount(ctx, clusterKubeClient, memberSystemNamespace, cluster.Name, false)
	if err != nil {
		return err
	}

	logger.V(1).Info("Updating cluster secret")
	token, err := getServiceAccountToken(ctx, clusterKubeClient, memberSystemNamespace.Name, saTokenSecretName)
	if err != nil {
		return fmt.Errorf("error getting service account token from joining cluster: %w", err)
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		secret, err := c.kubeClient.CoreV1().Secrets(c.fedSystemNamespace).Get(
			ctx,
			cluster.Spec.SecretRef.Name,
			metav1.GetOptions{},
		)
		if err != nil {
			return err
		}
		secret.Data[ServiceAccountTokenKey] = token
		_, err = c.kubeClient.CoreV1().Secrets(c.fedSystemNamespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf(
			"failed to update secret for joining cluster: %w",
			err,
		)
	}

	return nil
}

// createAuthorizedServiceAccount creates a service account and service account token secret
// and grants the privileges required by the control plane to manage
// resources in the joining cluster.  The created secret name is returned on success.
func createAuthorizedServiceAccount(
	ctx context.Context,
	clusterKubeClient kubernetes.Interface,
	memberSystemNamespace *corev1.Namespace,
	clusterName string,
	errorOnExisting bool,
) (string, error) {
	ctx, logger := logging.InjectLoggerValues(ctx, "member-service-account-name", MemberServiceAccountName)

	// 1. create service account

	logger.V(1).Info("Creating service account")
	if err := createServiceAccount(
		ctx,
		clusterKubeClient,
		memberSystemNamespace.Name,
		MemberServiceAccountName,
		clusterName,
		errorOnExisting,
	); err != nil {
		return "", fmt.Errorf("failed to create service account %s: %w", MemberServiceAccountName, err)
	}

	// 2. create service account token secret

	logger.V(1).Info("Creating service account token secret")
	saTokenSecretName, err := createServiceAccountTokenSecret(
		ctx,
		clusterKubeClient,
		memberSystemNamespace.Name,
		MemberServiceAccountName,
		clusterName,
		errorOnExisting,
	)
	if err != nil {
		return "", fmt.Errorf("error creating service account token secret %s : %w", MemberServiceAccountName, err)
	}

	ctx, logger = logging.InjectLoggerValues(ctx, "sa-token-secret-name", saTokenSecretName)
	logger.V(1).Info("Created service account token secret for service account")

	// 3. create rbac

	logger.V(1).Info("Creating RBAC for service account")
	if err = createClusterRoleAndBinding(
		ctx,
		clusterKubeClient,
		memberSystemNamespace,
		MemberServiceAccountName,
		clusterName,
		errorOnExisting,
	); err != nil {
		return "", fmt.Errorf("error creating cluster role and binding for service account %s: %w", MemberServiceAccountName, err)
	}

	return saTokenSecretName, nil
}

// createServiceAccount creates a service account in the cluster associated
// with clusterClientset with credentials that will be used by the host cluster
// to access its API server.
func createServiceAccount(
	ctx context.Context,
	clusterClientset kubernetes.Interface,
	namespace, saName, joiningClusterName string,
	errorOnExisting bool,
) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: namespace,
			Annotations: map[string]string{
				"kubernetes.io/enforce-mountable-secrets": "true",
			},
		},
		AutomountServiceAccountToken: pointer.Bool(false),
	}

	_, err := clusterClientset.CoreV1().ServiceAccounts(namespace).Create(ctx, sa, metav1.CreateOptions{})
	switch {
	case apierrors.IsAlreadyExists(err) && errorOnExisting:
		return fmt.Errorf("service account %s/%s already exists in target cluster %s", namespace, saName, joiningClusterName)
	case err != nil && !apierrors.IsAlreadyExists(err):
		return fmt.Errorf(
			"could not create service account %s/%s in target cluster %s due to: %w",
			namespace,
			saName,
			joiningClusterName,
			err,
		)
	default:
		return nil
	}
}

// createServiceAccountTokenSecret creates a service account token secret in the cluster associated
// with clusterClientset with credentials that will be used by the host cluster
// to access its API server.
func createServiceAccountTokenSecret(
	ctx context.Context,
	clusterClientset kubernetes.Interface,
	namespace, saName, joiningClusterName string,
	errorOnExisting bool,
) (string, error) {
	saTokenSecretName := saName
	saTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saTokenSecretName,
			Namespace: namespace,
			Annotations: map[string]string{
				"kubernetes.io/service-account.name": saName,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	_, err := clusterClientset.CoreV1().Secrets(namespace).Create(ctx, saTokenSecret, metav1.CreateOptions{})
	switch {
	case apierrors.IsAlreadyExists(err) && errorOnExisting:
		return "", fmt.Errorf(
			"service account token secret %s/%s already exists in target cluster %s",
			namespace,
			saName,
			joiningClusterName,
		)
	case err != nil && !apierrors.IsAlreadyExists(err):
		return "", fmt.Errorf(
			"could not create service account token secret %s/%s in target cluster %s due to: %w",
			namespace,
			saName,
			joiningClusterName,
			err,
		)
	default:
		return saTokenSecretName, nil
	}
}

func bindingSubjects(saName, namespace string) []rbacv1.Subject {
	return []rbacv1.Subject{
		{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      saName,
			Namespace: namespace,
		},
	}
}

// createClusterRoleAndBinding creates an RBAC cluster role and
// binding that allows the service account identified by saName to
// access all resources in all namespaces in the cluster associated
// with clientset.
func createClusterRoleAndBinding(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace *corev1.Namespace,
	saName, clusterName string,
	errorOnExisting bool,
) error {
	roleName := fmt.Sprintf("kubeadmiral-controller-manager:%s", saName)
	namespaceOwnerReference := *metav1.NewControllerRef(namespace, schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})

	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:            roleName,
			OwnerReferences: []metav1.OwnerReference{namespaceOwnerReference},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{rbacv1.VerbAll},
				APIGroups: []string{rbacv1.APIGroupAll},
				Resources: []string{rbacv1.ResourceAll},
			},
			{
				NonResourceURLs: []string{rbacv1.NonResourceAll},
				Verbs:           []string{"get"},
			},
		},
	}

	existingRole, err := clientset.RbacV1().ClusterRoles().Get(ctx, roleName, metav1.GetOptions{})
	switch {
	case err != nil && !apierrors.IsNotFound(err):
		return fmt.Errorf("could not get cluster role for service account %s in joining cluster %s due to %w", saName, clusterName, err)
	case err == nil && errorOnExisting:
		return fmt.Errorf("cluster role for service account %s in joining cluster %s already exists", saName, clusterName)
	case err == nil:
		existingRole.Rules = role.Rules
		existingRole.OwnerReferences = []metav1.OwnerReference{namespaceOwnerReference}
		_, err := clientset.RbacV1().ClusterRoles().Update(ctx, existingRole, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf(
				"could not update cluster role for service account: %s in joining cluster: %s due to: %w",
				saName,
				clusterName,
				err,
			)
		}
	default: // role was not found
		_, err := clientset.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf(
				"could not create cluster role for service account: %s in joining cluster: %s due to: %w",
				saName,
				clusterName,
				err,
			)
		}
	}

	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            roleName,
			OwnerReferences: []metav1.OwnerReference{namespaceOwnerReference},
		},
		Subjects: bindingSubjects(saName, namespace.Name),
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     roleName,
		},
	}
	existingBinding, err := clientset.RbacV1().ClusterRoleBindings().Get(ctx, binding.Name, metav1.GetOptions{})
	switch {
	case err != nil && !apierrors.IsNotFound(err):
		return fmt.Errorf(
			"could not get cluster role binding for service account %s in joining cluster %s due to %w",
			saName,
			clusterName,
			err,
		)
	case err == nil && errorOnExisting:
		return fmt.Errorf("cluster role binding for service account %s in joining cluster %s already exists", saName, clusterName)
	case err == nil:
		// The roleRef cannot be updated, therefore if the existing roleRef is different, the existing rolebinding
		// must be deleted and recreated with the correct roleRef
		if !reflect.DeepEqual(existingBinding.RoleRef, binding.RoleRef) {
			err = clientset.RbacV1().ClusterRoleBindings().Delete(ctx, existingBinding.Name, metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf(
					"could not delete existing cluster role binding for service account %s in joining cluster %s due to: %w",
					saName,
					clusterName,
					err,
				)
			}
			_, err = clientset.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf(
					"could not create cluster role binding for service account: %s in joining cluster: %s due to: %w",
					saName,
					clusterName,
					err,
				)
			}
		} else {
			existingBinding.Subjects = binding.Subjects
			existingBinding.OwnerReferences = binding.OwnerReferences
			_, err := clientset.RbacV1().ClusterRoleBindings().Update(ctx, existingBinding, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf(
					"could not update cluster role binding for service account: %s in joining cluster: %s due to: %w",
					saName,
					clusterName,
					err,
				)
			}
		}
	default:
		_, err = clientset.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf(
				"could not create cluster role binding for service account: %s in joining cluster: %s due to: %w",
				saName,
				clusterName,
				err,
			)
		}
	}
	return nil
}

func getServiceAccountToken(
	ctx context.Context,
	clusterClientset kubernetes.Interface,
	memberSystemNamespace, secretName string,
) ([]byte, error) {
	// Get the secret from the joining cluster.
	var token []byte

	err := wait.PollImmediate(1*time.Second, serviceAccountSecretTimeout, func() (bool, error) {
		joiningClusterSASecret, err := clusterClientset.CoreV1().
			Secrets(memberSystemNamespace).
			Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		var ok bool
		if token, ok = joiningClusterSASecret.Data["token"]; !ok {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("could not get service account token secret from joining cluster: %w", err)
	}

	return token, nil
}
