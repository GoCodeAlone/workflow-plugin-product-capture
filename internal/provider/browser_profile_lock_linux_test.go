//go:build linux

package provider

import (
	"errors"
	"sync"
)

func acquireBrowserProfileLockForTest(profileDir string) (func() error, error) {
	dir, err := openBrowserProfileDirectory(profileDir)
	if err != nil {
		return nil, err
	}
	lock, err := acquireBrowserProfileLockAt(profileDir, int(dir.Fd()))
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			releaseErr = errors.Join(lock.release(), dir.Close())
		})
		return releaseErr
	}, nil
}
