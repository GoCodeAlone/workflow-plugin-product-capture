//go:build linux

package provider

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testBrowserProfileScope = "org-1/pool-1/provider-enrollment-product-capture:v1"

func TestManagedBrowserProfileRemovesOnlyVerifiablyStaleChromeLocks(t *testing.T) {
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")
	profile, err := acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("create managed browser profile: %v", err)
	}
	if err := profile.Release(); err != nil {
		t.Fatalf("release managed browser profile: %v", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	const stalePID = 99999999
	if err := os.Symlink(fmt.Sprintf("%s-%d", hostname, stalePID), filepath.Join(profileDir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SingletonSocket", "SingletonCookie"} {
		if err := os.Symlink("stale", filepath.Join(profileDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeTestChromeOwnerMetadata(t, profileDir, profile.Generation, hostname, stalePID, "123")

	profile, err = acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("reacquire managed profile with stale Chrome lock: %v", err)
	}
	defer func() {
		if err := profile.Release(); err != nil {
			t.Errorf("release managed browser profile: %v", err)
		}
	}()
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); !os.IsNotExist(err) {
			t.Fatalf("stale profile entry %s still exists: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(profileDir, browserProfileOwnerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed stale cleanup retained owner record: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(profileDir, browserProfileLaunchName)); err != nil {
		t.Fatalf("reacquired profile did not publish a fresh launch journal: %v", err)
	}
}

func TestManagedBrowserProfileRecoversOwnerlessLocksAuthorizedByLaunchJournal(t *testing.T) {
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")
	profile, err := acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("create managed browser profile: %v", err)
	}
	const stalePID = 99999999
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fmt.Sprintf("%s-%d", hostname, stalePID), filepath.Join(profileDir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SingletonSocket", "SingletonCookie"} {
		if err := os.Symlink("stale", filepath.Join(profileDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(profileDir, browserProfileConformanceBoundaryName), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := profile.Release(); err != nil {
		t.Fatalf("release crashed launch profile: %v", err)
	}

	profile, err = acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("recover ownerless locks with launch journal: %v", err)
	}
	defer func() { _ = profile.Release() }()
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale profile entry %s remains: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(profileDir, browserProfileConformanceBoundaryName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale conformance boundary marker remains: %v", err)
	}
}

func TestManagedBrowserProfileLockRemainsHeldByConfiguredChild(t *testing.T) {
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")
	profile, err := acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("create managed browser profile: %v", err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestBrowserProfileInheritedLockChild$")
	cmd.Env = append(os.Environ(),
		"PRODUCT_CAPTURE_TEST_INHERITED_LOCK_CHILD=1",
		"PRODUCT_CAPTURE_TEST_READY="+ready,
	)
	if err := profile.ConfigureCommand(cmd); err != nil {
		t.Fatalf("configure child profile lock: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lock child: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for lock child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if err := profile.Release(); err != nil {
		t.Fatalf("release parent profile lock: %v", err)
	}
	if _, err := acquireManagedBrowserProfile(profileDir); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("reacquire while child holds inherited lock error = %v, want active rejection", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill lock child: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed lock child exited successfully")
	}
	profile, err = acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("reacquire after inherited lock child exits: %v", err)
	}
	if err := profile.Release(); err != nil {
		t.Fatalf("release reacquired profile: %v", err)
	}
}

func TestBrowserProfileInheritedLockChild(t *testing.T) {
	if os.Getenv("PRODUCT_CAPTURE_TEST_INHERITED_LOCK_CHILD") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("PRODUCT_CAPTURE_TEST_READY"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
}

func TestManagedBrowserProfileRejectsLivePIDWithMismatchedExecutable(t *testing.T) {
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")
	profile, err := acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("create managed browser profile: %v", err)
	}
	if err := profile.Release(); err != nil {
		t.Fatalf("release managed browser profile: %v", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	lockPath := filepath.Join(profileDir, "SingletonLock")
	if err := os.Symlink(fmt.Sprintf("%s-%d", hostname, pid), lockPath); err != nil {
		t.Fatal(err)
	}
	writeTestChromeOwnerMetadata(t, profileDir, profile.Generation, hostname, pid, linuxProcessStartTimeForTest(t, pid))

	if _, err := acquireManagedBrowserProfile(profileDir); err == nil || !strings.Contains(err.Error(), "ownership is unknown") {
		t.Fatalf("reacquire profile error = %v, want unknown live identity rejection", err)
	}
	if _, err := os.Lstat(lockPath); err != nil {
		t.Fatalf("unknown live lock was removed: %v", err)
	}
}

func TestLinuxProcessExecutableIsChromeRecognizesTranslatedChrome(t *testing.T) {
	chromePath, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skipf("google-chrome is unavailable: %v", err)
	}
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")
	cmd := exec.Command(chromePath,
		"--headless=new",
		"--remote-debugging-port=9222",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--no-sandbox",
		"--disable-setuid-sandbox",
		"--disable-dev-shm-usage",
		"about:blank",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start google-chrome: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(filepath.Join(profileDir, "SingletonLock")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("google-chrome did not create SingletonLock")
		}
		time.Sleep(25 * time.Millisecond)
	}
	matched, err := linuxProcessExecutableIsChrome(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("classify google-chrome process: %v", err)
	}
	if !matched {
		executable, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", cmd.Process.Pid))
		comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", cmd.Process.Pid))
		commandLine, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid))
		t.Fatalf("google-chrome executable %q comm %q cmdline %q was not recognized", executable, comm, bytes.ReplaceAll(commandLine, []byte{0}, []byte{' '}))
	}
}

func TestManagedBrowserProfileBindsStateToDeploymentScope(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	profile, err := acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("create managed browser profile: %v", err)
	}
	if err := profile.Release(); err != nil {
		t.Fatalf("release managed browser profile: %v", err)
	}

	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", "org-2/pool-2/provider-enrollment-product-capture:v1")
	if _, err := acquireManagedBrowserProfile(profileDir); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("reacquire profile under another scope error = %v, want scope rejection", err)
	}
}

func TestManagedBrowserProfileInitializationFailureDoesNotWedgeNewPath(t *testing.T) {
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	profileDir := filepath.Join(t.TempDir(), "chrome-profile")

	if _, err := acquireManagedBrowserProfileWithIdentityReader(profileDir, strings.NewReader("")); err == nil || !strings.Contains(err.Error(), "generate browser profile identity") {
		t.Fatalf("initialization error = %v, want identity generation failure", err)
	}
	if _, err := os.Lstat(profileDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed initialization left profile path behind: %v", err)
	}

	profile, err := acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("reacquire profile after initialization failure: %v", err)
	}
	if err := profile.Release(); err != nil {
		t.Fatalf("release recovered profile: %v", err)
	}
}

func TestManagedBrowserProfileRejectsWritableNonStickyAncestor(t *testing.T) {
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	grandparent := filepath.Join(t.TempDir(), "writable-grandparent")
	if err := os.Mkdir(grandparent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(grandparent, 0o777); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(grandparent, "trusted-parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}

	profile, err := acquireManagedBrowserProfile(filepath.Join(parent, "chrome-profile"))
	if err == nil {
		_ = profile.Release()
		t.Fatal("managed browser profile accepted a writable non-sticky ancestor")
	}
	if !strings.Contains(err.Error(), "ancestor is writable by another user") {
		t.Fatalf("profile ancestor error = %v", err)
	}
}

func TestManagedBrowserProfileStaleCleanupRecoversAfterEveryUnlink(t *testing.T) {
	for _, failureAfter := range []string{"SingletonSocket", "SingletonCookie", "SingletonLock"} {
		t.Run(failureAfter, func(t *testing.T) {
			t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
			profileDir := filepath.Join(t.TempDir(), "chrome-profile")
			profile, err := acquireManagedBrowserProfile(profileDir)
			if err != nil {
				t.Fatalf("create managed browser profile: %v", err)
			}
			if err := profile.Release(); err != nil {
				t.Fatalf("release managed browser profile: %v", err)
			}

			hostname, err := os.Hostname()
			if err != nil {
				t.Fatal(err)
			}
			const stalePID = 99999999
			if err := os.Symlink(fmt.Sprintf("%s-%d", hostname, stalePID), filepath.Join(profileDir, "SingletonLock")); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"SingletonSocket", "SingletonCookie"} {
				if err := os.Symlink("stale", filepath.Join(profileDir, name)); err != nil {
					t.Fatal(err)
				}
			}
			writeTestChromeOwnerMetadata(t, profileDir, profile.Generation, hostname, stalePID, "123")

			dir, err := openBrowserProfileDirectory(profileDir)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected unlink interruption")
			err = cleanupStaleChromeProfileLocksWithHook(int(dir.Fd()), profileDir, profile.Generation, func(name string) error {
				if name == failureAfter {
					return injected
				}
				return nil
			})
			if closeErr := dir.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if !errors.Is(err, injected) {
				t.Fatalf("cleanup error = %v, want injected interruption after %s", err, failureAfter)
			}
			if failureAfter != "SingletonLock" {
				if _, err := os.Lstat(filepath.Join(profileDir, "SingletonLock")); err != nil {
					t.Fatalf("cleanup interruption after %s removed recovery lock: %v", failureAfter, err)
				}
			}

			profile, err = acquireManagedBrowserProfile(profileDir)
			if err != nil {
				t.Fatalf("reacquire after interruption following %s: %v", failureAfter, err)
			}
			if err := profile.Release(); err != nil {
				t.Fatalf("release recovered managed browser profile: %v", err)
			}
		})
	}
}

func TestBrowserDiagnosticProfileConformanceUsesManagedProfile(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "persistent-profile")
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte(`#!/bin/sh
[ "$PRODUCT_CAPTURE_BROWSER_PROFILE_DIR" = "$PRODUCT_CAPTURE_TEST_PROFILE_DIR" ] || { echo "profile=$PRODUCT_CAPTURE_BROWSER_PROFILE_DIR" >&2; exit 24; }
[ "$PRODUCT_CAPTURE_BROWSER_PROFILE_LOCK_HELD" = "1" ] || { echo "profile lock marker missing" >&2; exit 25; }
[ -n "$PRODUCT_CAPTURE_BROWSER_PROFILE_GENERATION" ] || { echo "profile generation missing" >&2; exit 26; }
/bin/cat "$PRODUCT_CAPTURE_TEST_DIAGNOSTIC_JSON_PATH"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PRODUCT_CAPTURE_BROWSER_HEADLESS", "1")
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_DIR", profileDir)
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	t.Setenv("PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS", "https://93.184.216.34")
	t.Setenv("PRODUCT_CAPTURE_TEST_PROFILE_DIR", profileDir)
	t.Setenv("PRODUCT_CAPTURE_TEST_DIAGNOSTIC_JSON_PATH", writeValidBrowserDiagnosticFixture(t, "https://93.184.216.34/profile"))

	if err := runBrowserDiagnosticWithOptions(
		"https://93.184.216.34/profile",
		io.Discard,
		browserDiagnosticOptions{PersistentProfileConformance: true},
	); err != nil {
		t.Fatalf("run persistent-profile browser diagnostic: %v", err)
	}
}

func TestBrowserDiagnosticProfileConformanceCleansTempScriptAfterProfileAcquireFailure(t *testing.T) {
	tempRoot := t.TempDir()
	untrustedParent := filepath.Join(tempRoot, "untrusted")
	if err := os.Mkdir(untrustedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(untrustedParent, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tempRoot)
	t.Setenv("PRODUCT_CAPTURE_BROWSER_HEADLESS", "1")
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_DIR", filepath.Join(untrustedParent, "profile"))
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	t.Setenv("PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS", "https://93.184.216.34")

	err := runBrowserDiagnosticWithOptions(
		"https://93.184.216.34/profile",
		io.Discard,
		browserDiagnosticOptions{PersistentProfileConformance: true},
	)
	if err == nil || !strings.Contains(err.Error(), "writable by another user") {
		t.Fatalf("profile acquisition error = %v, want untrusted-parent rejection", err)
	}
	entries, readErr := os.ReadDir(tempRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "product-capture-browser-diagnostic-") {
			t.Fatalf("diagnostic temp script directory leaked after profile acquisition failure: %s", entry.Name())
		}
	}
}

func TestCaptureHTMLWithPlaywrightRecoversManagedNativeChromeProfile(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "chrome-profile")
	moduleDir := filepath.Join(dir, "node_modules", "playwright")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "index.js"), []byte(managedProfileFakePlaywright+fakeConnectOverCDPAdapter), 0o600); err != nil {
		t.Fatal(err)
	}
	installFakeGoogleChrome(t, dir)
	t.Setenv("NODE_PATH", filepath.Join(dir, "node_modules"))
	t.Setenv("PRODUCT_CAPTURE_BROWSER_HEADLESS", "1")
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_DIR", profileDir)
	t.Setenv("PRODUCT_CAPTURE_BROWSER_PROFILE_SCOPE", testBrowserProfileScope)
	t.Setenv("PRODUCT_CAPTURE_TEST_PERSIST_SESSION_STATE", "1")
	crashMarker := filepath.Join(dir, "crashed-once")
	t.Setenv("PRODUCT_CAPTURE_TEST_CRASH_ON_VERSION_ONCE", crashMarker)
	t.Setenv("PRODUCT_CAPTURE_TEST_CONNECT_DELAY_MS", "50")

	workload := Workload{
		URL:          "https://www.amazon.com/dp/B09B8V1LZ3",
		AllowedHosts: []string{"www.amazon.com"},
		WarmupURL:    "https://www.amazon.com/",
	}
	_, firstErr := captureHTMLWithPlaywright(workload)
	if firstErr == nil {
		t.Fatal("first native Chrome capture succeeded despite injected browser crash")
	}
	for _, name := range []string{"SingletonLock", browserProfileLaunchName} {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); err != nil {
			t.Fatalf("crashed Chrome did not leave recoverable %s: %v; first capture error: %v", name, err, firstErr)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		html, err := captureHTMLWithPlaywright(workload)
		if err != nil {
			t.Fatalf("managed native Chrome capture %d after crash: %v", attempt+1, err)
		}
		if !strings.Contains(html, `id="productTitle"`) {
			t.Fatalf("managed native Chrome capture %d returned unexpected HTML: %s", attempt+1, html)
		}
	}
	if data, err := os.ReadFile(filepath.Join(profileDir, ".test-anonymous-session-state")); err != nil || string(data) != "anonymous-session\n" {
		t.Fatalf("persistent anonymous session state = %q, %v", data, err)
	}
	data, err := os.ReadFile(filepath.Join(profileDir, ".test-launch-count"))
	if err != nil {
		t.Fatal(err)
	}
	if count, err := strconv.Atoi(strings.TrimSpace(string(data))); err != nil || count != 3 {
		t.Fatalf("native Chrome launch count = %q, want 3: %v", data, err)
	}
	profile, err := acquireManagedBrowserProfile(profileDir)
	if err != nil {
		t.Fatalf("reacquire after recovered captures: %v", err)
	}
	if err := profile.Release(); err != nil {
		t.Fatalf("release profile after final stale-lock cleanup: %v", err)
	}
	for _, name := range []string{"SingletonLock", browserProfileOwnerName} {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("final managed-profile cleanup left %s: %v", name, err)
		}
	}
}

const managedProfileFakePlaywright = `
class TimeoutError extends Error {
  constructor(message) {
    super(message);
    this.name = 'TimeoutError';
  }
}
function withDocument(fn, arg) {
  const previousDocument = global.document;
  const previousLocation = global.location;
  const previousWindow = global.window;
  global.location = { href: 'https://www.amazon.com/dp/B09B8V1LZ3' };
  global.window = { location: { assign: (target) => { global.location.href = target; } } };
  global.document = {
    body: { textContent: 'product page' },
    querySelectorAll: (selector) => selector === '#productTitle' ? [{ value: '', textContent: ' Echo Dot ' }] : [],
    querySelector: (selector) => {
      if (selector === '#landingImage') return { getAttribute: (name) => name === 'src' ? 'https://m.media-amazon.com/images/I/echo.jpg' : '' };
      if (selector === 'link[rel="canonical"]') return { getAttribute: (name) => name === 'href' ? 'https://www.amazon.com/dp/B09B8V1LZ3' : '' };
      return null;
    },
  };
  try {
    return fn(arg);
  } finally {
    global.document = previousDocument;
    global.location = previousLocation;
    global.window = previousWindow;
  }
}
exports.chromium = {
  launch: async () => ({
    newPage: async () => ({
      goto: async () => {},
      url: () => 'https://www.amazon.com/dp/B09B8V1LZ3',
      locator: (selector) => {
        if (selector === 'form[action*="/errors/validateCaptcha"]') return { count: async () => 0 };
        return { count: async () => 0, first: () => ({ click: async () => {} }) };
      },
      waitForNavigation: async () => {},
      waitForLoadState: async () => {},
      waitForTimeout: async () => {},
      waitForFunction: async (fn, arg) => {
        if (!withDocument(fn, arg)) throw new TimeoutError('timeout');
      },
      evaluate: async (fn, arg) => withDocument(fn, arg),
      content: async () => '<html><head><link rel="canonical" href="https://www.amazon.com/dp/B09B8V1LZ3"></head><body><span id="productTitle">Echo Dot</span><img id="landingImage" src="https://m.media-amazon.com/images/I/echo.jpg"></body></html>',
    }),
    close: async () => {},
  }),
};
exports.errors = { TimeoutError };
`
