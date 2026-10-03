//go:build linux

package provider

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

type browserProfileLock struct {
	file *os.File
	dev  uint64
	ino  uint64
	once sync.Once
	err  error
}

func acquireBrowserProfileLockAt(profileDir string, dirFD int) (*browserProfileLock, error) {
	lockPath := filepath.Join(profileDir, ".product-capture.lock")
	fd, err := unix.Openat(dirFD, ".product-capture.lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open browser profile lock: %w", err)
	}
	lockFile := os.NewFile(uintptr(fd), lockPath)
	if lockFile == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open browser profile lock: invalid file descriptor")
	}
	if err := validatePrivateOwnedBrowserProfileFD(int(lockFile.Fd()), false); err != nil {
		_ = lockFile.Close()
		return nil, err
	}
	var openedStat unix.Stat_t
	if err := unix.Fstat(int(lockFile.Fd()), &openedStat); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("inspect opened browser profile lock: %w", err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lockFile.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("browser profile is already active")
		}
		return nil, fmt.Errorf("lock browser profile: %w", err)
	}
	var pathStat unix.Stat_t
	err = unix.Fstatat(dirFD, ".product-capture.lock", &pathStat, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil || openedStat.Dev != pathStat.Dev || openedStat.Ino != pathStat.Ino || pathStat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		_ = lockFile.Close()
		return nil, errors.Join(errors.New("browser profile lock path changed during acquisition"), err)
	}
	return &browserProfileLock{
		file: lockFile,
		dev:  uint64(openedStat.Dev),
		ino:  openedStat.Ino,
	}, nil
}

func (l *browserProfileLock) configureCommand(cmd *exec.Cmd) (int, error) {
	if l == nil || l.file == nil {
		return 0, errors.New("browser profile lock is unavailable")
	}
	if cmd == nil || cmd.Process != nil {
		return 0, errors.New("browser profile command is unavailable or already started")
	}
	childFD := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, l.file)
	return childFD, nil
}

func (l *browserProfileLock) release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.file != nil {
			l.err = l.file.Close()
		}
	})
	return l.err
}
