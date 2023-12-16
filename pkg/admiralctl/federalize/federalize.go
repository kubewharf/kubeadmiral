package federalize

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kubewharf/kubeadmiral/pkg/util/naming"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/templates"

	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util"
	"github.com/kubewharf/kubeadmiral/pkg/admiralctl/util/restmapper"
	fedcorev1a1 "github.com/kubewharf/kubeadmiral/pkg/apis/core/v1alpha1"
	fedclient "github.com/kubewharf/kubeadmiral/pkg/client/clientset/versioned"
	"github.com/kubewharf/kubeadmiral/pkg/client/generic/scheme"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/common"
	"github.com/kubewharf/kubeadmiral/pkg/controllers/scheduler"
	"github.com/kubewharf/kubeadmiral/pkg/util/adoption"
)

var (
	federalizeLongDesc = templates.LongDesc(`
	Federalize resources from target cluster to Kubeadmiral control plane. 
	The target cluster needs to have joined the federation.

	If the resource already exists in Kubeadmiral control plane, 
	you need to edit PropagationPolicy and OverridePolicy to propagate it.
	`)

	federalizeExample = templates.Examples(`
		# Federalize deployment(default/busybox) from cluster1 to Kubeadmiral
		%[1]s federalize deployment busybox -n default -C cluster1

		# Federalize deployment(default/busybox) with gvk from cluster1 to Kubeadmiral
		%[1]s federalize deployment.v1.apps busybox -n default -C cluster1

		# Support to use '--dry-run' to print resource and policy that need to be deployed
		%[1]s federalize deployment busybox -n default -C cluster1 --dry-run

		# Support to use '--policy-name' to specify a policy that already exists
		%[1]s federalize deployment busybox -n default -C cluster1 --auto-create-policy=false --policy-name=<POLICY_NAME>

		# Federalize secret(default/default-secret) from cluster1 to Kubeadmiral
		%[1]s federalize secret default-secret -n default -C cluster1

		# Support to use '--cluster-kubeconfig' to specify the kubeconfig of member cluster
		%[1]s federalize deployment busybox -n default -C cluster1 --cluster-kubeconfig=<CLUSTER_KUBECONFIG_PATH>

		# Support to use '--cluster-kubeconfig' and '--cluster-context' to specify the kubeconfig and context of member cluster
		%[1]s federalize deployment busybox -n default -C cluster1 --cluster-kubeconfig=<CLUSTER_KUBECONFIG_PATH> --cluster-context=<CLUSTER_CONTEXT>`)
)

// CommandFederalizeOption holds all command options for federalize
type CommandFederalizeOption struct {
	// AutoCreatePolicy determines whether a PropagationPolicy
	// or ClusterPropagationPolicy should be created automatically.
	AutoCreatePolicy bool

	// Cluster is the name of target cluster
	Cluster string

	// ClusterContext is context name of target cluster in kubeconfig.
	ClusterContext string

	// ClusterKubeConfig is the target cluster's kubeconfig path.
	ClusterKubeConfig string

	// DryRun run the command in dry-run mode.
	DryRun bool

	// Namespace is the namespace of target resource
	Namespace string

	// OutputFormat determines the output format, json or yaml.
	OutputFormat string

	// PolicyName is the name of the PropagationPolicy or ClusterPropagationPolicy.
	// If AutoCreatePolicy is false, it needs to be specified as an existing policy.
	PolicyName string

	// SchedulingMode determines the mode used by the scheduler when scheduling federated objects.
	SchedulingMode string

	JSONYamlPrintFlags *genericclioptions.JSONYamlPrintFlags
	Printer            func(*meta.RESTMapping, *bool, bool, bool) (printers.ResourcePrinterFunc, error)

	DynamicClientset dynamic.Interface
	FedClientset     *fedclient.Clientset

	controlPlaneRestConfig *rest.Config
	resourceName           string
	resourceKind           string
	resourceObj            *unstructured.Unstructured
	resource.FilenameOptions
	mapper meta.RESTMapper
	gvr    schema.GroupVersionResource
	gvk    schema.GroupVersionKind
}

