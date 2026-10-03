//go:build linux

package provider

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	browserProfileMetadataName            = ".product-capture-profile.json"
	browserProfileOwnerName               = ".product-capture-chrome-owner.json"
	browserProfileLaunchName              = ".product-capture-chrome-launch.json"
	browserProfileConformanceBoundaryName = ".product-capture-chrome-conformance-pre-owner-ready"
	browserProfileSchema                  = "product-capture-browser-profile.v2"
	browserProfileOwnerSchema             = "product-capture-chrome-owner.v1"
	browserProfileOwnerSchema2            = "product-capture-chrome-owner.v2"
	browserProfileLaunchSchema            = "product-capture-chrome-launch.v1"
	maxBrowserProfileFileBytes            = 4096
)

var chromeSingletonNames = []string{"SingletonLock", "SingletonSocket", "SingletonCookie"}

var chromeSingletonCleanupOrder = []string{"SingletonSocket", "SingletonCookie", "SingletonLock"}

type browserProfileMetadata struct {
	Schema      string `json:"schema"`
	Generation  string `json:"generation"`
	ScopeSHA256 string `json:"scope_sha256"`
}

type chromeProfileOwner struct {
	Schema       string `json:"schema"`
	Generation   string `json:"generation"`
	ScopeSHA256  string `json:"scope_sha256,omitempty"`
	LaunchID     string `json:"launch_id,omitempty"`
	Hostname     string `json:"hostname"`
	PID          int    `json:"pid"`
	ProcessGroup int    `json:"processGroupId"`
	ProcessStart string `json:"processStart"`
}

type chromeProfileLaunch struct {
	Schema      string `json:"schema"`
	Generation  string `json:"generation"`
	ScopeSHA256 string `json:"scope_sha256"`
	LaunchID    string `json:"launch_id"`
	Hostname    string `json:"hostname"`
	LockDev     string `json:"lock_dev"`
	LockIno     string `json:"lock_ino"`
}

func acquireManagedBrowserProfile(profileDir string) (managedBrowserProfile, error) {
	return acquireManagedBrowserProfileWithIdentityReader(profileDir, rand.Reader)
}

func acquireManagedBrowserProfileWithIdentityReader(profileDir string, identityReader io.Reader) (managedBrowserProfile, error) {
	absDir, err := filepath.Abs(filepath.Clean(profileDir))
	if err != nil {
		return managedBrowserProfile{}, fmt.Errorf("resolve browser profile directory: %w", err)
	}
	scopeSHA256, err := configuredBrowserProfileScopeSHA256()
	if err != nil {
		return managedBrowserProfile{}, err
	}
	if err := rejectBrowserProfileSymlinkPath(absDir); err != nil {
		return managedBrowserProfile{}, err
	}
	if err := validateTrustedBrowserProfileParent(filepath.Dir(absDir)); err != nil {
		return managedBrowserProfile{}, err
	}
	created := false
	if err := os.Mkdir(absDir, 0o700); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return managedBrowserProfile{}, fmt.Errorf("create browser profile directory: %w", err)
	}
	if err := rejectBrowserProfileSymlinkPath(absDir); err != nil {
		return managedBrowserProfile{}, err
	}
	dir, err := openBrowserProfileDirectory(absDir)
	if err != nil {
		return managedBrowserProfile{}, err
	}
	closeDir := func(err error) (managedBrowserProfile, error) {
		return managedBrowserProfile{}, errors.Join(err, dir.Close())
	}

	metadata, err := loadOrCreateBrowserProfileMetadataAt(int(dir.Fd()), absDir, created, scopeSHA256, identityReader)
	if err != nil {
		return closeDir(errors.Join(err, rollbackEmptyNewBrowserProfileDirectory(absDir, dir, created)))
	}
	lock, err := acquireBrowserProfileLockAt(absDir, int(dir.Fd()))
	if err != nil {
		return closeDir(err)
	}
	releaseAll := func(err error) (managedBrowserProfile, error) {
		return managedBrowserProfile{}, errors.Join(err, lock.release(), dir.Close())
	}
	current, err := readBrowserProfileMetadataAt(int(dir.Fd()), absDir)
	if err != nil || current != metadata {
		return releaseAll(errors.Join(errors.New("browser profile ownership changed while acquiring lock"), err))
	}
	if err := verifyBrowserProfileDirectoryPath(absDir, dir); err != nil {
		return releaseAll(err)
	}
	if err := cleanupStaleChromeProfileLocks(int(dir.Fd()), absDir, metadata.Generation); err != nil {
		return releaseAll(err)
	}
	launch, err := createChromeProfileLaunchAt(int(dir.Fd()), absDir, metadata, lock, identityReader)
	if err != nil {
		return releaseAll(err)
	}

	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			releaseErr = errors.Join(lock.release(), dir.Close())
		})
		return releaseErr
	}
	return managedBrowserProfile{
		Dir:         absDir,
		Generation:  metadata.Generation,
		ScopeSHA256: metadata.ScopeSHA256,
		verify: func() error {
			return verifyBrowserProfileDirectoryPath(absDir, dir)
		},
		configure: func(cmd *exec.Cmd) error {
			childFD, err := lock.configureCommand(cmd)
			if err != nil {
				return err
			}
			cmd.Env = withEnvValue(cmd.Env, "PRODUCT_CAPTURE_BROWSER_PROFILE_LOCK_FD", strconv.Itoa(childFD))
			cmd.Env = withEnvValue(cmd.Env, "PRODUCT_CAPTURE_BROWSER_PROFILE_LAUNCH_ID", launch.LaunchID)
			return nil
		},
		release: release,
	}, nil
}

