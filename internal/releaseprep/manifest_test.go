package releaseprep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareUpdatesVersionAndDownloadURLs(t *testing.T) {
	manifest := sampleManifest()

	got, err := Prepare(manifest, "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.2.0" {
		t.Fatalf("version = %q, want 0.2.0", got.Version)
	}
	wantURL := "https://github.com/GoCodeAlone/workflow-plugin-product-capture/releases/download/v0.2.0/workflow-plugin-product-capture-linux-amd64.tar.gz"
	if got.Downloads[0].URL != wantURL {
		t.Fatalf("download url = %q, want %q", got.Downloads[0].URL, wantURL)
	}
}

func TestPrepareIncludesPublishedWindowsARM64Download(t *testing.T) {
	got, err := Prepare(sampleManifest(), "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	want := Download{
		OS:   "windows",
		Arch: "arm64",
		URL:  "https://github.com/GoCodeAlone/workflow-plugin-product-capture/releases/download/v0.2.0/workflow-plugin-product-capture-windows-arm64.tar.gz",
	}
	for _, download := range got.Downloads {
		if download == want {
			return
		}
	}
	t.Fatalf("prepared downloads missing %+v: %+v", want, got.Downloads)
}

func TestCheckRejectsStaleManifest(t *testing.T) {
	current := sampleManifest()
	expected, err := Prepare(current, "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}

	err = Check(current, expected)
	if err == nil {
		t.Fatal("expected stale manifest error")
	}
	if !strings.Contains(err.Error(), "plugin.json.version") ||
		!strings.Contains(err.Error(), "plugin.json.downloads[0]") {
		t.Fatalf("error did not include stale fields: %v", err)
	}
}

func TestRunWritesPreparedManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(path, []byte(`{
  "name": "workflow-plugin-product-capture",
  "version": "0.1.11",
  "futureField": {"kept": true},
  "downloads": [
    {
      "os": "linux",
      "arch": "amd64",
      "url": "https://github.com/GoCodeAlone/workflow-plugin-product-capture/releases/download/v0.1.11/workflow-plugin-product-capture-linux-amd64.tar.gz"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Run(Options{ManifestPath: path, Tag: "v0.2.0", Write: true}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"version": "0.2.0"`,
		`/releases/download/v0.2.0/`,
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("written manifest missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(string(got), `"futureField":`) {
		t.Fatalf("written manifest did not preserve unknown field:\n%s", got)
	}
}

func TestCommittedManifestMatchesSourceReleaseMetadata(t *testing.T) {
	manifest, err := Read(filepath.Join("..", "..", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := Prepare(manifest, "v"+manifest.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(manifest, expected); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRejectsNonReleaseTag(t *testing.T) {
	for _, tag := range []string{"0.2.0", "v0.2.0-rc.1", "v0.2.0+build"} {
		if _, err := Prepare(sampleManifest(), tag); err == nil {
			t.Fatalf("Prepare(%q) succeeded, want error", tag)
		}
	}
}

func sampleManifest() Manifest {
	return Manifest{
		Name:             "workflow-plugin-product-capture",
		Version:          "0.1.11",
		Description:      "Product URL capture provider for workflow-compute",
		Author:           "GoCodeAlone",
		License:          "MIT",
		Type:             "external",
		Tier:             "community",
		MinEngineVersion: "0.57.4",
		Keywords:         []string{"product-capture"},
		Homepage:         "https://github.com/GoCodeAlone/workflow-plugin-product-capture",
		Repository:       "https://github.com/GoCodeAlone/workflow-plugin-product-capture",
		Capabilities:     []byte(`{"stepTypes":["step.product_capture"]}`),
		Downloads: []Download{
			{
				OS:   "linux",
				Arch: "amd64",
				URL:  "https://github.com/GoCodeAlone/workflow-plugin-product-capture/releases/download/v0.1.11/workflow-plugin-product-capture-linux-amd64.tar.gz",
			},
		},
	}
}
