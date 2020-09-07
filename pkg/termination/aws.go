package termination

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	awsTerminationEndpointURL = "http://169.254.169.254/latest/meta-data/spot/termination-time"
)

// awsHandler implements the logic to check the termination endpoint and sets failed node condition
type awsHandler struct {
	client       client.Client
	pollInterval time.Duration
	nodeName     string
	namespace    string
	log          logr.Logger
}

// Run starts the handler and runs the termination logic
func (h *awsHandler) Run(stop <-chan struct{}) error {
	ctx, cancel := context.WithCancel(context.Background())

	errs := make(chan error, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		errs <- h.run(ctx, wg)
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

func (h *awsHandler) run(ctx context.Context, wg *sync.WaitGroup) error {
	defer wg.Done()

	logger := h.log.WithValues("node", h.nodeName)
	logger.V(1).Info("Monitoring node termination")

	pollURL, err := url.Parse(awsTerminationEndpointURL)
	if err != nil {
		// This should never happen
		panic(err)
	}

	if err := wait.PollImmediateUntil(h.pollInterval, func() (bool, error) {
		resp, err := http.Get(pollURL.String())
		if err != nil {
			return false, fmt.Errorf("could not get URL %q: %v", pollURL.String(), err)
		}
		switch resp.StatusCode {
		case http.StatusNotFound:
			// Instance not terminated yet
			logger.V(2).Info("Instance not marked for termination")
			return false, nil
		case http.StatusOK:
			// Instance marked for termination
			return true, nil
		default:
			// Unknown case, return an error
			return false, fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}
	}, ctx.Done()); err != nil {
		return fmt.Errorf("error polling termination endpoint: %v", err)
	}

	// Will only get here if the termination endpoint returned 200
	logger.V(1).Info("Instance marked for termination, marking Machine for deletion")
	if err := markNodeForDeletion(ctx, h.client, h.nodeName); err != nil {
		return fmt.Errorf("error marking machine: %v", err)
	}

	return nil
}