func rollbackEmptyNewBrowserProfileDirectory(path string, dir *os.File, created bool) error {
	if !created {
		return nil
	}
	if err := verifyBrowserProfileDirectoryPath(path, dir); err != nil {
		return fmt.Errorf("verify failed browser profile initialization: %w", err)
	}
	entries, err := dir.ReadDir(1)
	if err == nil || (err != nil && !errors.Is(err, io.EOF)) {
		if err != nil {
			return fmt.Errorf("inspect failed browser profile initialization: %w", err)
		}
		if len(entries) > 0 {
			return nil
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove failed browser profile initialization: %w", err)
	}
	return nil
}

func configuredBrowserProfileScopeSHA256() (string, error) {
	scope := strings.TrimSpace(os.Getenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE"))
	if len(scope) < 8 || len(scope) > 256 {
		return "", errors.New("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE must identify the trusted org/pool/provider enrollment and rotation")
	}
	for _, char := range scope {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:/@-", char) {
			continue
		}
		return "", errors.New("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE contains an unsupported character")
	}
	sum := sha256.Sum256([]byte(scope))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func rejectBrowserProfileSymlinkPath(path string) error {
	path = filepath.Clean(path)
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect browser profile path %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("browser profile path must not contain a symlink: %s", current)
		}
	}
	return nil
}

func openBrowserProfileDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open browser profile directory: %w", err)
	}
	dir := os.NewFile(uintptr(fd), path)
	if dir == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open browser profile directory: invalid file descriptor")
	}
	if err := validatePrivateOwnedBrowserProfileFD(fd, true); err != nil {
		_ = dir.Close()
		return nil, err
	}
	if err := verifyBrowserProfileDirectoryPath(path, dir); err != nil {
		_ = dir.Close()
		return nil, err
	}
	return dir, nil
}

func verifyBrowserProfileDirectoryPath(path string, dir *os.File) error {
	openedInfo, err := dir.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened browser profile directory: %w", err)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect browser profile directory path: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, pathInfo) {
		return errors.New("browser profile directory path changed after validation")
	}
	return nil
}

func validatePrivateOwnedBrowserProfileFD(fd int, directory bool) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect browser profile ownership: %w", err)
	}
	wantType := uint32(unix.S_IFREG)
	if directory {
		wantType = unix.S_IFDIR
	}
	if stat.Mode&unix.S_IFMT != wantType {
		return errors.New("browser profile path has an invalid type")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return errors.New("browser profile path is not owned by the current user")
	}
	if stat.Mode&0o077 != 0 {
		return errors.New("browser profile path permissions are not private")
	}
	return nil
}

