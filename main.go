package main

import (
	"flag"
	"time"

	"github.com/alexander-demichev/termination-handler/pkg/termination"
	"k8s.io/klog"
	"k8s.io/klog/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
	klog.InitFlags(nil)
	logger := klogr.New()

	pollIntervalSeconds := flag.Int64("poll-interval-seconds", 5, "interval in seconds at which termination notice endpoint should be checked (Default: 5)")
	nodeName := flag.String("node-name", "", "name of the node that the termination handler is running on")
	namespace := flag.String("namespace", "", "namespace that the machine for the node should live in. If unspecified, the look for machines across all namespaces.")
	cloudProvider := flag.String("cloud-provider", "", "name of the cloud provider that the termination handler is running on")
	flag.Set("logtostderr", "true")
	flag.Parse()

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		logger.Error(err, "Error getting configuration")
		return
	}

	// Get the poll interval as a duration from the `poll-interval-seconds` flag
	pollInterval := time.Duration(*pollIntervalSeconds) * time.Second

	// Construct a termination handler
	handler, err := termination.NewHandler(logger, cfg, pollInterval, *cloudProvider, *namespace, *nodeName)
	if err != nil {
		logger.Error(err, "Error constructing termination handler")
		return
	}

	// Start the termination handler
	if err := handler.Run(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "Error starting termination handler")
		return
	}
}
