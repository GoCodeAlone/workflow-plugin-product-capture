package conformance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCompareBrowserObservationsClassifiesSchemaV1Fields(t *testing.T) {
	direct := matchingObservation("direct")
	attached := matchingObservation("attached")
	attached.Browser.Window.OuterWidth += 2
	attached.Browser.Window.InnerHeight -= 2
	attached.Browser.Navigator.HardwareConcurrency = 4
	attached.Browser.Navigator.DeviceMemory = 2
	attached.Browser.Document.CookiePresent = true
	attached.Browser.Document.CookieLength = 12
	attached.Browser.WebGL.Renderer = "different informational renderer"
	attached.Request.HeaderOrder = []string{"sec-fetch-site", "user-agent"}
	attached.Timing = map[string]float64{"navigation_ms": 42}

	report := Compare(direct, attached, Versions{Chrome: "Google Chrome 140", Playwright: "1.57.0", Xvfb: "1.20.14"})
	if report.Verdict != VerdictPass || report.ExitCode() != 0 {
		t.Fatalf("report = %+v, want pass", report)
	}
	for _, field := range []string{
		"browser.navigator.webdriver",
		"browser.navigator.user_agent",
		"browser.navigator.user_agent_data.brands",
		"browser.navigator.user_agent_data.platform",
		"browser.navigator.language",
		"browser.navigator.languages",
		"browser.navigator.platform",
		"browser.automation.playwright_binding_present",
		"browser.automation.playwright_init_scripts_present",
		"request.user_agent",
		"request.client_hints.brands",
		"request.client_hints.mobile",
		"request.client_hints.platform",
		"request.sec_fetch.dest",
		"request.sec_fetch.mode",
		"request.sec_fetch.site",
		"request.sec_fetch.user",
		"first_navigation_origin",
		"browser.window.outer_width",
		"browser.window.outer_height",
		"browser.window.inner_width",
		"browser.window.inner_height",
	} {
		if !hasComparison(report.StableComparisons, field) {
			t.Errorf("stable comparisons missing %q: %+v", field, report.StableComparisons)
		}
	}
	for _, field := range []string{
		"request.header_order",
		"timing",
		"browser.webgl",
		"browser.navigator.hardware_concurrency",
		"browser.navigator.device_memory",
		"browser.document.cookie_present",
		"browser.document.cookie_length",
	} {
		if _, ok := report.Informational[field]; !ok {
			t.Errorf("informational values missing %q: %+v", field, report.Informational)
		}
	}
}

func TestCompareBrowserObservationsReturnsNonzeroForStableMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Observation)
		field  string
	}{
		{name: "webdriver", field: "browser.navigator.webdriver", mutate: func(o *Observation) { o.Browser.Navigator.Webdriver = true }},
		{name: "ua", field: "browser.navigator.user_agent", mutate: func(o *Observation) { o.Browser.Navigator.UserAgent += " attached" }},
		{name: "brands", field: "browser.navigator.user_agent_data.brands", mutate: func(o *Observation) { o.Browser.Navigator.UserAgentData.Brands[0].Version = "141" }},
		{name: "platform", field: "browser.navigator.platform", mutate: func(o *Observation) { o.Browser.Navigator.Platform = "Other" }},
		{name: "playwright global", field: "browser.automation.playwright_binding_present", mutate: func(o *Observation) { o.Browser.Automation.PlaywrightBindingPresent = true }},
		{name: "request hints", field: "request.client_hints.platform", mutate: func(o *Observation) { o.Request.ClientHints.Platform = `"Other"` }},
		{name: "fetch", field: "request.sec_fetch.mode", mutate: func(o *Observation) { o.Request.SecFetch.Mode = "cors" }},
		{name: "origin", field: "first_navigation_origin", mutate: func(o *Observation) { o.FirstNavigationOrigin = "https://other.example" }},
		{name: "window tolerance", field: "browser.window.outer_width", mutate: func(o *Observation) { o.Browser.Window.OuterWidth += 3 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direct := matchingObservation("direct")
			attached := matchingObservation("attached")
			tt.mutate(&attached)

			report := Compare(direct, attached, Versions{})
			if report.Verdict != VerdictFail || report.ExitCode() == 0 {
				t.Fatalf("report = %+v, want nonzero failure", report)
			}
			comparison, ok := findComparison(report.StableComparisons, tt.field)
			if !ok || comparison.Match {
				t.Fatalf("comparison %q = %+v, found %v", tt.field, comparison, ok)
			}
		})
	}
}