func validateTrustedBrowserProfileParent(path string) error {
	current := filepath.Clean(path)
	immediate := true
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect browser profile ancestor %q: %w", current, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("browser profile ancestor is not a trusted directory: %s", current)
		}
		var stat unix.Stat_t
		if err := unix.Lstat(current, &stat); err != nil {
			return fmt.Errorf("inspect browser profile ancestor ownership %q: %w", current, err)
		}
		if stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0 {
			return fmt.Errorf("browser profile ancestor is not owned by the current user or root: %s", current)
		}
		if info.Mode().Perm()&0o022 != 0 {
			rootOwnedSticky := !immediate && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
			if !rootOwnedSticky {
				return fmt.Errorf("browser profile ancestor is writable by another user: %s", current)
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
		immediate = false
	}
}

func loadOrCreateBrowserProfileMetadataAt(dirFD int, profileDir string, created bool, scopeSHA256 string, identityReader io.Reader) (result browserProfileMetadata, retErr error) {
	metadata, err := readBrowserProfileMetadataAt(dirFD, profileDir)
	if err == nil {
		if metadata.ScopeSHA256 != scopeSHA256 {
			return browserProfileMetadata{}, errors.New("browser profile scope does not match this deployment")
		}
		return metadata, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return browserProfileMetadata{}, err
	}
	if !created {
		return browserProfileMetadata{}, errors.New("existing browser profile is not provider-owned")
	}
	if identityReader == nil {
		return browserProfileMetadata{}, errors.New("generate browser profile identity: entropy reader is unavailable")
	}
	var generationBytes [32]byte
	if _, err := io.ReadFull(identityReader, generationBytes[:]); err != nil {
		return browserProfileMetadata{}, fmt.Errorf("generate browser profile identity: %w", err)
	}
	metadata = browserProfileMetadata{
		Schema:      browserProfileSchema,
		Generation:  hex.EncodeToString(generationBytes[:]),
		ScopeSHA256: scopeSHA256,
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return browserProfileMetadata{}, fmt.Errorf("encode browser profile identity: %w", err)
	}
	temporaryName := browserProfileMetadataName + ".tmp-" + metadata.Generation
	temporaryPresent := false
	defer func() {
		if !temporaryPresent {
			return
		}
		if err := unix.Unlinkat(dirFD, temporaryName, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary browser profile identity: %w", err))
		}
	}()
	fd, err := unix.Openat(dirFD, temporaryName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return browserProfileMetadata{}, fmt.Errorf("create temporary browser profile identity: %w", err)
	}
	temporaryPresent = true
	file := os.NewFile(uintptr(fd), filepath.Join(profileDir, temporaryName))
	if file == nil {
		_ = unix.Close(fd)
		return browserProfileMetadata{}, errors.New("create temporary browser profile identity: invalid file descriptor")
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return browserProfileMetadata{}, fmt.Errorf("write browser profile identity: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return browserProfileMetadata{}, fmt.Errorf("sync browser profile identity: %w", err)
	}
	if err := file.Close(); err != nil {
		return browserProfileMetadata{}, fmt.Errorf("close browser profile identity: %w", err)
	}
	if err := unix.Renameat2(dirFD, temporaryName, dirFD, browserProfileMetadataName, unix.RENAME_NOREPLACE); err != nil {
		return browserProfileMetadata{}, fmt.Errorf("publish browser profile identity: %w", err)
	}
	temporaryPresent = false
	if err := unix.Fsync(dirFD); err != nil {
		return browserProfileMetadata{}, fmt.Errorf("sync browser profile directory: %w", err)
	}
	return metadata, nil
}

func readBrowserProfileMetadataAt(dirFD int, profileDir string) (browserProfileMetadata, error) {
	data, err := readPrivateBrowserProfileFileAt(dirFD, profileDir, browserProfileMetadataName)
	if err != nil {
		return browserProfileMetadata{}, err
	}
	var metadata browserProfileMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return browserProfileMetadata{}, fmt.Errorf("decode browser profile identity: %w", err)
	}
	if metadata.Schema != browserProfileSchema || len(metadata.Generation) != 64 || !validBrowserProfileScopeSHA256(metadata.ScopeSHA256) {
		return browserProfileMetadata{}, errors.New("browser profile identity is invalid")
	}
	if _, err := hex.DecodeString(metadata.Generation); err != nil {
		return browserProfileMetadata{}, errors.New("browser profile generation is invalid")
	}
	return metadata, nil
}

func validBrowserProfileScopeSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func createChromeProfileLaunchAt(
	dirFD int,
	profileDir string,
	metadata browserProfileMetadata,
	lock *browserProfileLock,
	identityReader io.Reader,
) (chromeProfileLaunch, error) {
	if lock == nil || lock.file == nil {
		return chromeProfileLaunch{}, errors.New("create Chrome launch journal: browser profile lock is unavailable")
	}
	if err := cleanupChromeRecoveryStateWithoutLocksAt(dirFD, profileDir, metadata, lock); err != nil {
		return chromeProfileLaunch{}, err
	}
	if identityReader == nil {
		return chromeProfileLaunch{}, errors.New("create Chrome launch journal: entropy reader is unavailable")
	}
	var launchBytes [32]byte
	if _, err := io.ReadFull(identityReader, launchBytes[:]); err != nil {
		return chromeProfileLaunch{}, fmt.Errorf("generate Chrome launch identity: %w", err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return chromeProfileLaunch{}, fmt.Errorf("read hostname for Chrome launch journal: %w", err)
	}
	launch := chromeProfileLaunch{
		Schema:      browserProfileLaunchSchema,
		Generation:  metadata.Generation,
		ScopeSHA256: metadata.ScopeSHA256,
		LaunchID:    hex.EncodeToString(launchBytes[:]),
		Hostname:    hostname,
		LockDev:     strconv.FormatUint(lock.dev, 10),
		LockIno:     strconv.FormatUint(lock.ino, 10),
	}
	if err := writePrivateBrowserProfileJSONAt(dirFD, profileDir, browserProfileLaunchName, launch.LaunchID, launch); err != nil {
		return chromeProfileLaunch{}, fmt.Errorf("publish Chrome launch journal: %w", err)
	}
	return launch, nil
}

func cleanupChromeRecoveryStateWithoutLocksAt(
	dirFD int,
	profileDir string,
	metadata browserProfileMetadata,
	lock *browserProfileLock,
) error {
	for _, name := range chromeSingletonNames {
		var stat unix.Stat_t
		if err := unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
			return fmt.Errorf("cannot reset Chrome recovery state while %s exists", name)
		} else if !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("inspect Chrome recovery state %s: %w", name, err)
		}
	}
	if owner, err := readChromeProfileOwnerAt(dirFD, profileDir); err == nil {
		if owner.Generation != metadata.Generation || (owner.Schema != browserProfileOwnerSchema && owner.Schema != browserProfileOwnerSchema2) {
			return errors.New("Chrome profile owner recovery state does not match this profile")
		}
		if err := unlinkPrivateBrowserProfileFileAt(dirFD, browserProfileOwnerName); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Chrome profile owner recovery state: %w", err)
	}
	if launch, err := readChromeProfileLaunchAt(dirFD, profileDir); err == nil {
		if err := validateChromeProfileLaunch(launch, metadata.Generation, metadata.ScopeSHA256, lock); err != nil {
			return err
		}
		if err := unlinkPrivateBrowserProfileFileAt(dirFD, browserProfileLaunchName); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Chrome launch recovery state: %w", err)
	}
	if err := unlinkPrivateBrowserProfileFileAt(dirFD, browserProfileConformanceBoundaryName); err != nil {
		return fmt.Errorf("remove Chrome conformance boundary recovery state: %w", err)
	}
	return nil
}