// AddFlags adds flags for a specified FlagSet.
func (o *CommandFederalizeOption) AddFlags(flags *pflag.FlagSet) {
	flags.BoolVar(&o.AutoCreatePolicy, "auto-create-policy", true,
		"Automatically create a PropagationPolicy for namespace-scoped resources or "+
			"create a ClusterPropagationPolicy for cluster-scoped resources.")
	flags.StringVarP(&o.Cluster, "cluster", "C", "", "The name of target cluster (eg. -C=member1).")
	flags.StringVar(&o.ClusterContext, "cluster-context", "", "Context name of target cluster in kubeconfig.")
	flags.StringVar(&o.ClusterKubeConfig, "cluster-kubeconfig", "", "Path of the target cluster's kubeconfig.")
	flags.BoolVar(&o.DryRun, "dry-run", false, "Run the command in dry-run mode.")
	flags.StringVarP(&o.Namespace, "namespace", "n", o.Namespace, "The namespace of the resource to be federalized.")
	flags.StringVarP(&o.OutputFormat, "output", "o", "yaml", "Output format: yaml or json.")
	flags.StringVar(&o.PolicyName, "policy-name", "",
		"The name of the PropagationPolicy or ClusterPropagationPolicy that is automatically created after migration. "+
			"If not specified, the policy name will be the resource name with resource group and kind.")
	flags.StringVar(&o.SchedulingMode, "scheduling-mode", string(fedcorev1a1.SchedulingModeDuplicate),
		"determines the mode used by the scheduler when scheduling federated objects, One of: Duplicate or Divide, default is Duplicate.")
}

// NewCmdFederalize creates the `federalize` command
func NewCmdFederalize(f util.Factory, parentCommand string) *cobra.Command {
	opts := CommandFederalizeOption{
		JSONYamlPrintFlags: genericclioptions.NewJSONYamlPrintFlags(),
	}

	cmd := &cobra.Command{
		Use:                   "federalize <RESOURCE_TYPE> <RESOURCE_NAME> -n <NAMESPACE> -C <CLUSTER_NAME>",
		Short:                 "federalize resource from target clusters to Kubeadmiral control plane",
		Long:                  federalizeLongDesc,
		Example:               fmt.Sprintf(federalizeExample, parentCommand),
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Preflight(f, args); err != nil {
				return err
			}
			if err := opts.Federalize(); err != nil {
				return err
			}
			return nil
		},
	}

	flag := cmd.Flags()
	opts.AddFlags(flag)

	return cmd
}

// Obj cluster info
type Obj struct {
	Cluster string
	Info    *resource.Info
}

// Preflight validate the option in advance and set option value.
func (o *CommandFederalizeOption) Preflight(f util.Factory, args []string) error {
	var err error

	if len(args) != 2 {
		return fmt.Errorf("command line input format error")
	}

	if o.Cluster == "" {
		return fmt.Errorf("the cluster name cannot be empty")
	}

	if o.OutputFormat != "" && o.OutputFormat != "yaml" && o.OutputFormat != "json" {
		return fmt.Errorf("output format is only one of json and yaml")
	}

	if !o.AutoCreatePolicy && o.PolicyName == "" {
		return fmt.Errorf("if AutoCreatePolicy is false, you need to specify an existing policy")
	}

	o.resourceKind = args[0]
	o.resourceName = args[1]

	o.Printer = func(mapping *meta.RESTMapping, outputObjects *bool, withNamespace bool,
		withKind bool) (printers.ResourcePrinterFunc, error) {
		printer, err := o.JSONYamlPrintFlags.ToPrinter(o.OutputFormat)
		if err != nil {
			return nil, err
		}

		printer, err = printers.NewTypeSetter(scheme.Scheme).WrapToPrinter(printer, nil)
		if err != nil {
			return nil, err
		}

		return printer.PrintObj, nil
	}

	if o.Namespace == "" {
		o.Namespace, _, err = f.ToRawKubeConfigLoader().Namespace()
		if err != nil {
			return fmt.Errorf("failed to get namespace from Factory. error: %w", err)
		}
	}

	if len(o.ClusterContext) == 0 {
		o.ClusterContext = o.Cluster
	}

	o.controlPlaneRestConfig, err = f.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to get control plane rest config. err: %w", err)
	}

	o.DynamicClientset = dynamic.NewForConfigOrDie(o.controlPlaneRestConfig)
	o.FedClientset = fedclient.NewForConfigOrDie(o.controlPlaneRestConfig)

	memberClusterFactory, err := o.createMemberClusterFactory(f)
	if err != nil {
		return err
	}

	objInfo, err := o.getObjInfo(memberClusterFactory, o.Cluster, args)
	if err != nil {
		return fmt.Errorf("failed to get resource in cluster(%s). err: %w", o.Cluster, err)
	}

	obj := objInfo.Info.Object.(*unstructured.Unstructured)

	// Resources in deletion do not support federalization
	if obj.GetDeletionTimestamp() != nil {
		return fmt.Errorf("this resource is in the deleted state and cannot be federalized")
	}

	o.resourceObj = obj
	o.gvk = obj.GetObjectKind().GroupVersionKind()
	o.mapper, err = restmapper.GetRESTMapper(o.controlPlaneRestConfig)
	if err != nil {
		return fmt.Errorf("failed to create restmapper: %w", err)
	}

	o.gvr, err = restmapper.ConvertGVKToGVR(o.mapper, o.gvk)
	if err != nil {
		return fmt.Errorf("failed to get gvr from %q: %w", o.gvk, err)
	}

	return nil
}