func TestCompareBrowserObservationsRejectsWrongSchemaOrRun(t *testing.T) {
	for _, mutate := range []func(*Observation){
		func(o *Observation) { o.Schema = "v2" },
		func(o *Observation) { o.RunID = "other-run" },
	} {
		direct := matchingObservation("direct")
		attached := matchingObservation("attached")
		mutate(&attached)
		report := Compare(direct, attached, Versions{})
		if report.Verdict != VerdictFail || report.ExitCode() == 0 || len(report.Errors) == 0 {
			t.Fatalf("report = %+v, want schema/run failure", report)
		}
	}
}

func TestRunnerRejectsUncorrelatedTunnelEndpointAndCleansUp(t *testing.T) {
	wrongEndpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema":"v1","run_id":"wrong"}`))
	}))
	defer wrongEndpoint.Close()

	tunnel := &fakeTunnel{origin: wrongEndpoint.URL}
	runner := Runner{Dependencies: Dependencies{
		Tunnel:     tunnel,
		HTTPClient: wrongEndpoint.Client(),
		LaunchDirect: func(context.Context, string, string) error {
			return errors.New("direct launch must not run before health correlation")
		},
		LaunchAttached: func(context.Context, string, string) error {
			return errors.New("attached launch must not run before health correlation")
		},
	}}
	err := runner.Run(context.Background(), Options{
		Image:  "candidate:test",
		Output: filepath.Join(t.TempDir(), "conformance.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "run-correlated") {
		t.Fatalf("Run error = %v, want run-correlated endpoint rejection", err)
	}
	if tunnel.stopCalls != 1 {
		t.Fatalf("tunnel stop calls = %d, want 1", tunnel.stopCalls)
	}
}

func TestFetchRunHealthRetriesTransientDNSBeforeAcceptingCorrelation(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls < 3 {
			return nil, &net.DNSError{Err: "no such host", Name: request.URL.Hostname(), IsNotFound: true}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"run-123"}`)),
			Request:    request,
		}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fetchRunHealth(ctx, client, "https://diagnostic.example/runs/run-123/healthz", "run-123", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("health calls = %d, want 3", calls)
	}
}

func TestTunnelHealthTimeoutAllowsSlowQuickTunnelDNSPropagation(t *testing.T) {
	if got := tunnelHealthTimeout(0); got != 2*time.Minute {
		t.Fatalf("default tunnel health timeout = %s, want 2m", got)
	}
	if got := tunnelHealthTimeout(25 * time.Millisecond); got != 25*time.Millisecond {
		t.Fatalf("configured tunnel health timeout = %s, want 25ms", got)
	}
}

func TestDefaultConformanceTimeoutCoversTunnelRetriesAndRuntime(t *testing.T) {
	if defaultConformanceTimeout != 12*time.Minute {
		t.Fatalf("default conformance timeout = %s, want 12m", defaultConformanceTimeout)
	}
	if defaultConformanceTimeout < 3*tunnelHealthTimeout(0)+4*time.Minute {
		t.Fatalf("default conformance timeout does not cover tunnel retries and runtime checks")
	}
}

func TestRunnerRetriesQuickTunnelActivationAndCleansEachAttempt(t *testing.T) {
	tunnel := &sequenceTunnel{origins: []string{"https://first.invalid", "https://second.test"}}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "first.invalid" {
			return nil, &net.DNSError{Err: "no such host", Name: request.URL.Hostname(), IsNotFound: true}
		}
		runID := strings.Split(strings.Trim(request.URL.Path, "/"), "/")[1]
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"` + runID + `"}`)),
			Request:    request,
		}, nil
	})}
	runner := Runner{Dependencies: Dependencies{
		Tunnel:              tunnel,
		HTTPClient:          client,
		TunnelHealthTimeout: 5 * time.Millisecond,
	}}
	err := runner.Run(context.Background(), Options{
		Image:  "candidate:test",
		Output: filepath.Join(t.TempDir(), "conformance.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "browser launch dependencies") {
		t.Fatalf("Run error = %v, want post-health dependency failure", err)
	}
	if tunnel.startCalls != 2 || tunnel.stopCalls != 2 {
		t.Fatalf("tunnel calls = start:%d stop:%d, want 2/2", tunnel.startCalls, tunnel.stopCalls)
	}
}

func TestFetchRunHealthRedactsEndpointOnFailure(t *testing.T) {
	const secretURL = "https://secret-tunnel.trycloudflare.com/runs/secret-run/healthz"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, &url.Error{
			Op:  request.Method,
			URL: secretURL,
			Err: &net.DNSError{Err: "no such host", Name: request.URL.Hostname(), IsNotFound: true},
		}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := fetchRunHealth(ctx, client, secretURL, "secret-run", time.Millisecond)
	if err == nil {
		t.Fatal("health check unexpectedly succeeded")
	}
	for _, forbidden := range []string{"secret-tunnel", "secret-run", "trycloudflare.com"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("health error leaked %q: %v", forbidden, err)
		}
	}
}

