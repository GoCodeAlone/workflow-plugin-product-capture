package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/releaseprep"
)

func main() {
	var opts releaseprep.Options
	flag.StringVar(&opts.ManifestPath, "manifest", "plugin.json", "plugin manifest path")
	flag.StringVar(&opts.Tag, "tag", "", "release tag, defaults to v<plugin.json.version>")
	flag.BoolVar(&opts.Write, "write", false, "rewrite plugin.json release metadata instead of checking it")
	flag.Parse()

	if err := releaseprep.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
