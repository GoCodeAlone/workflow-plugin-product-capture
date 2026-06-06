package releaseprep

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/mod/semver"
)

const releaseURLPrefix = "https://github.com/GoCodeAlone/workflow-plugin-product-capture/releases/download/"

type Manifest struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Description      string            `json:"description,omitempty"`
	Author           string            `json:"author,omitempty"`
	License          string            `json:"license,omitempty"`
	Type             string            `json:"type,omitempty"`
	Tier             string            `json:"tier,omitempty"`
	Private          bool              `json:"private"`
	MinEngineVersion string            `json:"minEngineVersion,omitempty"`
	Keywords         []string          `json:"keywords,omitempty"`
	Homepage         string            `json:"homepage,omitempty"`
	Repository       string            `json:"repository,omitempty"`
	Capabilities     json.RawMessage   `json:"capabilities,omitempty"`
	Contracts        []json.RawMessage `json:"contracts,omitempty"`
	Downloads        []Download        `json:"downloads,omitempty"`
	raw              map[string]json.RawMessage
}

type Download struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	URL  string `json:"url"`
}

type Options struct {
	ManifestPath string
	Tag          string
	Write        bool
}

func Run(opts Options) error {
	if opts.ManifestPath == "" {
		opts.ManifestPath = "plugin.json"
	}
	manifest, err := Read(opts.ManifestPath)
	if err != nil {
		return err
	}
	tag := opts.Tag
	if tag == "" {
		tag = "v" + manifest.Version
	}
	updated, err := Prepare(manifest, tag)
	if err != nil {
		return err
	}
	if !opts.Write {
		return Check(manifest, updated)
	}
	return Write(opts.ManifestPath, updated)
}

func Read(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &manifest.raw); err != nil {
		return Manifest{}, fmt.Errorf("decode raw %s: %w", path, err)
	}
	return manifest, nil
}

func Prepare(manifest Manifest, tag string) (Manifest, error) {
	version, err := versionFromTag(tag)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Name == "" {
		return Manifest{}, errors.New("plugin.json.name is required")
	}
	if len(manifest.Downloads) == 0 {
		return Manifest{}, errors.New("plugin.json.downloads is required")
	}
	manifest.Version = version
	manifest.Downloads = append([]Download(nil), manifest.Downloads...)
	for i := range manifest.Downloads {
		dl := &manifest.Downloads[i]
		if dl.OS == "" || dl.Arch == "" {
			return Manifest{}, fmt.Errorf("downloads[%d] must declare os and arch", i)
		}
		dl.URL = fmt.Sprintf("%s%s/%s-%s-%s.tar.gz", releaseURLPrefix, tag, manifest.Name, dl.OS, dl.Arch)
	}
	return manifest, nil
}

func Check(current, expected Manifest) error {
	var problems []string
	if current.Version != expected.Version {
		problems = append(problems, fmt.Sprintf("plugin.json.version=%q, want %q", current.Version, expected.Version))
	}
	if len(current.Downloads) != len(expected.Downloads) {
		problems = append(problems, fmt.Sprintf("plugin.json.downloads has %d entries, want %d", len(current.Downloads), len(expected.Downloads)))
	}
	for i := range current.Downloads {
		if i >= len(expected.Downloads) {
			break
		}
		if current.Downloads[i] != expected.Downloads[i] {
			problems = append(problems, fmt.Sprintf("plugin.json.downloads[%d]=%+v, want %+v", i, current.Downloads[i], expected.Downloads[i]))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("release metadata is stale:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func Write(path string, manifest Manifest) error {
	fields, err := manifest.rawFields()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func (manifest Manifest) rawFields() (map[string]json.RawMessage, error) {
	fields := make(map[string]json.RawMessage, len(manifest.raw)+2)
	if len(manifest.raw) > 0 {
		for key, value := range manifest.raw {
			fields[key] = value
		}
	} else {
		data, err := json.Marshal(manifest)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
	}
	version, err := json.Marshal(manifest.Version)
	if err != nil {
		return nil, err
	}
	downloads, err := json.Marshal(manifest.Downloads)
	if err != nil {
		return nil, err
	}
	fields["version"] = version
	fields["downloads"] = downloads
	return fields, nil
}

func versionFromTag(tag string) (string, error) {
	if !semver.IsValid(tag) || semver.Prerelease(tag) != "" || semver.Build(tag) != "" {
		return "", fmt.Errorf("tag %q must be release semver vN.N.N", tag)
	}
	return strings.TrimPrefix(tag, "v"), nil
}
