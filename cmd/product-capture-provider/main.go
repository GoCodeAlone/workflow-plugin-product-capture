package main

import (
	"os"

	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/provider"
)

func main() {
	os.Exit(provider.Main(os.Args[1:], os.Stdout, os.Stderr))
}
