package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/thekrauss/watcher/internal/metrics"
	"github.com/thekrauss/watcher/internal/restate"
	"go.uber.org/zap"
)

type PodMetadata struct {
	ProjectID  string
	WorkloadID string
}

type EventProcessor struct {
	restateClient  restate.IRestateClient
	log            *zap.SugaredLogger
	dedupMap       sync.Map
	metadataMap    sync.Map // Cache for pod metadata (ProjectID, WorkloadID)
	cooldown       time.Duration
	batchSize      int
	batchFlush     time.Duration
	eventChan      chan restate.PodEvent
	mu             sync.RWMutex
	enabledReasons map[string]bool
}

// Processor defines the interface for event processing logic.
type Processor interface {
	ApplyRemoteConfig(cfg *restate.RemoteConfig)
	Process(ctx context.Context, event restate.PodEvent)
	Run(ctx context.Context)
	CleanupLoop(ctx context.Context)
	GetMetadata(podName string) (projectID, workloadID string, ok bool)
}

func NewEventProcessor(rc restate.IRestateClient, log *zap.SugaredLogger, cooldown time.Duration) *EventProcessor {
	return &EventProcessor{
		restateClient: rc,
		log:           log,
		cooldown:      cooldown,
		batchSize:     50,
		batchFlush:    time.Second,
		eventChan:     make(chan restate.PodEvent, 1000),
		enabledReasons: map[string]bool{
			"CrashLoopBackOff":           true,
			"ImagePullBackOff":           true,
			"CreateContainerConfigError": true,
			"OOMKilled":                  true,
			"Failed":                     true,
			"Evicted":                    true,
		},
	}
}

func (p *EventProcessor) ApplyRemoteConfig(cfg *restate.RemoteConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cfg.DedupCooldown > 0 {
		p.cooldown = cfg.DedupCooldown
	}
	if cfg.BatchSize > 0 {
		p.batchSize = cfg.BatchSize
	}
	if cfg.BatchFlush > 0 {
		p.batchFlush = cfg.BatchFlush
	}
	if len(cfg.EnabledReasons) > 0 {
		newReasons := make(map[string]bool)
		for _, r := range cfg.EnabledReasons {
			newReasons[r] = true
		}
		p.enabledReasons = newReasons
	}
	p.log.Infow("remote configuration applied",
		"cooldown", p.cooldown,
		"batchSize", p.batchSize,
		"batchFlush", p.batchFlush)
}

func (p *EventProcessor) Process(ctx context.Context, event restate.PodEvent) {
	// Extract metadata from labels if available
	projectID := event.Labels["project-id"]
	workloadID := event.Labels["workload-id"]
	if projectID != "" || workloadID != "" {
		p.metadataMap.Store(event.Name, PodMetadata{
			ProjectID:  projectID,
			WorkloadID: workloadID,
		})
	}

	if !p.shouldProcess(event) {
		return
	}

	p.mu.RLock()
	currentCooldown := p.cooldown
	p.mu.RUnlock()

	key := fmt.Sprintf("%s/%s/%s/%s", event.Namespace, event.Name, event.ContainerName, event.Reason)
	if lastSeen, ok := p.dedupMap.Load(key); ok {
		if time.Since(lastSeen.(time.Time)) < currentCooldown {
			metrics.EventsDedupedTotal.Inc()
			return
		}
	}
	p.dedupMap.Store(key, time.Now())

	select {
	case p.eventChan <- event:
		metrics.QueueDepth.Inc()
	default:
		p.log.Warnw("event queue full, dropping event", "pod", event.Name)
	}
}

func (p *EventProcessor) GetMetadata(podName string) (projectID, workloadID string, ok bool) {
	if val, found := p.metadataMap.Load(podName); found {
		meta := val.(PodMetadata)
		return meta.ProjectID, meta.WorkloadID, true
	}
	return "", "", false
}

func (p *EventProcessor) Run(ctx context.Context) {
	p.mu.RLock()
	currentFlush := p.batchFlush
	p.mu.RUnlock()

	ticker := time.NewTicker(currentFlush)
	defer ticker.Stop()

	var batch []restate.PodEvent

	flush := func() {
		if len(batch) == 0 {
			return
		}

		// parallel notifications to avoid blocking the main loop
		var wg sync.WaitGroup
		for _, ev := range batch {
			wg.Add(1)
			go func(e restate.PodEvent) {
				defer wg.Done()
				status := "success"
				err := p.restateClient.NotifyPodEvent(ctx, e)
				if err != nil {
					p.log.Errorw("failed to notify restate", "err", err, "pod", e.Name)
					status = "error"
				}
				p.auditLog(e, status)
				metrics.QueueDepth.Dec()
			}(ev)
		}
		wg.Wait()
		batch = batch[:0]
	}

	for {
		p.mu.RLock()
		batchSize := p.batchSize
		flushInterval := p.batchFlush
		p.mu.RUnlock()

		if flushInterval != currentFlush {
			currentFlush = flushInterval
			ticker.Reset(currentFlush)
		}

		select {
		case ev := <-p.eventChan:
			batch = append(batch, ev)
			if len(batch) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

func (p *EventProcessor) auditLog(event restate.PodEvent, status string) {
	data, _ := json.Marshal(event)
	hash := sha256.Sum256(data)
	payloadHash := hex.EncodeToString(hash[:])

	p.log.Infow("AUDIT EVENT",
		"event_id", event.EventID,
		"delivery_status", status,
		"payload_hash", payloadHash,
		"namespace", event.Namespace,
		"pod", event.Name,
		"phase", event.Phase,
	)
}

func (p *EventProcessor) shouldProcess(event restate.PodEvent) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.enabledReasons[event.Reason] {
		return true
	}
	if event.Phase == "Pending" && event.Reason != "" {
		return true
	}
	if event.RestartCount > 0 {
		return true
	}
	return false
}

func (p *EventProcessor) CleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.RLock()
			currentCooldown := p.cooldown
			p.mu.RUnlock()
			p.dedupMap.Range(func(key, value interface{}) bool {
				if time.Since(value.(time.Time)) > currentCooldown*2 {
					p.dedupMap.Delete(key)
				}
				return true
			})
		}
	}
}
