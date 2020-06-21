package main

import (
	log "github.com/sirupsen/logrus"
	core_v1 "k8s.io/api/core/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"os/signal"
	"syscall"
)

const curlAnnotation = "x-k8s.io/curl-me-that"

type DataUpdater interface {
	UpdateData(*core_v1.ConfigMap, map[string]string) error
}

func curlConfigMap(configMap *core_v1.ConfigMap, updater DataUpdater) {
	logger := log.WithFields(log.Fields{
		"name":      configMap.Name,
		"namespace": configMap.Namespace,
	})
	urls, ok := configMap.Annotations[curlAnnotation]
	if !ok {
		logger.Info("Skipping configmap without annotation")
		return
	}

	fetcher, err := PageFetcherFromString(urls)
	if err != nil {
		logger.WithError(err).Error("Cannot parse URLs")
		return
	}

	// Do not fetch what the ConfigMap already has, as an optimization,
	// but also to prevent an infinite loop of constantly refreshing the
	// data in the configmap.
	fetcher.Exclude(configMap.Data)

	data, err := fetcher.Fetch()
	if err != nil {
		logger.WithError(err).Error("Cannot fetch URLs")
		// Do not return here, set the data on a best-effort basis.
	}

	if len(data) == 0 {
		logger.Info("Leaving configmap already processed")
		return
	}

	err = updater.UpdateData(configMap, data)
	if err != nil {
		logger.WithError(err).Error("Cannot add data")
		return
	}

	logger.WithField("data", data).Info("Curled data into ConfigMap")
}

func main() {
	config, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		log.WithError(err).Fatal("Cannot load config")
	}

	clientset, err := k8s.NewForConfig(config)
	if err != nil {
		log.WithError(err).Fatal("Creating kubernetes clientset")
	}
	manager := NewConfigMapManager(clientset)

	configmaps, err := manager.StartWatching()
	if err != nil {
		log.WithError(err).Fatal("Cannot watch ConfigMaps")
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-stop
		log.WithField("signal", sig).Info("Received signal for termination")
		manager.StopWatching()
	}()

	for configmap := range configmaps {
		curlConfigMap(configmap, manager)
	}
}