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
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/util"
)

const (
	MemberServiceAccountName = "kubeadmiral-member"
	FederatedClusterUID      = common.DefaultPrefix + "federated-cluster-uid"

	ServiceAccountTokenKey = "service-account-token-data"
	ServiceAccountCAKey    = "service-account-ca-data"

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

func handleNotJoinedCluster(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	client fedclient.Interface,
	kubeClient kubeclient.Interface,
	eventRecorder record.EventRecorder,
	fedSystemNamespace string,
	clusterJoinTimeout time.Duration,
) (*fedcorev1a1.FederatedCluster, error) {
	logger := klog.FromContext(ctx).WithValues("process", "cluster-join")
	ctx = klog.NewContext(ctx, logger)

	joinedCondition := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterJoined)

	// 1. check for join timeout

	if joinedCondition != nil &&
		joinedCondition.Status == corev1.ConditionFalse &&
		time.Since(joinedCondition.LastTransitionTime.Time) > clusterJoinTimeout {
		// join timed out
		logger.Error(nil, "Cluster join timed out")
		return recordJoinResult(
			ctx, cluster, client, eventRecorder,
			nil,
			corev1.EventTypeWarning, EventReasonJoinClusterTimeoutExceeded, "Cluster join timed out",
			corev1.ConditionFalse, JoinTimeoutExceededReason, fmt.Sprintf(JoinTimeoutExceededMessageTemplate, joinedCondition.Message),
		)
	}

	// 2. The remaining steps require a cluster kube client, attempt to create one

	// TODO: should populate condition if failed to create client
	restConfig := &rest.Config{Host: cluster.Spec.APIEndpoint}

	clusterSecretName := cluster.Spec.SecretRef.Name
	if clusterSecretName == "" {
		eventRecorder.Eventf(
			cluster,
			corev1.EventTypeWarning,
			EventReasonJoinClusterError,
			"cluster %q secret is not set",
			cluster.Name,
		)
		return cluster, fmt.Errorf("cluster secret is not set")
	}
	clusterSecret, err := kubeClient.CoreV1().Secrets(fedSystemNamespace).Get(ctx, clusterSecretName, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		eventRecorder.Eventf(
			cluster,
			corev1.EventTypeWarning,
			EventReasonJoinClusterError,
			"cluster %q secret %q not found",
			cluster.Name,
			clusterSecretName,
		)
		return cluster, fmt.Errorf("cluster secret not found: %w", err)
	}
	if err != nil {
		return cluster, fmt.Errorf("failed to get cluster secret: %w", err)
	}

	if err := util.PopulateAuthDetailsFromSecret(restConfig, cluster.Spec.Insecure, clusterSecret, false); err != nil {
		eventRecorder.Eventf(
			cluster,
			corev1.EventTypeWarning,
			EventReasonHandleTerminatingClusterFailed,
			"cluster %q secret %q is malformed: %v",
			cluster.Name,
			clusterSecretName,
			err.Error(),
		)
		return cluster, fmt.Errorf("cluster secret malformed: %w", err)
	}

	clusterKubeClient, err := kubeclient.NewForConfig(restConfig)
	if err != nil {
		return cluster, fmt.Errorf("failed to create cluster kube clientset: %w", err)
	}

	// 3. Get or create system namespace in the cluster, this will also tell us if the cluster is unjoinable

	logger.Info(fmt.Sprintf("Create or get system namespace %s in cluster", fedSystemNamespace))
	memberFedNamespace, err := clusterKubeClient.CoreV1().Namespaces().Get(ctx, fedSystemNamespace, metav1.GetOptions{ResourceVersion: "0"})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("Failed to get namespace: %v", err.Error())
			logger.Error(err, "Failed to get namespace")
			return recordJoinResult(
				ctx, cluster, client, eventRecorder,
				err,
				corev1.EventTypeWarning, EventReasonJoinClusterError, msg,
				corev1.ConditionFalse, TokenNotObtainedReason, msg,
			)
		}

		// Ensure the value is nil and not garbage.
		memberFedNamespace = nil
	}

	if memberFedNamespace != nil && memberFedNamespace.Annotations[FederatedClusterUID] != string(cluster.UID) {
		// ns exists and is not created by us - the cluster is managed by another control plane
		msg := "Cluster is unjoinable (check if cluster is already joined to another federation)"
		logger.Error(nil, msg, "UID", memberFedNamespace.Annotations[FederatedClusterUID], "clusterUID", string(cluster.UID))
		cluster.Status.JoinPerformed = false
		return recordJoinResult(
			ctx, cluster, client, eventRecorder,
			nil,
			corev1.EventTypeWarning, EventReasonClusterUnjoinable, msg,
			corev1.ConditionFalse, ClusterUnjoinableReason, msg,
		)
	}

	// Either the namespace doesn't exist or it is created by us.
	// Clean-up on removal is required.
	cluster.Status.JoinPerformed = true
	// Update the condition to persist the flag before proceeding to ensure
	// we don't lose the flag due to an update failure.
	if cluster, err = client.CoreV1alpha1().FederatedClusters().UpdateStatus(
		ctx, cluster, metav1.UpdateOptions{},
	); err != nil {
		logger.Error(err, "Failed to persist JoinPerformed status")
		return cluster, fmt.Errorf("failed to persist JoinPerformed status: %w", err)
	}

	// If ns doesn't exist, create one
	if memberFedNamespace == nil {
		memberFedNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: fedSystemNamespace,
				Annotations: map[string]string{
					FederatedClusterUID: string(cluster.UID),
				},
			},
		}

		memberFedNamespace, err = clusterKubeClient.CoreV1().Namespaces().Create(ctx, memberFedNamespace, metav1.CreateOptions{})
		if err != nil {
			msg := fmt.Sprintf("Failed to create system namespace: %v", err.Error())
			logger.Error(err, "Failed to create system namespace")
			return recordJoinResult(
				ctx, cluster, client, eventRecorder,
				err,
				corev1.EventTypeWarning, EventReasonJoinClusterError, msg,
				corev1.ConditionFalse, TokenNotObtainedReason, msg,
			)
		}
	}

	// 4. If the cluster uses service account token, we have an additional step to create the corresponding required resources

	if cluster.Spec.UseServiceAccountToken {
		logger.Info("Get and save cluster token")
		err = getAndSaveClusterToken(ctx, cluster, kubeClient, clusterKubeClient, fedSystemNamespace, memberFedNamespace, logger)

		if err != nil {
			msg := fmt.Sprintf("Failed to get and save cluster token: %v", err.Error())
			logger.Error(err, "Failed to get and save cluster token")
			return recordJoinResult(
				ctx, cluster, client, eventRecorder,
				err,
				corev1.EventTypeWarning, EventReasonJoinClusterError, msg,
				corev1.ConditionFalse, TokenNotObtainedReason, msg,
			)
		}
	}

	// 5. Cluster is joined, update condition

	logger.Info("Cluster joined successfully")
	return recordJoinResult(
		ctx, cluster, client, eventRecorder,
		nil,
		corev1.EventTypeNormal, EventReasonJoinClusterSuccess, "Cluster joined successfully",
		corev1.ConditionTrue, ClusterJoinedReason, ClusterJoinedMessage,
	)
}

