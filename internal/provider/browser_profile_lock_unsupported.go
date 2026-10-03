//go:build aix || darwin || dragonfly || freebsd || illumos || js || netbsd || openbsd || plan9 || solaris || wasip1

package provider

import "errors"

func acquireManagedBrowserProfile(string) (managedBrowserProfile, error) {
	return managedBrowserProfile{}, errors.New("persistent browser profiles are unsupported on this platform")
}
