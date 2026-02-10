package kubernetes

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
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

	processor := NewEventProcessor(rc, log, 3*time.Minute, cfg.BatchSize)

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

	// Label mode: Dynamic discovery of namespaces via Informer
	m.startNamespaceDiscoveryInformer(ctx)
	<-ctx.Done()
	return nil
}

func (m *Manager) startNamespaceDiscoveryInformer(ctx context.Context) {
	factory := informers.NewSharedInformerFactory(m.clientset, 10*time.Minute)
	nsInformer := factory.Core().V1().Namespaces().Informer()

	labelKey := m.cfg.ManagedNSLabel

	nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				return
			}
			if ns.Labels[labelKey] == "true" {
				m.ensureNamespaceWatcher(ctx, ns.Name)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newNS, ok := newObj.(*corev1.Namespace)
			if !ok {
				return
			}
			oldNS, ok := oldObj.(*corev1.Namespace)
			if !ok {
				return
			}

			// Check if label was added or removed
			isManagedNow := newNS.Labels[labelKey] == "true"
			wasManaged := oldNS.Labels[labelKey] == "true"

			if isManagedNow && !wasManaged {
				m.ensureNamespaceWatcher(ctx, newNS.Name)
			} else if !isManagedNow && wasManaged {
				m.stopNamespaceWatcher(newNS.Name)
			}
		},
		DeleteFunc: func(obj interface{}) {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				// Handle DeletedFinalStateUnknown
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					if ns, ok = tombstone.Obj.(*corev1.Namespace); ok {
						m.stopNamespaceWatcher(ns.Name)
					}
				}
				return
			}
			m.stopNamespaceWatcher(ns.Name)
		},
	})

	go factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), nsInformer.HasSynced) {
		m.log.Error("failed to sync namespace discovery cache")
		return
	}
	m.log.Infow("namespace discovery informer started", "label", labelKey)
}

func (m *Manager) ensureNamespaceWatcher(ctx context.Context, namespace string) {
	m.informerMu.Lock()
	defer m.informerMu.Unlock()

	if _, ok := m.informers[namespace]; !ok {
		m.log.Infow("starting watcher for new namespace", "namespace", namespace)
		nsCtx, cancel := context.WithCancel(ctx)
		m.informers[namespace] = cancel
		go m.startNamespaceInformer(nsCtx, namespace)
	}
}

func (m *Manager) stopNamespaceWatcher(namespace string) {
	m.informerMu.Lock()
	defer m.informerMu.Unlock()

	if cancel, ok := m.informers[namespace]; ok {
		m.log.Infow("stopping watcher for namespace", "namespace", namespace)
		cancel()
		delete(m.informers, namespace)
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
		// Use UID, ResourceVersion and Reason to create a deterministic EventID for idempotency
		EventID:       fmt.Sprintf("%s-%s-%s", pod.UID, pod.ResourceVersion, reason),
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
