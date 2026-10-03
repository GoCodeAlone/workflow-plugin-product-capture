//go:build !linux

package provider

import "errors"

func acquireBrowserProfileLockForTest(string) (func() error, error) {
	return nil, errors.New("persistent browser profiles are unsupported on this platform")
}
