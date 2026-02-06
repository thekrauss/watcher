package main

import (
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s_client "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/thekrauss/watcher/internal/config"
	"github.com/thekrauss/watcher/internal/kubernetes"
	"github.com/thekrauss/watcher/internal/restate"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger, logErr := zap.NewProduction()
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "logger init error: %v\n", logErr)
		os.Exit(1)
	}
	defer logger.Sync()
	log := logger.Sugar()

	ready := &atomic.Bool{}
	go startHTTPServer(ctx, cfg.HTTPListen, ready, log)

	rc := restate.NewRestateClient(cfg.RestateURL, cfg.ClusterID, cfg.RestateCallTimeout, log)

	mgr, err := kubernetes.NewManager(cfg, rc, log)
	if err != nil {
		log.Fatalw("failed to create manager", "err", err)
	}

	// V3 Leader Election
	runWithLeaderElection(ctx, cfg, mgr, log, ready)
}

func runWithLeaderElection(ctx context.Context, cfg config.Config, mgr kubernetes.IWatcher, log *zap.SugaredLogger, ready *atomic.Bool) {
	k8sCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalw("failed to get kubernetes config", "err", err)
	}

	clientset, err := k8s_client.NewForConfig(k8sCfg)
	if err != nil {
		log.Fatalw("failed to create kubernetes clientset", "err", err)
	}

	id, err := os.Hostname()
	if err != nil {
		log.Fatalw("failed to get hostname", "err", err)
	}
	// Add some randomness if needed, but hostname is usually enough in Pods
	id = id + "-" + fmt.Sprintf("%d", os.Getpid())

	// Use the namespace where the pod is running, fall back to "default"
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		ns = "default"
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "watcher-leader-lock",
			Namespace: ns,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.Infow("started leading, starting manager", "id", id)
				ready.Store(true)
				if err := mgr.Run(ctx); err != nil {
					log.Errorw("manager terminated", "err", err)
				}
			},
			OnStoppedLeading: func() {
				log.Infow("stopped leading", "id", id)
				ready.Store(false)
			},
			OnNewLeader: func(identity string) {
				if identity == id {
					return
				}
				log.Infow("new leader elected", "leader", identity)
			},
		},
	})
}

func startHTTPServer(ctx context.Context, addr string, ready *atomic.Bool, log *zap.SugaredLogger) {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "not leading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	mux.Handle("/metrics", promhttp.Handler())

	// pprof
	mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/cmdline", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/profile", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/symbol", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/trace", http.DefaultServeMux.ServeHTTP)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Warnw("http server shutdown", "err", err)
		}
	}()

	log.Infow("http server listening (health/metrics/pprof)", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Errorw("http server error", "err", err)
	}
}
