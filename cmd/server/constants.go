package main

import "time"

// Élection de leader (une seule réplique du watcher active à la fois).
const (
	// envPodNamespace : espace de noms du pod (downward API).
	envPodNamespace      = "POD_NAMESPACE"
	defaultLockNamespace = "default"
	leaderLockName       = "watcher-leader-lock"

	leaseDuration = 15 * time.Second
	renewDeadline = 10 * time.Second
	retryPeriod   = 2 * time.Second
)

// Serveur HTTP de santé, de métriques et de profilage.
const (
	pathHealthz = "/healthz"
	pathReadyz  = "/readyz"
	pathMetrics = "/metrics"

	pathPprof        = "/debug/pprof/"
	pathPprofCmdline = "/debug/pprof/cmdline"
	pathPprofProfile = "/debug/pprof/profile"
	pathPprofSymbol  = "/debug/pprof/symbol"
	pathPprofTrace   = "/debug/pprof/trace"

	bodyHealthy = "ok"
	bodyReady   = "ready"

	httpReadTimeout     = 5 * time.Second
	httpWriteTimeout    = 5 * time.Second
	httpShutdownTimeout = 5 * time.Second
)
