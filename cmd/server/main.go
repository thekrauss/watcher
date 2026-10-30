package main

import (
	"context"
	"fmt"
	"github.com/thekrauss/watcher/internal/auth"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s_client "k8s.io/client-go/kubernetes"
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

	var authn auth.Authenticator = auth.None{}
	switch {
	case cfg.KeyFile != "":
		ts, err := auth.LoadKeyFile(cfg.KeyFile, cfg.ClusterID, nil)
		if err != nil {
			log.Fatalw("invalid key file", "err", err)
		}
		authn = ts
		log.Infow("auth: service account key file (short-lived STS tokens)")
	case cfg.APIKey != "":
		authn = auth.APIKey(cfg.APIKey)
		log.Infow("auth: API key (prefer --key-file: no long-lived secret on the wire)")
	default:
		log.Warnw("no credentials configured — requests are sent unauthenticated " +
			"(set --key-file / WATCHER_KEY_FILE, or --api-key / WATCHER_API_KEY)")
	}
	rc := restate.NewRestateClient(cfg.RestateURL, cfg.ClusterID, authn, cfg.RestateCallTimeout, log)

	mgr, err := kubernetes.NewManager(cfg, rc, log)
	if err != nil {
		log.Fatalw("failed to create manager", "err", err)
	}

	// V3 Leader Election
	runWithLeaderElection(ctx, cfg, mgr, log, ready)
}

func runWithLeaderElection(ctx context.Context, cfg config.Config, mgr kubernetes.IWatcher, log *zap.SugaredLogger, ready *atomic.Bool) {
	k8sCfg, err := kubernetes.RESTConfig()
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
	ns := os.Getenv(envPodNamespace)
	if ns == "" {
		ns = defaultLockNamespace
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaderLockName,
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
		LeaseDuration:   leaseDuration,
		RenewDeadline:   renewDeadline,
		RetryPeriod:     retryPeriod,
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

	mux.HandleFunc(pathHealthz, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(bodyHealthy))
	})

	mux.HandleFunc(pathReadyz, func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "not leading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(bodyReady))
	})

	mux.Handle(pathMetrics, promhttp.Handler())

	// pprof
	mux.HandleFunc(pathPprof, http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc(pathPprofCmdline, http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc(pathPprofProfile, http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc(pathPprofSymbol, http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc(pathPprofTrace, http.DefaultServeMux.ServeHTTP)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  httpReadTimeout,
		WriteTimeout: httpWriteTimeout,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
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
