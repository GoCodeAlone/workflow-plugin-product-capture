package plugin

import "testing"

func TestManifest(t *testing.T) {
	manifest := NewPlugin().Manifest()
	if manifest.Name != "workflow-plugin-product-capture" {
		t.Fatalf("manifest name: %q", manifest.Name)
	}
}