func writePrivateBrowserProfileJSONAt(dirFD int, profileDir, name, suffix string, value any) (retErr error) {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	temporaryName := name + ".tmp-" + suffix
	temporaryPresent := false
	defer func() {
		if temporaryPresent {
			if err := unix.Unlinkat(dirFD, temporaryName, 0); err != nil && !errors.Is(err, unix.ENOENT) {
				retErr = errors.Join(retErr, fmt.Errorf("remove temporary %s: %w", name, err))
			}
		}
	}()
	fd, err := unix.Openat(dirFD, temporaryName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", name, err)
	}
	temporaryPresent = true
	file := os.NewFile(uintptr(fd), filepath.Join(profileDir, temporaryName))
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("create temporary %s: invalid file descriptor", name)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary %s: %w", name, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", name, err)
	}
	if err := unix.Renameat2(dirFD, temporaryName, dirFD, name, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("publish %s: %w", name, err)
	}
	temporaryPresent = false
	if err := unix.Fsync(dirFD); err != nil {
		return fmt.Errorf("sync browser profile directory after publishing %s: %w", name, err)
	}
	return nil
}

func unlinkPrivateBrowserProfileFileAt(dirFD int, name string) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return fmt.Errorf("inspect browser profile file %s before removal: %w", name, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 {
		return fmt.Errorf("browser profile file %s has invalid type or permissions", name)
	}
	if err := unix.Unlinkat(dirFD, name, 0); err != nil {
		return fmt.Errorf("remove browser profile file %s: %w", name, err)
	}
	if err := unix.Fsync(dirFD); err != nil {
		return fmt.Errorf("sync browser profile directory after removing %s: %w", name, err)
	}
	return nil
}

func readChromeProfileLaunchAt(dirFD int, profileDir string) (chromeProfileLaunch, error) {
	data, err := readPrivateBrowserProfileFileAt(dirFD, profileDir, browserProfileLaunchName)
	if err != nil {
		return chromeProfileLaunch{}, err
	}
	var launch chromeProfileLaunch
	if err := json.Unmarshal(data, &launch); err != nil {
		return chromeProfileLaunch{}, fmt.Errorf("decode Chrome launch journal: %w", err)
	}
	if launch.Schema != browserProfileLaunchSchema || len(launch.Generation) != 64 ||
		!validBrowserProfileScopeSHA256(launch.ScopeSHA256) || len(launch.LaunchID) != 64 ||
		launch.Hostname == "" {
		return chromeProfileLaunch{}, errors.New("Chrome launch journal is invalid")
	}
	if _, err := hex.DecodeString(launch.Generation); err != nil {
		return chromeProfileLaunch{}, errors.New("Chrome launch journal generation is invalid")
	}
	if _, err := hex.DecodeString(launch.LaunchID); err != nil {
		return chromeProfileLaunch{}, errors.New("Chrome launch journal launch ID is invalid")
	}
	if _, err := strconv.ParseUint(launch.LockDev, 10, 64); err != nil {
		return chromeProfileLaunch{}, errors.New("Chrome launch journal lock device is invalid")
	}
	if _, err := strconv.ParseUint(launch.LockIno, 10, 64); err != nil {
		return chromeProfileLaunch{}, errors.New("Chrome launch journal lock inode is invalid")
	}
	return launch, nil
}

func validateChromeProfileLaunch(launch chromeProfileLaunch, generation, scopeSHA256 string, lock *browserProfileLock) error {
	if lock == nil || launch.Generation != generation || launch.ScopeSHA256 != scopeSHA256 ||
		launch.LockDev != strconv.FormatUint(lock.dev, 10) || launch.LockIno != strconv.FormatUint(lock.ino, 10) {
		return errors.New("Chrome launch journal does not match the acquired profile lock")
	}
	return nil
}

