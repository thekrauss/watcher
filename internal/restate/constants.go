package restate

import "time"

// Endpoints du backend (format Restate : <base>/<Service>/<clé>/<handler>).
const (
	enrollEndpointFmt         = "%s/ClusterService/Enroll"
	remoteConfigEndpointFmt   = "%s/ConfigService/%s/Get"
	podEventEndpointFmt       = "%s/ProjectService/%s/onPodEvent"
	serviceAccountKeysEndpFmt = "%s/ClusterService/%s/ServiceAccountKeys"
)

// agentVersion : version du watcher annoncée à l'enrôlement.
const agentVersion = "v1.0.0"

const (
	headerContentType = "Content-Type"
	mimeJSON          = "application/json"
)

// Disjoncteur des appels au backend.
const (
	breakerName = "restate-client"
	// breakerMaxRequests : requêtes autorisées en état semi-ouvert.
	breakerMaxRequests = 5
	breakerInterval    = 10 * time.Second
	// breakerOpenTimeout : durée de l'état ouvert avant un nouvel essai.
	breakerOpenTimeout = 30 * time.Second
	// breakerTripAfter : échecs consécutifs qui ouvrent le disjoncteur.
	breakerTripAfter = 5
)

// podEventBackoff : attentes entre les essais d'envoi d'un événement de pod.
var podEventBackoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