func TestCollectorRejectsWrongRunAndOversizedObservation(t *testing.T) {
	collector := NewCollector("run-123")
	server := httptest.NewServer(collector.Handler())
	defer server.Close()

	for _, request := range []struct {
		name string
		path string
		body string
	}{
		{name: "wrong run", path: "/runs/wrong/direct", body: `{}`},
		{name: "wrong kind", path: "/runs/run-123/unknown", body: `{}`},
		{name: "oversized", path: "/runs/run-123/direct", body: strings.Repeat("x", MaxObservationBytes+1)},
	} {
		t.Run(request.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, server.URL+request.path, strings.NewReader(request.body))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode < 400 {
				t.Fatalf("status = %d, want rejection", resp.StatusCode)
			}
		})
	}
}

func TestCollectorWaitReportsNavigationStateWithoutEndpointDetails(t *testing.T) {
	collector := NewCollector("secret-run")
	server := httptest.NewServer(collector.Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/runs/secret-run/direct")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = collector.Wait(ctx, "direct")
	if err == nil || !strings.Contains(err.Error(), "navigation_observed=true") {
		t.Fatalf("Wait error = %v", err)
	}
	for _, forbidden := range []string{"secret-run", server.URL} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("Wait error leaked %q: %v", forbidden, err)
		}
	}
}

func TestCollectorServesBoundedPageAndBuildsRunCorrelatedObservation(t *testing.T) {
	collector := NewCollector("run-123")
	server := httptest.NewServer(collector.Handler())
	defer server.Close()

	navigation, err := http.NewRequest(http.MethodGet, server.URL+"/runs/run-123/direct", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	navigation.Host = "diagnostic.example"
	navigation.Header.Set("X-Forwarded-Proto", "https")
	navigation.Header.Set("User-Agent", "Mozilla/5.0 Chrome/140")
	navigation.Header.Set("Sec-CH-UA", `"Chromium";v="140"`)
	navigation.Header.Set("Sec-CH-UA-Mobile", "?0")
	navigation.Header.Set("Sec-CH-UA-Platform", `"Linux"`)
	navigation.Header.Set("Sec-Fetch-Dest", "document")
	navigation.Header.Set("Sec-Fetch-Mode", "navigate")
	navigation.Header.Set("Sec-Fetch-Site", "none")
	navigation.Header.Set("Sec-Fetch-User", "?1")
	response, err := http.DefaultClient.Do(navigation)
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(page) > MaxPageBytes {
		t.Fatalf("page status/size = %d/%d", response.StatusCode, len(page))
	}
	for _, want := range []string{"navigator.webdriver", "navigator.userAgentData", "__playwright__binding__", "__pwInitScripts", "fetch(location.href"} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("self-reporting page missing %q", want)
		}
	}
	if response.Header.Get("Content-Security-Policy") == "" || response.Header.Get("Accept-CH") == "" {
		t.Fatalf("bounded page headers: %+v", response.Header)
	}

	payload := providerPayload(matchingObservation("direct").Browser)
	post, err := http.NewRequest(http.MethodPost, server.URL+"/runs/run-123/direct", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("post status = %d", response.StatusCode)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observation, err := collector.Wait(waitCtx, "direct")
	if err != nil {
		t.Fatal(err)
	}
	if observation.Schema != SchemaV1 || observation.RunID != "run-123" || observation.Kind != "direct" {
		t.Fatalf("observation correlation: %+v", observation)
	}
	if observation.Request.UserAgent != "Mozilla/5.0 Chrome/140" || observation.Request.ClientHints.Platform != `"Linux"` {
		t.Fatalf("request signals: %+v", observation.Request)
	}
	if observation.FirstNavigationOrigin != "https://diagnostic.example" {
		t.Fatalf("first navigation origin = %q", observation.FirstNavigationOrigin)
	}
	if observation.Browser.Navigator.UserAgent != matchingObservation("direct").Browser.Navigator.UserAgent {
		t.Fatalf("browser signals: %+v", observation.Browser)
	}
}