func getAndSaveClusterToken(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	kubeClient kubeclient.Interface,
	clusterKubeClient kubeclient.Interface,
	fedSystemNamespace string,
	memberSystemNamespace *corev1.Namespace,
	logger klog.Logger,
) error {
	logger.Info("Creating authorized service account")
	saTokenSecretName, err := createAuthorizedServiceAccount(ctx, clusterKubeClient, memberSystemNamespace, cluster.Name, false, logger)
	if err != nil {
		return err
	}

	logger.Info("Updating cluster secret")
	token, ca, err := getServiceAccountToken(ctx, clusterKubeClient, memberSystemNamespace.Name, saTokenSecretName)
	if err != nil {
		return fmt.Errorf("error getting service account token from joining cluster: %w", err)
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		secret, err := kubeClient.CoreV1().Secrets(fedSystemNamespace).Get(ctx, cluster.Spec.SecretRef.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		secret.Data[ServiceAccountTokenKey] = token
		secret.Data[ServiceAccountCAKey] = ca
		_, err = kubeClient.CoreV1().Secrets(fedSystemNamespace).Update(ctx, secret, metav1.UpdateOptions{})
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
	clusterKubeClient kubeclient.Interface,
	memberSystemNamespace *corev1.Namespace,
	clusterName string,
	errorOnExisting bool,
	logger klog.Logger,
) (string, error) {
	// 1. create service account
	logger.Info(fmt.Sprintf("Creating service account %s", MemberServiceAccountName))
	err := createServiceAccount(ctx, clusterKubeClient, memberSystemNamespace.Name, MemberServiceAccountName, clusterName, errorOnExisting)
	if err != nil {
		return "", fmt.Errorf("failed to create service account %s: %w", MemberServiceAccountName, err)
	}

	// 2. create service account token secret
	logger.Info(fmt.Sprintf("Creating service account token secret for %s", MemberServiceAccountName))
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
	logger.Info(fmt.Sprintf("Created service account token secret %s for service account %v", saTokenSecretName, MemberServiceAccountName))

	// 3. create rbac
	logger.Info(fmt.Sprintf("Creating RBAC for service account %s", MemberServiceAccountName))
	err = createClusterRoleAndBinding(ctx, clusterKubeClient, memberSystemNamespace, MemberServiceAccountName, clusterName, errorOnExisting)
	if err != nil {
		return "", fmt.Errorf("error creating cluster role and binding for service account %s: %w", MemberServiceAccountName, err)
	}

	return saTokenSecretName, nil
}

// createServiceAccount creates a service account in the cluster associated
// with clusterClientset with credentials that will be used by the host cluster
// to access its API server.
func createServiceAccount(
	ctx context.Context,
	clusterClientset kubeclient.Interface,
	namespace, saName, joiningClusterName string, errorOnExisting bool,
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
	clusterClientset kubeclient.Interface,
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
	clientset kubeclient.Interface,
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
	clusterClientset kubeclient.Interface,
	memberSystemNamespace, secretName string,
) ([]byte, []byte, error) {
	// Get the secret from the joining cluster.
	var token []byte
	var ca []byte

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
		ca = joiningClusterSASecret.Data["ca.crt"]

		return true, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("could not get service account token secret from joining cluster: %w", err)
	}

	return token, ca, nil
}

func recordJoinResult(
	ctx context.Context,
	cluster *fedcorev1a1.FederatedCluster,
	client fedclient.Interface,
	eventRecorder record.EventRecorder,

	joinError error,

	eventType, eventReason, eventMessage string,

	conditionStatus corev1.ConditionStatus,
	conditionReason, conditionMessage string,
) (*fedcorev1a1.FederatedCluster, error) {
	eventRecorder.Event(
		cluster, eventType, eventReason, eventMessage,
	)

	currentTime := metav1.Now()
	oldCondition := getClusterCondition(&cluster.Status, fedcorev1a1.ClusterJoined)
	newCondition := &fedcorev1a1.ClusterCondition{
		Type:               fedcorev1a1.ClusterJoined,
		Status:             conditionStatus,
		Reason:             conditionReason,
		Message:            conditionMessage,
		LastProbeTime:      currentTime,
		LastTransitionTime: currentTime,
	}
	// The condition's last transition time is updated to the current time only if
	// the status has changed.
	if oldCondition != nil && oldCondition.Status == conditionStatus {
		newCondition.LastTransitionTime = oldCondition.LastTransitionTime
	}

	setClusterCondition(&cluster.Status, newCondition)

	if cluster, updateErr := client.CoreV1alpha1().FederatedClusters().UpdateStatus(
		ctx, cluster, metav1.UpdateOptions{},
	); updateErr != nil {
		klog.FromContext(ctx).Error(updateErr, "Failed to update cluster status")
		return cluster, fmt.Errorf("failed to update cluster status: %w", updateErr)
	}

	return cluster, joinError
}