func readPrivateBrowserProfileFileAt(dirFD int, profileDir, name string) ([]byte, error) {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("open browser profile file %q: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(profileDir, name))
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open browser profile file %q: invalid file descriptor", name)
	}
	defer file.Close()
	if err := validatePrivateOwnedBrowserProfileFD(fd, false); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBrowserProfileFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read browser profile file %q: %w", name, err)
	}
	if len(data) > maxBrowserProfileFileBytes {
		return nil, fmt.Errorf("browser profile file %q is too large", name)
	}
	return data, nil
}

func cleanupStaleChromeProfileLocks(dirFD int, profileDir, generation string) error {
	return cleanupStaleChromeProfileLocksWithHook(dirFD, profileDir, generation, nil)
}

func cleanupStaleChromeProfileLocksWithHook(
	dirFD int,
	profileDir, generation string,
	afterUnlink func(string) error,
) error {
	entries := make(map[string]unix.Stat_t, len(chromeSingletonNames))
	for _, name := range chromeSingletonNames {
		var stat unix.Stat_t
		err := unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect Chrome profile lock %s: %w", name, err)
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			return fmt.Errorf("Chrome profile lock has invalid type: %s", name)
		}
		entries[name] = stat
	}
	if len(entries) == 0 {
		return nil
	}
	lockStat, ok := entries["SingletonLock"]
	if !ok {
		return errors.New("Chrome profile lock ownership is unknown: missing SingletonLock")
	}
	if lockStat.Mode&unix.S_IFMT != unix.S_IFLNK {
		return errors.New("Chrome profile lock ownership is unknown: SingletonLock")
	}
	target, err := readlinkBrowserProfileAt(dirFD, "SingletonLock")
	if err != nil {
		return fmt.Errorf("Chrome profile lock ownership is unknown: %w", err)
	}
	metadata, err := readBrowserProfileMetadataAt(dirFD, profileDir)
	if err != nil || metadata.Generation != generation {
		return errors.Join(errors.New("Chrome profile lock ownership is unknown: profile identity"), err)
	}
	var lockPathStat unix.Stat_t
	if err := unix.Fstatat(dirFD, ".product-capture.lock", &lockPathStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("Chrome profile lock ownership is unknown: inspect profile lock: %w", err)
	}
	lock := &browserProfileLock{dev: uint64(lockPathStat.Dev), ino: lockPathStat.Ino}
	launch, launchErr := readChromeProfileLaunchAt(dirFD, profileDir)
	launchPresent := launchErr == nil
	if launchPresent {
		if err := validateChromeProfileLaunch(launch, metadata.Generation, metadata.ScopeSHA256, lock); err != nil {
			return errors.Join(errors.New("Chrome profile lock ownership is unknown: launch journal"), err)
		}
	} else if !errors.Is(launchErr, os.ErrNotExist) {
		return errors.Join(errors.New("Chrome profile lock ownership is unknown: launch journal"), launchErr)
	}
	owner, err := readChromeProfileOwnerAt(dirFD, profileDir)
	ownerPresent := err == nil
	authorityHostname := ""
	if ownerPresent {
		switch owner.Schema {
		case browserProfileOwnerSchema:
			hostname, hostnameErr := os.Hostname()
			if hostnameErr != nil {
				return fmt.Errorf("read hostname for Chrome profile lock: %w", hostnameErr)
			}
			if owner.Generation != generation || owner.Hostname != hostname || owner.ProcessGroup <= 0 || owner.ProcessStart == "" {
				return errors.New("Chrome profile lock ownership is unknown: legacy owner record")
			}
			authorityHostname = hostname
		case browserProfileOwnerSchema2:
			if !launchPresent || owner.Generation != generation || owner.ScopeSHA256 != metadata.ScopeSHA256 ||
				owner.LaunchID != launch.LaunchID || owner.Hostname != launch.Hostname || owner.ProcessGroup <= 0 || owner.ProcessStart == "" {
				return errors.New("Chrome profile lock ownership is unknown: owner record")
			}
			authorityHostname = launch.Hostname
		default:
			return errors.New("Chrome profile lock ownership is unknown: owner schema")
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) || !launchPresent {
			return errors.Join(errors.New("Chrome profile lock ownership is unknown: owner record"), err)
		}
		authorityHostname = launch.Hostname
	}
	pid, ok := chromeSingletonPID(target, authorityHostname)
	if !ok || (ownerPresent && owner.PID != pid) {
		return errors.New("Chrome profile lock ownership is unknown: SingletonLock")
	}
	identity, exists, err := readLinuxProcessIdentity(pid)
	if err != nil {
		return fmt.Errorf("Chrome profile lock ownership is unknown: %w", err)
	}
	if exists && identity.state != 'Z' {
		if ownerPresent && (identity.startTime != owner.ProcessStart || identity.processGroupID != owner.ProcessGroup) {
			return errors.New("Chrome profile lock ownership is unknown: live process identity mismatch")
		}
		chrome, err := linuxManagedProcessIsChrome(pid, profileDir, identity)
		if err != nil {
			return fmt.Errorf("Chrome profile lock ownership is unknown: %w", err)
		}
		if !chrome {
			return errors.New("Chrome profile lock ownership is unknown: live PID executable mismatch")
		}
		return errors.New("Chrome profile is already active: SingletonLock")
	}

	for _, name := range chromeSingletonCleanupOrder {
		expected, ok := entries[name]
		if !ok {
			continue
		}
		var current unix.Stat_t
		if err := unix.Fstatat(dirFD, name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return fmt.Errorf("reinspect stale Chrome profile lock %s: %w", name, err)
		}
		if current.Dev != expected.Dev || current.Ino != expected.Ino || current.Mode&unix.S_IFMT != expected.Mode&unix.S_IFMT {
			return fmt.Errorf("Chrome profile lock ownership changed before cleanup: %s", name)
		}
		if err := unix.Unlinkat(dirFD, name, 0); err != nil {
			return fmt.Errorf("remove stale Chrome profile lock %s: %w", name, err)
		}
		if err := unix.Fsync(dirFD); err != nil {
			return fmt.Errorf("sync stale Chrome profile lock cleanup %s: %w", name, err)
		}
		if afterUnlink != nil {
			if err := afterUnlink(name); err != nil {
				return fmt.Errorf("stale Chrome profile lock cleanup interrupted after %s: %w", name, err)
			}
		}
	}
	if ownerPresent {
		if err := unlinkPrivateBrowserProfileFileAt(dirFD, browserProfileOwnerName); err != nil {
			return fmt.Errorf("remove stale Chrome profile owner: %w", err)
		}
	}
	if launchPresent {
		if err := unlinkPrivateBrowserProfileFileAt(dirFD, browserProfileLaunchName); err != nil {
			return fmt.Errorf("remove stale Chrome launch journal: %w", err)
		}
	}
	if err := unlinkPrivateBrowserProfileFileAt(dirFD, browserProfileConformanceBoundaryName); err != nil {
		return fmt.Errorf("remove stale Chrome conformance boundary: %w", err)
	}
	return nil
}