func TestRunnerWritesRedactedReportAndReturnsConformanceFailure(t *testing.T) {
	client := &http.Client{}
	tunnel := &proxyTunnel{client: client}
	launch := func(kind string, mismatch bool) func(context.Context, string, string) error {
		return func(ctx context.Context, image, target string) error {
			if image != "candidate:test" {
				return errors.New("unexpected image")
			}
			navigation, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
			if err != nil {
				return err
			}
			navigation.Header.Set("User-Agent", "Mozilla/5.0 Chrome/140")
			navigation.Header.Set("Sec-CH-UA", `"Chromium";v="140", "Google Chrome";v="140"`)
			navigation.Header.Set("Sec-CH-UA-Mobile", "?0")
			navigation.Header.Set("Sec-CH-UA-Platform", `"Linux"`)
			navigation.Header.Set("Sec-Fetch-Dest", "document")
			navigation.Header.Set("Sec-Fetch-Mode", "navigate")
			navigation.Header.Set("Sec-Fetch-Site", "none")
			navigation.Header.Set("Sec-Fetch-User", "?1")
			response, err := client.Do(navigation)
			if err != nil {
				return err
			}
			if err := response.Body.Close(); err != nil {
				return err
			}

			observation := matchingObservation(kind)
			if mismatch {
				observation.Browser.Navigator.UserAgent += " mismatched"
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(providerPayload(observation.Browser)))
			if err != nil {
				return err
			}
			request.Header.Set("Content-Type", "application/json")
			response, err = client.Do(request)
			if err != nil {
				return err
			}
			if err := response.Body.Close(); err != nil {
				return err
			}
			if response.StatusCode != http.StatusAccepted {
				return errors.New("observation was not accepted")
			}
			return nil
		}
	}

	output := filepath.Join(t.TempDir(), "conformance.json")
	lifecycleCalls := 0
	runner := Runner{Dependencies: Dependencies{
		Tunnel:         tunnel,
		HTTPClient:     client,
		LaunchDirect:   launch("direct", false),
		LaunchAttached: launch("attached", true),
		ValidateLifecycle: func(context.Context, string, string) error {
			lifecycleCalls++
			return nil
		},
		InspectVersions: func(context.Context, string) (Versions, error) {
			return Versions{ImageID: "sha256:image", Chrome: "Google Chrome 140", Playwright: "1.57.0", Xvfb: "1.20.14"}, nil
		},
	}}
	err := runner.Run(context.Background(), Options{Image: "candidate:test", Output: output})
	if err == nil || !strings.Contains(err.Error(), "conformance failed") {
		t.Fatalf("Run error = %v, want conformance failure", err)
	}
	if tunnel.stopCalls != 1 {
		t.Fatalf("tunnel stop calls = %d, want 1", tunnel.stopCalls)
	}
	if lifecycleCalls != 1 {
		t.Fatalf("lifecycle calls = %d, want 1", lifecycleCalls)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Verdict != VerdictFail || report.ExitCode() == 0 || report.Versions.ImageID != "sha256:image" {
		t.Fatalf("report = %+v", report)
	}
	for _, forbidden := range []string{"run-", "diagnostic.example", "trycloudflare.com", "remote_addr", "cookie_value"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("report contains unredacted %q: %s", forbidden, data)
		}
	}
}

