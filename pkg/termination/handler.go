package termination

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	azureProvider                                       = "azure"
	awsProvider                                         = "aws"
	gcpProvider                                         = "gcp"
	terminatingConditionType   corev1.NodeConditionType = "Terminating"
	terminationRequestedReason                          = "TerminationRequested"
)

// Handler represents a handler that will run to check the termination
// notice endpoint and mark node for deletion
type Handler interface {
	Run(stop <-chan struct{}) error
}

// NewHandler constructs a new Handler for every cloud supported cloud provider
func NewHandler(logger logr.Logger, cfg *rest.Config, pollInterval time.Duration, cloudProvider, namespace, nodeName string) (Handler, error) {
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, fmt.Errorf("error creating client: %v", err)
	}

	logger = logger.WithValues("node", nodeName, "namespace", namespace)

	switch cloudProvider {
	case azureProvider:
		return &azureHandler{
			client:       c,
			pollInterval: pollInterval,
			nodeName:     nodeName,
			namespace:    namespace,
			log:          logger,
		}, nil
	case awsProvider:
		return &awsHandler{
			client:       c,
			pollInterval: pollInterval,
			nodeName:     nodeName,
			namespace:    namespace,
			log:          logger,
		}, nil
	case gcpProvider:
		return &gcpHandler{
			client:       c,
			pollInterval: pollInterval,
			nodeName:     nodeName,
			namespace:    namespace,
			log:          logger,
		}, nil
	}

	return nil, errors.New("cloudProviderNot supported")
}

func markNodeForDeletion(ctx context.Context, ctrlRuntimeClient client.Client, nodeName string) error {
	node := &corev1.Node{}
	if err := ctrlRuntimeClient.Get(ctx, client.ObjectKey{Name: nodeName}, node); err != nil {
		return fmt.Errorf("error fetching node: %v", err)
	}

	addNodeTerminationCondition(node)
	if err := ctrlRuntimeClient.Status().Update(ctx, node); err != nil {
		return fmt.Errorf("error updating node status")
	}
	return nil
}

// nodeHasTerminationCondition checks whether the node already
// has a condition with the terminatingConditionType type
func nodeHasTerminationCondition(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == terminatingConditionType {
			return true
		}
	}
	return false
}

// addNodeTerminationCondition will add a condition with a
// terminatingConditionType type to the node
func addNodeTerminationCondition(node *corev1.Node) {
	now := metav1.Now()
	terminatingCondition := corev1.NodeCondition{
		Type:               terminatingConditionType,
		Status:             corev1.ConditionTrue,
		LastHeartbeatTime:  now,
		LastTransitionTime: now,
		Reason:             terminationRequestedReason,
		Message:            "The cloud provider has marked this instance for termination",
	}

	if !nodeHasTerminationCondition(node) {
		// No need to merge, just add the new condition to the end
		node.Status.Conditions = append(node.Status.Conditions, terminatingCondition)
		return
	}

	// The node already has a terminating condition,
	// so make sure it has the correct status
	conditions := []corev1.NodeCondition{}
	for _, condition := range node.Status.Conditions {
		if condition.Type != terminatingConditionType {
			conditions = append(conditions, condition)
			continue
		}

		// Condition type is terminating
		if condition.Status == corev1.ConditionTrue {
			// Condition already marked true, do not update
			conditions = append(conditions, condition)
			continue
		}

		// The existing terminating condition had the wrong status
		conditions = append(conditions, terminatingCondition)
	}

	node.Status.Conditions = conditions
}
