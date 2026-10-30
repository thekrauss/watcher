package kubernetes

import "time"

// Labels lus sur les pods pour rattacher un événement à un projet/workload :
// ceux du chart standard-app de kubemanager, puis les anciens noms.
const (
	labelProjectID        = "km-project-id"
	labelWorkloadID       = "km-workload-id"
	legacyLabelProjectID  = "project-id"
	legacyLabelWorkloadID = "workload-id"
)

// Raisons Kubernetes transmises au backend par défaut (surchargées par la
// configuration distante).
const (
	reasonCrashLoopBackOff           = "CrashLoopBackOff"
	reasonImagePullBackOff           = "ImagePullBackOff"
	reasonCreateContainerConfigError = "CreateContainerConfigError"
	reasonOOMKilled                  = "OOMKilled"
	reasonFailed                     = "Failed"
	reasonEvicted                    = "Evicted"
	// reasonCompleted : conteneur terminé normalement, ignoré.
	reasonCompleted = "Completed"
	// phasePending : pod en attente, transmis dès qu'une raison est connue.
	phasePending = "Pending"
)

// Types d'événements d'informer.
const (
	eventAdded    = "Added"
	eventModified = "Modified"
	eventDeleted  = "Deleted"
)

// Traitement des événements (valeurs par défaut).
const (
	defaultBatchSize  = 50
	defaultBatchFlush = time.Second
	eventQueueSize    = 1000
	// defaultDedupCooldown : fenêtre de déduplication d'un même événement.
	defaultDedupCooldown = 3 * time.Minute
	cleanupInterval      = 10 * time.Minute
)

// Informers et découverte des espaces de noms.
const (
	informerResync           = 10 * time.Minute
	namespaceDiscoveryPeriod = 30 * time.Second
	// managedLabelSelectorFmt : <label>=true.
	managedLabelSelectorFmt = "%s=true"
)

// Découverte OIDC de l'émetteur des comptes de service Kubernetes.
const (
	oidcDiscoveryPath = "/.well-known/openid-configuration"
	oidcJWKSPath      = "/openid/v1/jwks"
)

// envKubeconfig : fichier kubeconfig hors cluster (exécution locale).
const envKubeconfig = "KUBECONFIG"
