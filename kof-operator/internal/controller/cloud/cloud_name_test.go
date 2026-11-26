package cloud

import "testing"

// Die Anbietermenge ist der ganze Zweck dieses Forks.
//
// `getCloud` liest den Anbieter aus `infrastructure-<name>` in
// `ClusterTemplate.status.providers` und gibt ihn nur weiter, wenn
// `IsValidName` ihn kennt. Kennt es ihn nicht, entstehen **keine**
// kof-ConfigMaps, kein `kof-cluster-config-<kind>` und kein VMUser - das
// Kind-ClusterDeployment bleibt bei `Services: N/M` stehen, und keine
// Telemetrie erreicht den regionalen Cluster. Das Symptom sieht nicht nach
// "ein Name fehlt in einer Liste" aus.
//
// Upstream kennt weder Hetzner noch StackIT und wird es nach unserer
// Einschaetzung nie tun. Dieser Test ist deshalb die Stelle, an der ein
// Upgrade bemerkt, dass der Patch verloren gegangen ist.
func TestIsValidName(t *testing.T) {
	for _, name := range []string{Hetzner, StackIT, AWS, Azure, Docker, GCP, OpenStack, VSphere, Remote, Adopted} {
		if !IsValidName(name) {
			t.Errorf("IsValidName(%q) = false, want true — the fork patch is incomplete", name)
		}
	}

	for _, name := range []string{"", "Hetzner", "stackit-cloud", "ovh"} {
		if IsValidName(name) {
			t.Errorf("IsValidName(%q) = true, want false", name)
		}
	}
}