func TestLaunchAndCollectWaitsForLauncherCleanupAfterCancellation(t *testing.T) {
	collector := NewCollector("run-cancel")
	cleanupComplete := make(chan struct{})
	launch := func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		time.Sleep(25 * time.Millisecond)
		close(cleanupComplete)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := launchAndCollect(ctx, collector, "direct", "candidate:test", "https://diagnostic.example/direct", launch)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("launchAndCollect error = %v, want deadline", err)
	}
	if !strings.Contains(err.Error(), "navigation_observed=false") {
		t.Fatalf("launchAndCollect error missing collector state: %v", err)
	}
	select {
	case <-cleanupComplete:
	default:
		t.Fatal("launchAndCollect returned before launcher cleanup completed")
	}
}

func TestMainRequiresCandidateImageAndOutput(t *testing.T) {
	for _, args := range [][]string{{}, {"--image", "candidate:test"}, {"--output", "report.json"}} {
		var stdout, stderr bytes.Buffer
		if code := Main(args, &stdout, &stderr, Dependencies{}); code != 2 {
			t.Fatalf("Main(%v) = %d, stderr %q; want 2", args, code, stderr.String())
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "required") {
			t.Fatalf("Main(%v) stdout/stderr = %q/%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestDirectChromeContainerArgsUseHeadedChromeWithoutCDPOrPlaywright(t *testing.T) {
	got := directChromeContainerArgs("candidate:test", "https://diagnostic.example/runs/run/direct", "/tmp/profile")
	for _, want := range []string{
		"--platform", "linux/amd64",
		"--entrypoint", "/bin/sh",
		"candidate:test", "google-chrome",
		"--window-size=1920,1080",
		"--user-data-dir=/tmp/conformance-profile",
		"https://diagnostic.example/runs/run/direct",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("direct args missing %q: %v", want, got)
		}
	}
	joined := strings.Join(got, " ")
	for _, forbidden := range []string{"--headless", "remote-debugging", "playwright", "product-capture-provider", "AutomationControlled"} {
		if strings.Contains(strings.ToLower(joined), strings.ToLower(forbidden)) {
			t.Errorf("direct args contain forbidden %q: %s", forbidden, joined)
		}
	}
}

func TestHeadedContainerArgsUseBoundedXvfbSocketReadiness(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "direct", args: directChromeContainerArgs("candidate:test", "https://diagnostic.example/direct", "/tmp/profile"), want: "google-chrome"},
		{name: "attached", args: attachedProviderContainerArgs("candidate:test", "https://diagnostic.example/attached"), want: "/usr/local/bin/product-capture-provider"},
	} {
		t.Run(test.name, func(t *testing.T) {
			joined := strings.Join(test.args, " ")
			for _, required := range []string{
				"--entrypoint /bin/sh",
				"Xvfb :99",
				"/tmp/.X11-unix/X99",
				"PRODUCT_CAPTURE_XVFB_READY_TIMEOUT",
				test.want,
			} {
				if !strings.Contains(joined, required) {
					t.Errorf("headed %s args missing %q: %s", test.name, required, joined)
				}
			}
			if strings.Contains(joined, "xvfb-run") {
				t.Fatalf("headed %s args retain xvfb-run signal handshake: %s", test.name, joined)
			}
		})
	}
}

func TestHeadedContainerWrapperWaitsForChildBeforeStoppingXvfb(t *testing.T) {
	waitIndex := strings.Index(headedContainerScript, `wait "$child_pid"`)
	xvfbStopIndex := strings.Index(headedContainerScript, `kill -TERM "$xvfb_pid"`)
	if waitIndex < 0 || xvfbStopIndex < 0 || waitIndex > xvfbStopIndex {
		t.Fatalf("headed cleanup must reap child before stopping Xvfb:\n%s", headedContainerScript)
	}
	for _, required := range []string{"PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT", `kill -KILL "$child_pid"`} {
		if !strings.Contains(headedContainerScript, required) {
			t.Fatalf("headed cleanup missing %q", required)
		}
	}
}

func TestAttachedContainerArgsRunRealProviderDiagnostic(t *testing.T) {
	got := attachedProviderContainerArgs("candidate:test", "https://diagnostic.example/runs/run/attached")
	want := []string{
		"run", "--rm", "--platform", "linux/amd64",
		"-e", "PRODUCT_CAPTURE_BROWSER_HEADLESS=false",
		"-e", "PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS=https://diagnostic.example",
		"-e", "PRODUCT_CAPTURE_XVFB_READY_TIMEOUT=10",
		"-e", "PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT=10",
		"--entrypoint", "/bin/sh", "candidate:test",
		"-c", headedContainerScript, "--", "/usr/local/bin/product-capture-provider",
		"--browser-diagnostic-url", "https://diagnostic.example/runs/run/attached",
	}
	if !reflect.DeepEqual(stripContainerName(got), want) {
		t.Fatalf("attached args = %#v, want %#v", stripContainerName(got), want)
	}
}