// getObjInfo get obj info in member cluster
func (o *CommandFederalizeOption) getObjInfo(f cmdutil.Factory, cluster string, args []string) (*Obj, error) {
	r := f.NewBuilder().
		Unstructured().
		NamespaceParam(o.Namespace).
		FilenameParam(false, &o.FilenameOptions).
		RequestChunksOf(500).
		ResourceTypeOrNameArgs(true, args...).
		ContinueOnError().
		Latest().
		Flatten().
		Do()

	r.IgnoreErrors(apierrors.IsNotFound)

	infos, err := r.Infos()
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, fmt.Errorf("resource %s(%s) don't exist in cluster(%s)", o.resourceKind, o.resourceName, o.Cluster)
	}

	obj := &Obj{
		Cluster: cluster,
		Info:    infos[0],
	}

	return obj, nil
}

// createMemberClusterFactory create member cluster factory base on whether kubeconfig is specified
func (o *CommandFederalizeOption) createMemberClusterFactory(f util.Factory) (cmdutil.Factory, error) {
	var memberClusterFactory cmdutil.Factory
	var err error
	if o.ClusterKubeConfig != "" {
		memberClusterFactory, err = util.NewClusterFactoryByKubeConfig(o.ClusterKubeConfig, o.ClusterContext)
		if err != nil {
			return nil, err
		}
	} else {
		memberClusterFactory, err = f.NewClusterFactoryByClusterName(o.Cluster)
		if err != nil {
			return nil, err
		}
	}
	return memberClusterFactory, nil
}

