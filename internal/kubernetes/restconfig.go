package kubernetes

import (
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RESTConfig : configuration du pod (in-cluster), sinon le fichier désigné par
// KUBECONFIG (exécution locale, ex. contre un cluster k3d de dev).
func RESTConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		return nil, fmt.Errorf("not running in a cluster and KUBECONFIG is not set: %w", err)
	}
	cfg, err = clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("load KUBECONFIG %s: %w", path, err)
	}
	return cfg, nil
}