func TestPinnedCloudflaredReleaseMetadata(t *testing.T) {
	if CloudflaredVersion != "2026.7.1" || CloudflaredSHA256 != "79a0ade7fc854f62c1aaef48424d9d979e8c2fcd039189d24db82b84cd146be1" {
		t.Fatalf("cloudflared pin = %s/%s", CloudflaredVersion, CloudflaredSHA256)
	}
	wantURL := "https://github.com/cloudflare/cloudflared/releases/download/2026.7.1/cloudflared-linux-amd64"
	if CloudflaredDownloadURL != wantURL {
		t.Fatalf("cloudflared URL = %q, want %q", CloudflaredDownloadURL, wantURL)
	}
}

func TestQuickTunnelOriginRejectsAPIAndPathURLs(t *testing.T) {
	for _, line := range []string{
		`failed: Post "https://api.trycloudflare.com/tunnel": certificate error`,
		`https://candidate.trycloudflare.com/not-an-origin`,
		`https://two.parts.trycloudflare.com`,
	} {
		if origin := parseQuickTunnelOrigin(line); origin != "" {
			t.Errorf("parseQuickTunnelOrigin(%q) = %q, want empty", line, origin)
		}
	}
	want := "https://native-session-123.trycloudflare.com"
	if origin := parseQuickTunnelOrigin("INF |  " + want + "  |"); origin != want {
		t.Fatalf("generated origin = %q, want %q", origin, want)
	}
}

func TestAlreadyRemovedContainerIsSuccessfulCleanup(t *testing.T) {
	err := errors.New("docker stop: Error response from daemon: No such container: tunnel")
	if got := ignoreMissingContainer(err); got != nil {
		t.Fatalf("ignoreMissingContainer = %v, want nil", got)
	}
	original := errors.New("permission denied")
	if got := ignoreMissingContainer(original); !errors.Is(got, original) {
		t.Fatalf("ignoreMissingContainer = %v, want original", got)
	}
}

func TestPinnedCloudflaredStopForceRemovesContainerAfterReapTimeout(t *testing.T) {
	if os.Getenv("PRODUCT_CAPTURE_TEST_BLOCK_TUNNEL_COMMAND") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	t.Setenv("PRODUCT_CAPTURE_TEST_BLOCK_TUNNEL_COMMAND", "1")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPinnedCloudflaredStopForceRemovesContainerAfterReapTimeout$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	tempDir := filepath.Join(t.TempDir(), "cloudflared")
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var calls []string
	tunnel := &pinnedCloudflaredTunnel{
		cmd:           cmd,
		done:          done,
		containerName: "product-capture-cloudflared-test",
		tempDir:       tempDir,
		reapTimeout:   10 * time.Millisecond,
		docker: func(_ context.Context, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			if len(args) >= 2 && args[0] == "rm" && args[1] == "-f" {
				return cmd.Process.Kill()
			}
			return nil
		},
	}
	err := tunnel.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop after force removal: %v", err)
	}
	for _, want := range []string{
		"stop --timeout 3 product-capture-cloudflared-test",
		"rm -f product-capture-cloudflared-test",
	} {
		if !slices.Contains(calls, want) {
			t.Fatalf("docker calls = %v, missing %q", calls, want)
		}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("docker run process was not reaped after force removal")
	}
	if _, statErr := os.Stat(tempDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cloudflared temp dir remains: %v", statErr)
	}
}

func TestForceContainerAndWaitReapsAfterGraceTimeout(t *testing.T) {
	if os.Getenv("PRODUCT_CAPTURE_TEST_BLOCK_CONTAINER_COMMAND") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	t.Setenv("PRODUCT_CAPTURE_TEST_BLOCK_CONTAINER_COMMAND", "1")
	cmd := exec.Command(os.Args[0], "-test.run=^TestForceContainerAndWaitReapsAfterGraceTimeout$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-wait:
		default:
		}
	})
	forceCalls := 0
	err := forceContainerAndWait(wait, 10*time.Millisecond, func() error {
		forceCalls++
		return cmd.Process.Kill()
	})
	if err != nil {
		t.Fatalf("force and wait: %v", err)
	}
	if forceCalls != 1 {
		t.Fatalf("force calls = %d, want 1", forceCalls)
	}
}

