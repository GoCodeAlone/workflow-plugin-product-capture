package main

import (
	"os"

	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/plugin"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(plugin.NewCLIProvider().RunCLI(os.Args[1:]))
	}
	sdk.Serve(plugin.NewPlugin(), sdk.WithBuildVersion(sdk.ResolveBuildVersion(plugin.Version)))
}