// Federalize federalize resource from target cluster
func (o *CommandFederalizeOption) Federalize() error {
	if err := templateForResource(o.resourceObj); err != nil {
		return fmt.Errorf("failed to convert resource %q(%s/%s) to template: %w", o.gvr, o.Namespace, o.resourceName, err)
	}

	// if dry running, just print resourceObj and policy.
	if o.DryRun {
		err := o.printResourceObjectAndPolicy(o.resourceObj)
		return err
	}

	policyName := o.PolicyName
	var err error
	if len(o.resourceObj.GetNamespace()) == 0 {
		if o.AutoCreatePolicy {
			policyName, err = o.createClusterPropagationPolicy()
			if err != nil {
				return err
			}
		}

		_, err := o.DynamicClientset.Resource(o.gvr).Get(context.TODO(), o.resourceName, metav1.GetOptions{})
		if err == nil {
			fmt.Printf("Resource %q(%s) already exist in Kubeadmiral control plane.", o.gvr, o.resourceName)
			return nil
		}

		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get resource %q(%s) in control plane: %w", o.gvr, o.resourceName, err)
		}

		bindPolicyToResource(o.resourceObj, policyName)
		setConflictResolutionAnnotation(o.resourceObj)

		_, err = o.DynamicClientset.Resource(o.gvr).Create(context.TODO(), o.resourceObj, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create resource %q(%s) in control plane: %w", o.gvr, o.resourceName, err)
		}

		fmt.Printf("Resource %q(%s) is federalized successfully\n", o.gvr, o.resourceName)
	} else {
		if o.AutoCreatePolicy {
			policyName, err = o.createPropagationPolicy()
			if err != nil {
				return err
			}
		}

		_, err := o.DynamicClientset.Resource(o.gvr).Namespace(o.Namespace).Get(context.TODO(), o.resourceName, metav1.GetOptions{})
		if err == nil {
			fmt.Printf("Resource %q(%s/%s) already exist in Kubeadmiral control plane.", o.gvr, o.Namespace, o.resourceName)
			return nil
		}

		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get resource %q(%s/%s) in control plane: %w", o.gvr, o.Namespace, o.resourceName, err)
		}

		bindPolicyToResource(o.resourceObj, policyName)
		setConflictResolutionAnnotation(o.resourceObj)

		_, err = o.DynamicClientset.Resource(o.gvr).Namespace(o.Namespace).Create(context.TODO(), o.resourceObj, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create resource %q(%s/%s) in control plane: %w", o.gvr, o.Namespace, o.resourceName, err)
		}

		fmt.Printf("Resource %q(%s/%s) is federalized successfully\n", o.gvr, o.Namespace, o.resourceName)
	}

	return nil
}

// bindPolicyToResource bind the policy to the resource as a label
func bindPolicyToResource(obj *unstructured.Unstructured, policyName string) {
	labels := obj.DeepCopy().GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if _, exist := labels[scheduler.PropagationPolicyNameLabel]; !exist {
		labels[scheduler.PropagationPolicyNameLabel] = policyName
	}
	obj.SetLabels(labels)
}

// setConflictResolutionAnnotation set ConflictResolution "adopt" to resource
// because the resources that need to be federalized already exist in the cluster
func setConflictResolutionAnnotation(obj *unstructured.Unstructured) {
	annotations := obj.DeepCopy().GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if _, exist := annotations[adoption.ConflictResolutionAnnotation]; !exist {
		annotations[adoption.ConflictResolutionAnnotation] = string(adoption.ConflictResolutionAdopt)
	}
	obj.SetAnnotations(annotations)
}

// printResourceObjectAndPolicy print the converted resource and federalized policy
func (o *CommandFederalizeOption) printResourceObjectAndPolicy(resourceObj *unstructured.Unstructured) error {
	printer, err := o.Printer(nil, nil, false, false)
	if err != nil {
		return fmt.Errorf("failed to initialize k8s printer. err: %w", err)
	}

	if err = printer.PrintObj(resourceObj, os.Stdout); err != nil {
		return fmt.Errorf("failed to print the resource template. err: %w", err)
	}
	if o.AutoCreatePolicy {
		var policyName string
		if o.PolicyName == "" {
			policyName = naming.GenerateFederatedObjectName(o.resourceName, o.gvk.String())
		} else {
			policyName = o.PolicyName
		}

		if len(resourceObj.GetNamespace()) == 0 {
			policy := constructPropagationPolicy(policyName, "", o.Cluster, o.SchedulingMode)
			if cpp, ok := policy.(*fedcorev1a1.ClusterPropagationPolicy); ok {
				if err = printer.PrintObj(cpp, os.Stdout); err != nil {
					return fmt.Errorf("failed to print the ClusterPropagationPolicy. err: %w", err)
				}
			} else {
				return fmt.Errorf("failed to construct the ClusterPropagationPolicy")
			}
		} else {
			policy := constructPropagationPolicy(policyName, o.Namespace, o.Cluster, o.SchedulingMode)
			if pp, ok := policy.(*fedcorev1a1.PropagationPolicy); ok {
				if err = printer.PrintObj(pp, os.Stdout); err != nil {
					return fmt.Errorf("failed to print the PropagationPolicy. err: %w", err)
				}
			} else {
				return fmt.Errorf("failed to construct the PropagationPolicy")
			}
		}
	} else {
		// The user specifies a policy that already exists
		if len(resourceObj.GetNamespace()) == 0 {
			policy, err := o.FedClientset.CoreV1alpha1().ClusterPropagationPolicies().Get(context.TODO(), o.PolicyName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get the ClusterPropagationPolicy %s. err: %w", o.PolicyName, err)
			}
			if err = printer.PrintObj(policy, os.Stdout); err != nil {
				return fmt.Errorf("failed to print the ClusterPropagationPolicy. err: %w", err)
			}
		} else {
			policy, err := o.FedClientset.CoreV1alpha1().PropagationPolicies(o.Namespace).Get(context.TODO(), o.PolicyName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get the PropagationPolicy %s in namespace %s. err: %w", o.PolicyName, o.Namespace, err)
			}
			if err = printer.PrintObj(policy, os.Stdout); err != nil {
				return fmt.Errorf("failed to print the PropagationPolicy %s in namespace %s. err: %w", o.PolicyName, o.Namespace, err)
			}
		}
	}

	return nil
}