func TestCleanupEphemeralProfileRemovesChromeLocks(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("container-123", filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte("9222\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupEphemeralProfile(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral profile remains: %v", err)
	}
}

func TestNonLinuxCloudflaredContainerReceivesHostCARoots(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Docker fallback contract")
	}
	cmd, _ := cloudflaredCommand("/tmp/cloudflared-linux-amd64", "http://127.0.0.1:1234")
	joined := strings.Join(cmd.Args, " ")
	want := "/etc/ssl/cert.pem:/etc/ssl/certs/ca-certificates.crt:ro"
	if !strings.Contains(joined, want) {
		t.Fatalf("cloudflared Docker args missing CA mount %q: %s", want, joined)
	}
}

func TestCandidateReleaseBuildsOnceAndPublishesOnlyTestedImage(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release.yml")
	for _, want := range []string{
		"load: true",
		"platforms: linux/amd64",
		"go run ./cmd/browser-runtime-conformance --image \"$CANDIDATE\" --output conformance.json",
		"docker push \"$CANDIDATE\"",
		"docker buildx imagetools inspect",
		"steps.build.outputs.imageid",
		"provider_image_ref",
		"Chrome version",
		"Playwright version",
		"Xvfb version",
		"conformance.json",
		"publish-release:",
		"needs: [release, runtime-image]",
		"gh release edit \"${{ github.ref_name }}\" --draft=false",
		"event-type: plugin-release",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{"push: true", "docker save", "oci", "notify-registry:"} {
		if strings.Contains(strings.ToLower(workflow), strings.ToLower(forbidden)) {
			t.Errorf("release workflow contains forbidden %q", forbidden)
		}
	}
	if strings.Count(workflow, "docker/build-push-action@") != 1 {
		t.Fatalf("build-push action count = %d, want 1", strings.Count(workflow, "docker/build-push-action@"))
	}
	conformanceIndex := strings.Index(workflow, "go run ./cmd/browser-runtime-conformance")
	pushIndex := strings.Index(workflow, "docker push \"$CANDIDATE\"")
	if conformanceIndex < 0 || pushIndex < 0 || conformanceIndex > pushIndex {
		t.Fatal("candidate conformance must complete before docker push")
	}
	publishIndex := strings.Index(workflow, "publish-release:")
	editIndex := strings.Index(workflow, "gh release edit")
	if publishIndex < 0 || editIndex < publishIndex {
		t.Fatal("GitHub release publication must occur only in publish-release")
	}
}

func TestCandidateReleaseMetadataAndDocumentation(t *testing.T) {
	dockerfile := readRepositoryFile(t, "docker/product-capture-browser/Dockerfile")
	if !strings.Contains(dockerfile, "PRODUCT_CAPTURE_BROWSER_HEADLESS=false") {
		t.Fatal("runtime image must default to headed Chrome")
	}
	manifest := readRepositoryFile(t, "plugin.json")
	if !strings.Contains(manifest, `"version": "0.1.60"`) || strings.Contains(manifest, "/v0.1.59/") {
		t.Fatal("plugin manifest must be prepared for v0.1.60")
	}
	readme := readRepositoryFile(t, "README.md")
	for _, want := range []string{
		"go run ./cmd/browser-runtime-conformance",
		"decisions/0002-use-ephemeral-diagnostic-tunnel.md",
		"provider_image_ref",
		"provider_component_ref",
		"provider_component_digest",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README missing %q", want)
		}
	}
}

func TestVerifyCloudflaredArtifactRejectsVersionAndDigestMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloudflared")
	data := []byte("test cloudflared artifact")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	wantDigest := hex.EncodeToString(digest[:])
	if err := VerifyCloudflaredArtifact(path, wantDigest, "cloudflared version "+CloudflaredVersion+" (built 2026-07-01)"); err != nil {
		t.Fatalf("valid artifact: %v", err)
	}
	if err := VerifyCloudflaredArtifact(path, strings.Repeat("0", 64), "cloudflared version "+CloudflaredVersion); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("digest mismatch error = %v", err)
	}
	if err := VerifyCloudflaredArtifact(path, wantDigest, "cloudflared version 2026.6.0"); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version mismatch error = %v", err)
	}
}

