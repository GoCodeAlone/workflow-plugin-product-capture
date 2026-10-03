//go:build windows

package provider

import "errors"

func acquireManagedBrowserProfile(string) (managedBrowserProfile, error) {
	return managedBrowserProfile{}, errors.New("persistent browser profiles are unsupported on Windows")
}