// createPropagationPolicy create PropagationPolicy in Kubeadmiral control plane
func (o *CommandFederalizeOption) createPropagationPolicy() (string, error) {
	var policyName string
	if o.PolicyName == "" {
		policyName = naming.GenerateFederatedObjectName(o.resourceName, strings.ToLower(fmt.Sprintf("%s-%s", o.gvk.Group, o.gvk.Kind)))
	} else {
		policyName = o.PolicyName
	}

	_, err := o.FedClientset.CoreV1alpha1().PropagationPolicies(o.Namespace).Get(context.TODO(), policyName, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		policy := constructPropagationPolicy(policyName, o.Namespace, o.Cluster, o.SchedulingMode)
		if pp, ok := policy.(*fedcorev1a1.PropagationPolicy); ok {
			_, err = o.FedClientset.CoreV1alpha1().PropagationPolicies(o.Namespace).Create(context.TODO(), pp, metav1.CreateOptions{})
			if err != nil {
				return "", err
			}
		} else {
			err = fmt.Errorf("failed to construct the PropagationPolicy")
		}

		return policyName, err
	}
	if err != nil {
		return "", fmt.Errorf("failed to get PropagationPolicy(%s/%s) in control plane: %w", o.Namespace, policyName, err)
	}

	// PropagationPolicy already exists, not to create it
	return "", fmt.Errorf("the PropagationPolicy(%s/%s) already exist, please edit it to propagate resource", o.Namespace, policyName)
}

// createClusterPropagationPolicy create ClusterPropagationPolicy in Kubeadmiral control plane
func (o *CommandFederalizeOption) createClusterPropagationPolicy() (string, error) {
	var policyName string
	if o.PolicyName == "" {
		policyName = naming.GenerateFederatedObjectName(o.resourceName, o.gvk.String())
	} else {
		policyName = o.PolicyName
	}

	_, err := o.FedClientset.CoreV1alpha1().ClusterPropagationPolicies().Get(context.TODO(), policyName, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		policy := constructPropagationPolicy(policyName, "", o.Cluster, o.SchedulingMode)
		if cpp, ok := policy.(*fedcorev1a1.ClusterPropagationPolicy); ok {
			_, err = o.FedClientset.CoreV1alpha1().ClusterPropagationPolicies().Create(context.TODO(), cpp, metav1.CreateOptions{})
		} else {
			err = fmt.Errorf("failed to construct the ClusterPropagationPolicy")
		}

		return policyName, err
	}
	if err != nil {
		return "", fmt.Errorf("failed to get ClusterPropagationPolicy(%s) in control plane: %w", policyName, err)
	}

	// ClusterPropagationPolicy already exists, not to create it
	return "", fmt.Errorf("the ClusterPropagationPolicy(%s) already exist, please edit it to propagate resource", policyName)
}

