package termination

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// azureTerminationEndpointURL see the following link for more details about the endpoint
	// https://docs.microsoft.com/en-us/azure/virtual-machines/windows/scheduled-events#endpoint-discovery
	azureTerminationEndpointURL = "http://169.254.169.254/metadata/scheduledevents?api-version=2019-08-01"
)

// azureHandler implements the logic to check the termination endpoint and sets failed node condition
type azureHandler struct {
	client       client.Client
	pollInterval time.Duration
	nodeName     string
	namespace    string
	log          logr.Logger
}

// Run starts the handler and runs the termination logic
func (h *azureHandler) Run(stop <-chan struct{}) error {
	ctx, cancel := context.WithCancel(context.Background())

	errs := make(chan error, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		errs <- h.run(ctx)
	}()

	select {
	case <-stop:
		cancel()
		// Wait for run to stop
		wg.Wait()
		return nil
	case err := <-errs:
		cancel()
		return err
	}
}

func (h *azureHandler) run(ctx context.Context) error {
	logger := h.log.WithValues("node", h.nodeName)
	logger.V(1).Info("Monitoring node termination")

	pollURL, err := url.Parse(azureTerminationEndpointURL)
	if err != nil {
		// This should never happen
		panic(err)
	}

	if err := wait.PollImmediateUntil(h.pollInterval, func() (bool, error) {
		req, err := http.NewRequest("GET", pollURL.String(), nil)
		if err != nil {
			return false, fmt.Errorf("could not create request %q: %w", pollURL.String(), err)
		}

		req.Header.Add("Metadata", "true")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, fmt.Errorf("could not get URL %q: %w", pollURL.String(), err)
		}

		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return false, fmt.Errorf("failed to read responce body: %w", err)
		}

		s := scheduledEvents{}
		err = json.Unmarshal(bodyBytes, &s)
		if err != nil {
			return false, fmt.Errorf("failed to unmarshal responce body: %w", err)
		}

		for _, event := range s.Events {
			if event.EventType == preemptEventType {
				// Instance marked for termination
				return true, nil
			}
		}

		// Instance not terminated yet
		h.log.V(2).Info("Instance not marked for termination")
		return false, nil
	}, ctx.Done()); err != nil {
		return fmt.Errorf("error polling termination endpoint: %w", err)
	}

	// Will only get here if the termination endpoint returned preempt event
	logger.V(1).Info("Instance marked for termination, marking Machine for deletion")
	if err := markNodeForDeletion(ctx, h.client, h.nodeName); err != nil {
		return fmt.Errorf("error marking machine: %v", err)
	}

	return nil
}

const preemptEventType = "Preempt"

// scheduledEvents represents metadata response, more detailed info can be found here:
// https://docs.microsoft.com/en-us/azure/virtual-machines/linux/scheduled-events#use-the-api
type scheduledEvents struct {
	Events []events `json:"Events"`
}

type events struct {
	EventType string `json:"EventType"`
}

// notFoundMachineForNode this error is returned when no machine for node is found in a list of machines
type notFoundMachineForNode struct{}

func (err notFoundMachineForNode) Error() string {
	return "machine not found for node"
}
