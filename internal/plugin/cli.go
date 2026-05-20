package plugin

import (
	"fmt"
	"io"
	"os"

	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/provider"
)

type CLIProvider struct {
	stdout io.Writer
	stderr io.Writer
}

func NewCLIProvider() *CLIProvider {
	return &CLIProvider{stdout: os.Stdout, stderr: os.Stderr}
}

func (c *CLIProvider) RunCLI(args []string) int {
	if len(args) == 0 || args[0] != "product-capture" {
		c.usage()
		return 2
	}
	if len(args) < 2 {
		c.usage()
		return 2
	}
	switch args[1] {
	case "probe":
		if err := provider.WriteProbe(c.stdout); err != nil {
			fmt.Fprintf(c.stderr, "product-capture probe: %v\n", err)
			return 1
		}
		return 0
	case "help", "-h", "--help":
		c.usage()
		return 0
	default:
		fmt.Fprintf(c.stderr, "unknown product-capture subcommand %q\n", args[1])
		c.usage()
		return 2
	}
}

func (c *CLIProvider) usage() {
	fmt.Fprintln(c.stderr, `Usage:
  wfctl product-capture probe

Subcommands:
  product-capture probe   Report provider capability status`)
}