func readChromeProfileOwnerAt(dirFD int, profileDir string) (chromeProfileOwner, error) {
	data, err := readPrivateBrowserProfileFileAt(dirFD, profileDir, browserProfileOwnerName)
	if err != nil {
		return chromeProfileOwner{}, err
	}
	var owner chromeProfileOwner
	if err := json.Unmarshal(data, &owner); err != nil {
		return chromeProfileOwner{}, fmt.Errorf("decode Chrome profile owner: %w", err)
	}
	if owner.PID <= 0 {
		return chromeProfileOwner{}, errors.New("Chrome profile owner PID is invalid")
	}
	if _, err := strconv.ParseUint(owner.ProcessStart, 10, 64); err != nil {
		return chromeProfileOwner{}, errors.New("Chrome profile owner start time is invalid")
	}
	return owner, nil
}

func readlinkBrowserProfileAt(dirFD int, name string) (string, error) {
	buffer := make([]byte, maxBrowserProfileFileBytes)
	n, err := unix.Readlinkat(dirFD, name, buffer)
	if err != nil {
		return "", err
	}
	if n == len(buffer) {
		return "", errors.New("Chrome profile lock target is too long")
	}
	return string(buffer[:n]), nil
}

func chromeSingletonPID(target, hostname string) (int, bool) {
	prefix := hostname + "-"
	if !strings.HasPrefix(target, prefix) {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimPrefix(target, prefix))
	return pid, err == nil && pid > 0
}

