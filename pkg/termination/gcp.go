package termination

import (
	"context"
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
	gcpTerminationEndpointURL = "http://169.254.169.254/computeMetadata/v1/instance/preempted"
)

// gcpHandler implements the logic to check the termination endpoint and sets failed node condition
type gcpHandler struct {
	client       client.Client
	pollInterval time.Duration
	nodeName     string
	namespace    string
	log          logr.Logger
}

// Run starts the handler and runs the termination logic
func (h *gcpHandler) Run(stop <-chan struct{}) error {
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

func (h *gcpHandler) run(ctx context.Context) error {
	logger := h.log.WithValues("node", h.nodeName)
	logger.V(1).Info("Monitoring node termination")

	pollURL, err := url.Parse(gcpTerminationEndpointURL)
	if err != nil {
		// This should never happen
		panic(err)
	}

	if err := wait.PollImmediateUntil(h.pollInterval, func() (bool, error) {
		req, err := http.NewRequest("GET", pollURL.String(), nil)
		if err != nil {
			return false, fmt.Errorf("could not create request %q: %w", pollURL.String(), err)
		}

		req.Header.Add("Metadata-Flavor", "Google")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, fmt.Errorf("could not get URL %q: %w", pollURL.String(), err)
		}

		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return false, fmt.Errorf("failed to read responce body: %w", err)
		}

		respBody := string(bodyBytes)

		if respBody == "TRUE" {
			// Instance marked for termination
			return true, nil
		}

		// Instance not terminated yet
		logger.V(2).Info("Instance not marked for termination")
		return false, nil
	}, ctx.Done()); err != nil {
		return fmt.Errorf("error polling termination endpoint: %w", err)
	}

	// Will only get here if the termination endpoint returned FALSE
	logger.V(1).Info("Instance marked for termination, marking Machine for deletion")
	if err := markNodeForDeletion(ctx, h.client, h.nodeName); err != nil {
		return fmt.Errorf("error marking machine: %v", err)
	}

	return nil
}