// templateForResource convert resource to template
func templateForResource(resource *unstructured.Unstructured) error {
	resource.SetSelfLink("")
	resource.SetUID("")
	resource.SetResourceVersion("")
	resource.SetCreationTimestamp(metav1.Time{})
	resource.SetOwnerReferences(nil)
	resource.SetFinalizers(nil)
	resource.SetManagedFields(nil)
	resource.SetDeletionGracePeriodSeconds(nil)
	resource.SetGeneration(0)
	unstructured.RemoveNestedField(resource.Object, common.StatusField)

	if resource.GetKind() == common.ServiceKind {
		clusterIP, exist, _ := unstructured.NestedString(resource.Object, "spec", "clusterIP")
		if exist && clusterIP != corev1.ClusterIPNone {
			unstructured.RemoveNestedField(resource.Object, "spec", "clusterIP")
			unstructured.RemoveNestedField(resource.Object, "spec", "clusterIPs")
		}
	}

	if resource.GetKind() == common.JobKind {
		manualSelector, exists, _ := unstructured.NestedBool(resource.Object, "spec", "manualSelector")
		if !exists || exists && !manualSelector {
			if err := removeGenerateSelectorOfJob(resource); err != nil {
				return err
			}
		}
	}

	if resource.GetKind() == common.ServiceAccountKind {
		secrets, exist, _ := unstructured.NestedSlice(resource.Object, "secrets")
		if exist && len(secrets) > 0 {
			tokenPrefix := fmt.Sprintf("%s-token-", resource.GetName())
			for idx := 0; idx < len(secrets); idx++ {
				if strings.HasPrefix(secrets[idx].(map[string]interface{})["name"].(string), tokenPrefix) {
					secrets = append(secrets[:idx], secrets[idx+1:]...)
				}
			}
			_ = unstructured.SetNestedSlice(resource.Object, secrets, "secrets")
		}
	}
	return nil
}

// getLabelValue get the label value by labelKey.
func getLabelValue(labels map[string]string, labelKey string) string {
	if labels == nil {
		return ""
	}

	return labels[labelKey]
}

// removeGenerateSelectorOfJob remove some unwanted information that is not needed during
// the job federalization process
func removeGenerateSelectorOfJob(resource *unstructured.Unstructured) error {
	matchLabels, exist, err := unstructured.NestedStringMap(resource.Object, "spec", "selector", "matchLabels")
	if err != nil {
		return err
	}
	if exist {
		if getLabelValue(matchLabels, "controller-uid") != "" {
			delete(matchLabels, "controller-uid")
		}
		err = unstructured.SetNestedStringMap(resource.Object, matchLabels, "spec", "selector", "matchLabels")
		if err != nil {
			return err
		}
	}

	labels, exist, err := unstructured.NestedStringMap(resource.Object, "spec", "template", "metadata", "labels")
	if err != nil {
		return err
	}
	if exist {
		if getLabelValue(labels, "controller-uid") != "" {
			delete(labels, "controller-uid")
		}

		if getLabelValue(labels, "job-name") != "" {
			delete(labels, "job-name")
		}

		err = unstructured.SetNestedStringMap(resource.Object, labels, "spec", "template", "metadata", "labels")
		if err != nil {
			return err
		}
	}
	return nil
}

// constructPropagationPolicy construct PropagationPolicy or ClusterPropagationPolicy
func constructPropagationPolicy(name, namespace, cluster, schedulingMode string) interface{} {
	objMeta := metav1.ObjectMeta{Name: name}

	var clusterScoped bool
	if len(namespace) == 0 {
		clusterScoped = true
	}

	if !clusterScoped {
		objMeta.Namespace = namespace
	}

	spec := fedcorev1a1.PropagationPolicySpec{
		SchedulingMode: fedcorev1a1.SchedulingMode(schedulingMode),
		Placements: []fedcorev1a1.DesiredPlacement{
			{
				Cluster: cluster,
			},
		},
	}

	if clusterScoped {
		return &fedcorev1a1.ClusterPropagationPolicy{
			ObjectMeta: objMeta,
			Spec:       spec,
		}
	}

	return &fedcorev1a1.PropagationPolicy{
		ObjectMeta: objMeta,
		Spec:       spec,
	}
}
