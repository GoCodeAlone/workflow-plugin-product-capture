package provider

import (
	"go/build"
	"testing"
)

func TestBrowserProcessPolicyBuildSelection(t *testing.T) {
	policyFiles := []string{
		"browser_process_linux.go",
		"browser_process_darwin.go",
		"browser_process_windows.go",
		"browser_process_unsupported_unix.go",
		"browser_process_nonunix.go",
	}
	tests := map[string]string{
		"linux":     "browser_process_linux.go",
		"android":   "browser_process_linux.go",
		"darwin":    "browser_process_darwin.go",
		"ios":       "browser_process_darwin.go",
		"windows":   "browser_process_windows.go",
		"aix":       "browser_process_unsupported_unix.go",
		"dragonfly": "browser_process_unsupported_unix.go",
		"freebsd":   "browser_process_unsupported_unix.go",
		"illumos":   "browser_process_unsupported_unix.go",
		"netbsd":    "browser_process_unsupported_unix.go",
		"openbsd":   "browser_process_unsupported_unix.go",
		"solaris":   "browser_process_unsupported_unix.go",
		"plan9":     "browser_process_nonunix.go",
		"js":        "browser_process_nonunix.go",
		"wasip1":    "browser_process_nonunix.go",
	}

	for goos, want := range tests {
		t.Run(goos, func(t *testing.T) {
			context := build.Default
			context.GOOS = goos
			matched := ""
			for _, file := range policyFiles {
				ok, err := context.MatchFile(".", file)
				if err != nil {
					t.Fatalf("match %s: %v", file, err)
				}
				if !ok {
					continue
				}
				if matched != "" {
					t.Fatalf("multiple policies match: %s and %s", matched, file)
				}
				matched = file
			}
			if matched != want {
				t.Fatalf("matched policy = %q, want %q", matched, want)
			}
		})
	}
}
