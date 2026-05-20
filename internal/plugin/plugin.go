package plugin

import (
	"fmt"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

var Version = "0.0.0"

type productCapturePlugin struct{}

func NewPlugin() sdk.PluginProvider {
	return productCapturePlugin{}
}

func (productCapturePlugin) Manifest() sdk.PluginManifest {
	return sdk.PluginManifest{
		Name:        "workflow-plugin-product-capture",
		Version:     Version,
		Author:      "GoCodeAlone",
		Description: "Product URL capture provider for workflow-compute",
	}
}

func (productCapturePlugin) StepTypes() []string {
	return nil
}

func (productCapturePlugin) CreateStep(typeName, name string, config map[string]any) (sdk.StepInstance, error) {
	return nil, fmt.Errorf("product-capture plugin: unknown step type %q", typeName)
}
