package kubernetes

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/thekrauss/watcher/internal/restate"
)

func WatchPods(ctx context.Context, restateClient *restate.RestateClient) {

	config, _ := rest.InClusterConfig()
	clientset, _ := kubernetes.NewForConfig(config)

	watcher, _ := clientset.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{})

	for event := range watcher.ResultChan() {

		if event.Object == nil {
			continue
		}
		if event.Type == watch.Modified || event.Type == watch.Error {
			restateClient.NotifyEvent(event)
		}
	}

}

//kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml

func WatchMetrics(ctx context.Context, rc *restate.RestateClient) {

}