func linuxProcessExecutableIsChrome(pid int) (bool, error) {
	identity, exists, err := readLinuxProcessIdentity(pid)
	if err != nil || !exists || identity.state == 'Z' {
		return false, err
	}
	return linuxManagedProcessIsChrome(pid, "", identity)
}

func linuxManagedProcessIsChrome(pid int, profileDir string, expected linuxProcessIdentity) (bool, error) {
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return false, fmt.Errorf("read executable for PID %d: %w", pid, err)
	}
	comm, err := readBoundedLinuxProcessFile(pid, "comm", 64)
	if err != nil {
		return false, err
	}
	commandLine, err := readBoundedLinuxProcessFile(pid, "cmdline", 16384)
	if err != nil {
		return false, err
	}
	if string(comm) != "chrome\n" || len(commandLine) == 0 || commandLine[len(commandLine)-1] != 0 {
		return false, nil
	}
	parts := bytes.Split(commandLine[:len(commandLine)-1], []byte{0})
	args := make([]string, len(parts))
	for index := range parts {
		args[index] = string(parts[index])
	}
	var chromeArgs []string
	if executable == "/run/rosetta/rosetta" {
		if len(args) < 4 || args[0] != "/run/rosetta/rosetta" || args[1] != "/opt/google/chrome/chrome" || args[2] != "/usr/bin/google-chrome" {
			return false, nil
		}
		chromeArgs = args[3:]
	} else {
		if len(args) < 2 || !acceptedNativeChromeProcessName(filepath.Base(executable)) || !acceptedNativeChromeProcessName(filepath.Base(args[0])) {
			return false, nil
		}
		chromeArgs = args[1:]
	}
	if !validChromeProcessArguments(chromeArgs, profileDir) {
		return false, nil
	}
	after, exists, err := readLinuxProcessIdentity(pid)
	if err != nil || !exists || after.state == 'Z' {
		return false, err
	}
	return after.processGroupID == expected.processGroupID && after.startTime == expected.startTime, nil
}

func readBoundedLinuxProcessFile(pid int, name string, maximumBytes int64) ([]byte, error) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/%s", pid, name))
	if err != nil {
		return nil, fmt.Errorf("read process %s for PID %d: %w", name, pid, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read process %s for PID %d: %w", name, pid, err)
	}
	if int64(len(data)) > maximumBytes {
		return nil, fmt.Errorf("process %s for PID %d exceeds identity bound", name, pid)
	}
	return data, nil
}

func acceptedNativeChromeProcessName(name string) bool {
	switch strings.ToLower(name) {
	case "chrome", "google-chrome", "google-chrome-stable":
		return true
	default:
		return false
	}
}

func validChromeProcessArguments(args []string, profileDir string) bool {
	profileCount := 0
	debugPortCount := 0
	validProfile := false
	validDebugPort := false
	hasLoopbackAddress := false
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--user-data-dir="):
			profileCount++
			value := strings.TrimPrefix(arg, "--user-data-dir=")
			validProfile = value != "" && filepath.IsAbs(value) && (profileDir == "" || value == profileDir)
		case strings.HasPrefix(arg, "--remote-debugging-port="):
			debugPortCount++
			port, err := strconv.Atoi(strings.TrimPrefix(arg, "--remote-debugging-port="))
			validDebugPort = err == nil && port > 0 && port <= 65535
		case arg == "--remote-debugging-address=127.0.0.1":
			hasLoopbackAddress = true
		}
	}
	return profileCount == 1 && validProfile && debugPortCount == 1 && validDebugPort && hasLoopbackAddress && len(args) > 0 && args[len(args)-1] == "about:blank"
}
