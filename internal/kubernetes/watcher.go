package kubernetes

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"go.uber.org/zap"

	"github.com/thekrauss/watcher/internal/config"
	"github.com/thekrauss/watcher/internal/metrics"
	"github.com/thekrauss/watcher/internal/restate"
)

// Watcher defines the interface for the Kubernetes resource manager.
type IWatcher interface {
	Run(ctx context.Context) error
}
type Manager struct {
	cfg           config.Config
	clientset     *kubernetes.Clientset
	log           *zap.SugaredLogger
	processor     Processor
	informers     map[string]context.CancelFunc
	informerMu    sync.Mutex
	restateClient restate.IRestateClient
}

func NewManager(cfg config.Config, rc restate.IRestateClient, log *zap.SugaredLogger) (*Manager, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("build in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	processor := NewEventProcessor(rc, log, 3*time.Minute)

	return &Manager{
		cfg:           cfg,
		clientset:     clientset,
		log:           log,
		processor:     processor,
		informers:     make(map[string]context.CancelFunc),
		restateClient: rc,
	}, nil
}

func (m *Manager) Run(ctx context.Context) error {
	// Enroll cluster on startup
	if err := m.restateClient.Enroll(ctx); err != nil {
		m.log.Errorw("failed to enroll cluster", "err", err)
		// We continue anyway as enrollment might be optional or fails due to network
	}

	go m.processor.Run(ctx)
	go m.processor.CleanupLoop(ctx)
	go m.configSyncLoop(ctx)

	if m.cfg.WatchNamespaceMode == "single" {
		m.startNamespaceInformer(ctx, m.cfg.WatchNamespace)
		<-ctx.Done()
		return nil
	}

	// Label mode: Dynamic discovery of namespaces
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		m.discoverNamespaces(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}
}

func (m *Manager) configSyncLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	// Initial fetch
	if cfg, err := m.restateClient.GetRemoteConfig(ctx); err == nil {
		m.processor.ApplyRemoteConfig(cfg)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg, err := m.restateClient.GetRemoteConfig(ctx)
			if err != nil {
				m.log.Warnw("failed to fetch remote config", "err", err)
				continue
			}
			m.processor.ApplyRemoteConfig(cfg)
		}
	}
}

func (m *Manager) discoverNamespaces(ctx context.Context) {
	selector := fmt.Sprintf("%s=true", m.cfg.ManagedNSLabel)
	nsList, err := m.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		m.log.Errorw("failed to list namespaces", "selector", selector, "err", err)
		return
	}

	m.informerMu.Lock()
	defer m.informerMu.Unlock()

	activeNS := make(map[string]bool)
	for _, ns := range nsList.Items {
		activeNS[ns.Name] = true
		if _, ok := m.informers[ns.Name]; !ok {
			m.log.Infow("starting watcher for new namespace", "namespace", ns.Name)
			nsCtx, cancel := context.WithCancel(ctx)
			m.informers[ns.Name] = cancel
			go m.startNamespaceInformer(nsCtx, ns.Name)
		}
	}

	// Stop informers for namespaces no longer managed
	for ns, cancel := range m.informers {
		if !activeNS[ns] {
			m.log.Infow("stopping watcher for namespace", "namespace", ns)
			cancel()
			delete(m.informers, ns)
		}
	}
}

func (m *Manager) startNamespaceInformer(ctx context.Context, namespace string) {
	factory := informers.NewSharedInformerFactoryWithOptions(m.clientset, 10*time.Minute, informers.WithNamespace(namespace))
	podInformer := factory.Core().V1().Pods().Informer()

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			m.handlePodEvent("Added", obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			m.handlePodEvent("Modified", newObj)
		},
		DeleteFunc: func(obj interface{}) {
			m.handlePodEvent("Deleted", obj)
		},
	})

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced) {
		m.log.Errorw("failed to sync cache", "namespace", namespace)
		return
	}

	m.log.Infow("informer synced and running", "namespace", namespace)
	metrics.ReconnectsTotal.Inc()
	<-ctx.Done()
}

func (m *Manager) handlePodEvent(eventType string, obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	payload := m.toPodEvent(eventType, pod)
	metrics.EventsTotal.WithLabelValues(eventType, payload.Reason, pod.Namespace).Inc()
	m.processor.Process(context.Background(), payload)
}

func (m *Manager) toPodEvent(eventType string, pod *corev1.Pod) restate.PodEvent {
	reason := ""
	message := ""
	containerName := ""

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason = cs.State.Waiting.Reason
			message = cs.State.Waiting.Message
			containerName = cs.Name
			break
		}
		if cs.State.Terminated != nil {
			reason = cs.State.Terminated.Reason
			message = cs.State.Terminated.Message
			containerName = cs.Name
			if reason == "Completed" {
				continue
			}
			break
		}
	}

	restartCount := int32(0)
	for _, cs := range pod.Status.ContainerStatuses {
		restartCount += cs.RestartCount
	}

	return restate.PodEvent{
		EventID:       fmt.Sprintf("%s-%d", pod.UID, time.Now().UnixNano()),
		Namespace:     pod.Namespace,
		Name:          pod.Name,
		Phase:         string(pod.Status.Phase),
		Reason:        reason,
		Message:       message,
		RestartCount:  int(restartCount),
		ContainerName: containerName,
		Labels:        pod.Labels,
	}
}