type fakeTunnel struct {
	origin    string
	stopCalls int
}

type sequenceTunnel struct {
	origins    []string
	startCalls int
	stopCalls  int
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type proxyTunnel struct {
	client    *http.Client
	server    *httptest.Server
	stopCalls int
}

func (t *proxyTunnel) Start(_ context.Context, localURL string) (string, error) {
	target, err := url.Parse(localURL)
	if err != nil {
		return "", err
	}
	t.server = httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
	*t.client = *t.server.Client()
	return t.server.URL, nil
}

func (t *proxyTunnel) Stop(context.Context) error {
	t.stopCalls++
	if t.server != nil {
		t.server.Close()
	}
	return nil
}

func providerPayload(browser BrowserSignals) string {
	data, err := json.Marshal(map[string]any{
		"source":          "product_capture_browser_diagnostic",
		"final_url":       "https://diagnostic.example/runs/run-123/direct",
		"browser_signals": browser,
		"timing":          map[string]float64{"navigation_ms": 12},
	})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func stripContainerName(args []string) []string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--name" {
			return append(append([]string(nil), args[:index]...), args[index+2:]...)
		}
	}
	return args
}

func readRepositoryFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *fakeTunnel) Start(context.Context, string) (string, error) { return f.origin, nil }
func (f *fakeTunnel) Stop(context.Context) error {
	f.stopCalls++
	return nil
}

func (t *sequenceTunnel) Start(context.Context, string) (string, error) {
	if t.startCalls >= len(t.origins) {
		return "", errors.New("unexpected tunnel start")
	}
	origin := t.origins[t.startCalls]
	t.startCalls++
	return origin, nil
}

func (t *sequenceTunnel) Stop(context.Context) error {
	t.stopCalls++
	return nil
}

func matchingObservation(kind string) Observation {
	return Observation{
		Schema: SchemaV1,
		RunID:  "run-123",
		Kind:   kind,
		Browser: BrowserSignals{
			Navigator: NavigatorSignals{
				Webdriver:           false,
				UserAgent:           "Mozilla/5.0 Chrome/140",
				UserAgentData:       UserAgentData{Brands: []Brand{{Brand: "Chromium", Version: "140"}, {Brand: "Google Chrome", Version: "140"}}, Platform: "Linux"},
				Language:            "en-US",
				Languages:           []string{"en-US", "en"},
				Platform:            "Linux x86_64",
				HardwareConcurrency: 8,
				DeviceMemory:        8,
			},
			Window: WindowSignals{OuterWidth: 1920, OuterHeight: 1080, InnerWidth: 1920, InnerHeight: 941},
			Automation: AutomationSignals{
				PlaywrightBindingPresent:     false,
				PlaywrightInitScriptsPresent: false,
			},
			Document: DocumentSignals{},
			WebGL:    WebGLSignals{Available: true, Vendor: "Google Inc.", Renderer: "ANGLE"},
		},
		Request: RequestSignals{
			UserAgent: "Mozilla/5.0 Chrome/140",
			ClientHints: ClientHintSignals{
				Brands:   `"Chromium";v="140", "Google Chrome";v="140"`,
				Mobile:   "?0",
				Platform: `"Linux"`,
			},
			SecFetch:    SecFetchSignals{Dest: "document", Mode: "navigate", Site: "none", User: "?1"},
			HeaderOrder: []string{"user-agent", "sec-ch-ua", "sec-fetch-site"},
		},
		FirstNavigationOrigin: "https://diagnostic.example",
		Timing:                map[string]float64{"navigation_ms": 10},
	}
}

func hasComparison(comparisons []Comparison, field string) bool {
	_, ok := findComparison(comparisons, field)
	return ok
}

func findComparison(comparisons []Comparison, field string) (Comparison, bool) {
	index := slices.IndexFunc(comparisons, func(comparison Comparison) bool { return comparison.Field == field })
	if index < 0 {
		return Comparison{}, false
	}
	return comparisons[index], true
}
