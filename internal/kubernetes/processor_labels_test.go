package kubernetes

import "testing"

// Labels du chart kubemanager (km-*) lus en priorité ; anciens noms en repli.
func TestFirstLabel(t *testing.T) {
	chart := map[string]string{"km-workload-id": "w1", "workload-id": "ancien"}
	if got := firstLabel(chart, labelWorkloadID, legacyLabelWorkloadID); got != "w1" {
		t.Fatalf("label du chart attendu, obtenu %q", got)
	}
	legacy := map[string]string{"project-id": "p1"}
	if got := firstLabel(legacy, labelProjectID, legacyLabelProjectID); got != "p1" {
		t.Fatalf("repli sur l'ancien nom attendu, obtenu %q", got)
	}
	if got := firstLabel(nil, labelProjectID); got != "" {
		t.Fatalf("aucun label : vide attendu, obtenu %q", got)
	}
}
