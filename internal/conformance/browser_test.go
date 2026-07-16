package conformance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestCompareBrowserObservationsClassifiesSchemaV1Fields(t *testing.T) {
	direct := matchingObservation("direct")
	attached := matchingObservation("attached")
	attached.Browser.Window.OuterWidth += 2
	attached.Browser.Window.InnerHeight -= 56
	attached.Browser.Navigator.HardwareConcurrency = 4
	attached.Browser.Navigator.DeviceMemory = 2
	attached.Browser.Document.CookiePresent = true
	attached.Browser.Document.CookieLength = 12
	attached.Browser.WebGL.Renderer = "different informational renderer"
	attached.Request.HeaderNames = []string{"sec-fetch-site", "user-agent"}
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
		"browser.screen.width",
		"browser.screen.height",
	} {
		if !hasComparison(report.StableComparisons, field) {
			t.Errorf("stable comparisons missing %q: %+v", field, report.StableComparisons)
		}
	}
	for _, field := range []string{
		"request.header_names",
		"timing",
		"browser.webgl",
		"browser.navigator.hardware_concurrency",
		"browser.navigator.device_memory",
		"browser.document.cookie_present",
		"browser.document.cookie_length",
		"browser.window.inner_height",
	} {
		if _, ok := report.Informational[field]; !ok {
			t.Errorf("informational values missing %q: %+v", field, report.Informational)
		}
	}
}

func TestCompareBrowserObservationsTreatsBrowserChromeHeightAsInformational(t *testing.T) {
	direct := matchingObservation("direct")
	attached := matchingObservation("attached")
	direct.Browser.Window.InnerHeight = 992
	attached.Browser.Window.InnerHeight = 936

	report := Compare(direct, attached, Versions{})
	if report.Verdict != VerdictPass || report.ExitCode() != 0 {
		t.Fatalf("report = %+v, want browser chrome height difference to pass", report)
	}
	if _, ok := findComparison(report.StableComparisons, "browser.window.inner_height"); ok {
		t.Fatalf("inner height remained a stable comparison: %+v", report.StableComparisons)
	}
	pair, ok := report.Informational["browser.window.inner_height"]
	if !ok || pair.Direct != 992 || pair.Attached != 936 {
		t.Fatalf("inner height informational pair = %+v, found %v", pair, ok)
	}
}

func TestCompareBrowserObservationsAllowsUnavailableInformationalInnerHeight(t *testing.T) {
	tests := []struct {
		name           string
		directHeight   int
		attachedHeight int
	}{
		{name: "direct unavailable", directHeight: 0, attachedHeight: 936},
		{name: "attached unavailable", directHeight: 992, attachedHeight: 0},
		{name: "both unavailable", directHeight: 0, attachedHeight: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direct := matchingObservation("direct")
			attached := matchingObservation("attached")
			direct.Browser.Window.InnerHeight = tt.directHeight
			attached.Browser.Window.InnerHeight = tt.attachedHeight

			report := Compare(direct, attached, Versions{})
			if report.Verdict != VerdictPass || report.ExitCode() != 0 {
				t.Fatalf("report = %+v, want unavailable informational height to pass", report)
			}
			pair, ok := report.Informational["browser.window.inner_height"]
			if !ok || pair.Direct != tt.directHeight || pair.Attached != tt.attachedHeight {
				t.Fatalf("inner height informational pair = %+v, found %v", pair, ok)
			}
		})
	}
}

func TestCompareBrowserObservationsAllowsTwoPixelScreenTolerance(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		mutate func(*Observation)
	}{
		{name: "width plus", field: "browser.screen.width", mutate: func(o *Observation) { o.Browser.Screen.Width += 2 }},
		{name: "width minus", field: "browser.screen.width", mutate: func(o *Observation) { o.Browser.Screen.Width -= 2 }},
		{name: "height plus", field: "browser.screen.height", mutate: func(o *Observation) { o.Browser.Screen.Height += 2 }},
		{name: "height minus", field: "browser.screen.height", mutate: func(o *Observation) { o.Browser.Screen.Height -= 2 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direct := matchingObservation("direct")
			attached := matchingObservation("attached")
			tt.mutate(&attached)

			report := Compare(direct, attached, Versions{})
			if report.Verdict != VerdictPass || report.ExitCode() != 0 {
				t.Fatalf("report = %+v, want two-pixel screen difference to pass", report)
			}
			comparison, ok := findComparison(report.StableComparisons, tt.field)
			if !ok || !comparison.Match || comparison.Tolerance != 2 {
				t.Fatalf("comparison %q = %+v, found %v", tt.field, comparison, ok)
			}
		})
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
		{name: "content width tolerance", field: "browser.window.inner_width", mutate: func(o *Observation) { o.Browser.Window.InnerWidth += 3 }},
		{name: "screen width tolerance", field: "browser.screen.width", mutate: func(o *Observation) { o.Browser.Screen.Width += 3 }},
		{name: "screen width negative tolerance", field: "browser.screen.width", mutate: func(o *Observation) { o.Browser.Screen.Width -= 3 }},
		{name: "screen height tolerance", field: "browser.screen.height", mutate: func(o *Observation) { o.Browser.Screen.Height += 3 }},
		{name: "screen height negative tolerance", field: "browser.screen.height", mutate: func(o *Observation) { o.Browser.Screen.Height -= 3 }},
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

func TestConformanceFailureErrorReportsScreenMismatchLabels(t *testing.T) {
	for _, field := range []string{"browser.screen.width", "browser.screen.height"} {
		t.Run(field, func(t *testing.T) {
			report := Report{StableComparisons: []Comparison{{Field: field, Match: false}}}

			message := conformanceFailureError(report).Error()
			if !strings.Contains(message, field) {
				t.Fatalf("failure message = %q, want screen mismatch label %q", message, field)
			}
			if strings.Contains(message, string(failureClassReportValidation)) {
				t.Fatalf("failure message = %q, valid screen mismatch degraded to report validation", message)
			}
		})
	}
}

func TestConformanceFailureErrorPrioritizesValidationClassesAndReportsTruncation(t *testing.T) {
	report := Compare(matchingObservation("direct"), matchingObservation("attached"), Versions{})
	report.Errors = []string{`both observations must use schema "v1"`}
	report.FailureClasses = []FailureClass{failureClassObservationSchema}
	for index := range report.StableComparisons {
		report.StableComparisons[index].Match = false
	}

	message := conformanceFailureError(report).Error()
	if !strings.Contains(message, "observation.schema") {
		t.Fatalf("failure message = %q, want validation class", message)
	}
	if !strings.Contains(message, "report.validation") {
		t.Fatalf("failure message = %q, want prioritized generic validation class", message)
	}
	if !strings.Contains(message, "additional_labels:14") {
		t.Fatalf("failure message = %q, want bounded truncation count", message)
	}
	labels := strings.Split(strings.TrimPrefix(message, "browser runtime conformance failed: "), ", ")
	if len(labels) != 12 || labels[len(labels)-1] != "additional_labels:14" {
		t.Fatalf("failure labels = %v, want exactly 12 with truncation marker", labels)
	}
}

func TestConformanceFailureErrorRejectsUnknownFailureClassWithoutOtherErrors(t *testing.T) {
	report := Report{FailureClasses: []FailureClass{"secret-observed-value"}}

	message := conformanceFailureError(report).Error()
	if !strings.Contains(message, "report.validation") {
		t.Fatalf("failure message = %q, want generic validation class", message)
	}
	if strings.Contains(message, "secret-observed-value") {
		t.Fatalf("failure message exposes unknown failure class: %q", message)
	}
}

func TestConformanceFailureErrorRejectsUnknownComparisonFields(t *testing.T) {
	report := Report{
		StableComparisons: []Comparison{{Field: "secret-observed-value", Match: false}},
	}

	message := conformanceFailureError(report).Error()
	if !strings.Contains(message, "report.validation") {
		t.Fatalf("failure message = %q, want generic validation class", message)
	}
	if strings.Contains(message, "secret-observed-value") {
		t.Fatalf("failure message exposes unknown comparison field: %q", message)
	}
}

func TestConformanceFailureErrorRejectsUnknownClassesAndSignalsUnclassifiedErrors(t *testing.T) {
	report := Report{
		Errors:         []string{"known classified error", "future unclassified secret-value"},
		FailureClasses: []FailureClass{failureClassObservationSchema, "secret-value"},
	}

	message := conformanceFailureError(report).Error()
	if !strings.Contains(message, "observation.schema") || !strings.Contains(message, "report.validation") {
		t.Fatalf("failure message = %q, want known and generic validation classes", message)
	}
	if strings.Contains(message, "secret-value") {
		t.Fatalf("failure message exposes unknown class or report value: %q", message)
	}
}

func TestCompareEmitsStableFailureClassesAtSource(t *testing.T) {
	tests := []struct {
		name  string
		class FailureClass
		apply func(*Observation, *Observation)
	}{
		{name: "schema", class: failureClassObservationSchema, apply: func(_, attached *Observation) { attached.Schema = "v2" }},
		{name: "run correlation", class: failureClassObservationRunCorrelation, apply: func(_, attached *Observation) { attached.RunID = "other" }},
		{name: "order", class: failureClassObservationOrder, apply: func(direct, _ *Observation) { direct.Kind = "attached" }},
		{name: "direct evidence", class: failureClassDirectInvalidEvidence, apply: func(direct, _ *Observation) { direct.Request.UserAgent = "" }},
		{name: "attached evidence", class: failureClassAttachedInvalidEvidence, apply: func(_, attached *Observation) { attached.Request.UserAgent = "" }},
		{name: "automation globals", class: failureClassAutomationGlobalsPresent, apply: func(_, attached *Observation) { attached.Browser.Automation.PlaywrightBindingPresent = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direct := matchingObservation("direct")
			attached := matchingObservation("attached")
			tt.apply(&direct, &attached)

			report := Compare(direct, attached, Versions{})
			if !reflect.DeepEqual(report.FailureClasses, []FailureClass{tt.class}) {
				t.Fatalf("failure classes = %v, want exactly %q", report.FailureClasses, tt.class)
			}
		})
	}
	direct := matchingObservation("direct")
	attached := matchingObservation("attached")
	if report := Compare(direct, attached, Versions{}); len(report.FailureClasses) != 0 {
		t.Fatalf("passing comparison failure classes = %v, want none", report.FailureClasses)
	}
}

func TestCompareBrowserObservationsTreatsBrandAndLanguageFieldsAsSets(t *testing.T) {
	direct := matchingObservation("direct")
	attached := matchingObservation("attached")
	slices.Reverse(attached.Browser.Navigator.UserAgentData.Brands)
	attached.Browser.Navigator.UserAgentData.Brands = append(attached.Browser.Navigator.UserAgentData.Brands, attached.Browser.Navigator.UserAgentData.Brands[0])
	slices.Reverse(attached.Browser.Navigator.Languages)
	attached.Browser.Navigator.Languages = append(attached.Browser.Navigator.Languages, attached.Browser.Navigator.Languages[0])
	attached.Request.ClientHints.Brands = ` "Google Chrome";v="140" , "Chromium";v="140", "Google Chrome";v="140" `

	report := Compare(direct, attached, Versions{})
	if report.Verdict != VerdictPass {
		t.Fatalf("report = %+v, want reordered semantic sets with duplicates to match", report)
	}
	for _, field := range []string{
		"browser.navigator.user_agent_data.brands",
		"browser.navigator.languages",
		"request.client_hints.brands",
	} {
		comparison, ok := findComparison(report.StableComparisons, field)
		if !ok || !comparison.Match {
			t.Errorf("comparison %q = %+v, found %v", field, comparison, ok)
		}
	}
}

func TestCompareBrowserObservationsRejectsEmptyStableEvidence(t *testing.T) {
	tests := map[string]func(*Observation){
		"user agent":       func(o *Observation) { o.Browser.Navigator.UserAgent = "" },
		"browser brands":   func(o *Observation) { o.Browser.Navigator.UserAgentData.Brands = nil },
		"language":         func(o *Observation) { o.Browser.Navigator.Language = "" },
		"language set":     func(o *Observation) { o.Browser.Navigator.Languages = nil },
		"browser platform": func(o *Observation) { o.Browser.Navigator.Platform = "" },
		"client platform":  func(o *Observation) { o.Browser.Navigator.UserAgentData.Platform = "" },
		"window dimensions": func(o *Observation) {
			o.Browser.Window = WindowSignals{}
		},
		"screen dimensions": func(o *Observation) {
			o.Browser.Screen = ScreenSignals{}
		},
		"request user agent": func(o *Observation) { o.Request.UserAgent = "" },
		"request brands":     func(o *Observation) { o.Request.ClientHints.Brands = "" },
		"sec-fetch dest":     func(o *Observation) { o.Request.SecFetch.Dest = "" },
		"sec-fetch mode":     func(o *Observation) { o.Request.SecFetch.Mode = "" },
		"sec-fetch site":     func(o *Observation) { o.Request.SecFetch.Site = "" },
		"sec-fetch user":     func(o *Observation) { o.Request.SecFetch.User = "" },
		"navigation origin":  func(o *Observation) { o.FirstNavigationOrigin = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			direct := matchingObservation("direct")
			attached := matchingObservation("attached")
			mutate(&direct)
			mutate(&attached)
			report := Compare(direct, attached, Versions{})
			if report.Verdict != VerdictFail || len(report.Errors) == 0 {
				t.Fatalf("report = %+v, want explicit empty-evidence failure", report)
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
		Tunnel:           tunnel,
		HTTPClient:       wrongEndpoint.Client(),
		TunnelHTTPClient: wrongEndpoint.Client(),
		LaunchDirect: func(context.Context, string, string, bool) error {
			return errors.New("direct launch must not run before health correlation")
		},
		LaunchAttached: func(context.Context, string, string, bool) error {
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

func TestRunnerPropagatesUnexpectedDiagnosticServerFailure(t *testing.T) {
	serveErr := errors.New("injected listener failure")
	runner := Runner{Dependencies: Dependencies{
		Listen: func(string, string) (net.Listener, error) {
			return &failingListener{err: serveErr}, nil
		},
		Tunnel:           contextCancellationTunnel{},
		TunnelHTTPClient: &http.Client{Timeout: time.Second},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := runner.Run(ctx, Options{
		Image:  "candidate:test",
		Output: filepath.Join(t.TempDir(), "conformance.json"),
	})
	if !errors.Is(err, serveErr) {
		t.Fatalf("Run error = %v, want diagnostic server failure", err)
	}
}

func TestFetchRunHealthRetriesTransientDNSBeforeAcceptingCorrelation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		started := time.Now()
		if err := fetchRunHealth(context.Background(), client, "https://diagnostic.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry); err != nil {
			t.Fatal(err)
		}
		if calls != 3 {
			t.Fatalf("health calls = %d, want 3", calls)
		}
		if elapsed := time.Since(started); elapsed != 2*time.Millisecond {
			t.Fatalf("managed publication retry elapsed = %s, want %s", elapsed, 2*time.Millisecond)
		}
	})
}

func TestFetchRunHealthRetriesTemporaryDNSForOperatorOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, &net.DNSError{Err: "temporary failure", Name: request.URL.Hostname(), IsTemporary: true}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"run-123"}`)),
				Request:    request,
			}, nil
		})}
		started := time.Now()
		if err := fetchRunHealth(context.Background(), client, "https://operator.example/runs/run-123/healthz", "run-123", time.Millisecond, false, waitForDiagnosticHealthRetry); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("operator health calls = %d, want temporary DNS retry followed by success", calls)
		}
		if elapsed := time.Since(started); elapsed != time.Millisecond {
			t.Fatalf("operator health retry elapsed = %s, want %s", elapsed, time.Millisecond)
		}
	})
}

func TestFetchRunHealthPreservesOperatorTransportCauseOnExhaustion(t *testing.T) {
	cause := errors.New("operator transport cause")
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.Join(
			&net.DNSError{Err: "temporary failure", Name: request.URL.Hostname(), IsTemporary: true},
			cause,
		)
	})}
	stopErr := errors.New("stop after operator health retry")
	err := fetchRunHealth(
		context.Background(),
		client,
		"https://operator.example/runs/run-123/healthz",
		"run-123",
		time.Millisecond,
		false,
		func(context.Context, time.Duration) error { return stopErr },
	)
	if !errors.Is(err, errDiagnosticHealthEndpointUnreachable) || !errors.Is(err, cause) || !errors.Is(err, stopErr) {
		t.Fatalf("operator health error = %v, want endpoint classification, transport cause, and retry-stop cause", err)
	}
}

func TestFetchRunHealthRetriesTemporaryDNSForManagedOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		cause := errors.New("temporary resolver cause")
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, errors.Join(&net.DNSError{Err: "temporary failure", Name: request.URL.Hostname(), IsTemporary: true}, cause)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"run-123"}`)),
				Request:    request,
			}, nil
		})}
		started := time.Now()
		if err := fetchRunHealth(context.Background(), client, "https://managed.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("managed health calls = %d, want temporary DNS retry followed by success", calls)
		}
		if elapsed := time.Since(started); elapsed != time.Millisecond {
			t.Fatalf("managed resolver retry elapsed = %s, want %s", elapsed, time.Millisecond)
		}
	})
}

func TestFetchRunHealthRetriesCloudflareOriginDNSFailureOnlyForManagedTunnel(t *testing.T) {
	healthResponse := func(request *http.Request, status int) *http.Response {
		var body io.ReadCloser = http.NoBody
		if status == http.StatusOK {
			body = io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"run-123"}`))
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}
	}

	t.Run("managed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			failureBody := &trackingReadCloser{Reader: strings.NewReader("<!doctype html><title>Origin DNS error</title>")}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					response := healthResponse(request, cloudflareOriginDNSFailureStatusCode)
					response.Body = failureBody
					return response, nil
				}
				return healthResponse(request, http.StatusOK), nil
			})}
			started := time.Now()
			err := fetchRunHealth(
				context.Background(),
				client,
				"https://managed.example/runs/run-123/healthz",
				"run-123",
				time.Millisecond,
				true,
				waitForDiagnosticHealthRetry,
			)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("managed health calls = %d, want status 530 retry followed by success", calls)
			}
			if !failureBody.closed {
				t.Fatal("managed status 530 HTML body was not closed")
			}
			if elapsed := time.Since(started); elapsed != time.Millisecond {
				t.Fatalf("managed status 530 retry elapsed = %s, want %s", elapsed, time.Millisecond)
			}
		})
	})

	t.Run("explicit", func(t *testing.T) {
		calls := 0
		retryWaits := 0
		failureBody := &trackingReadCloser{Reader: strings.NewReader("<!doctype html><title>Origin DNS error</title>")}
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			response := healthResponse(request, cloudflareOriginDNSFailureStatusCode)
			response.Body = failureBody
			return response, nil
		})}
		err := fetchRunHealth(
			context.Background(),
			client,
			"https://operator.example/runs/run-123/healthz",
			"run-123",
			time.Millisecond,
			false,
			func(context.Context, time.Duration) error {
				retryWaits++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "status 530") {
			t.Fatalf("explicit health error = %v, want terminal status 530", err)
		}
		if calls != 1 || retryWaits != 0 {
			t.Fatalf("explicit health calls/retry waits = %d/%d, want 1/0", calls, retryWaits)
		}
		if !failureBody.closed {
			t.Fatal("explicit status 530 HTML body was not closed")
		}
	})
}

func TestFetchRunHealthClassifiesExhaustedTemporaryDNS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		cause := errors.New("temporary resolver cause")
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			return nil, errors.Join(&net.DNSError{Err: "temporary failure", Name: request.URL.Hostname(), IsTemporary: true}, cause)
		})}
		const timeout = 2500 * time.Microsecond
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		started := time.Now()
		err := fetchRunHealth(ctx, client, "https://managed.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry)
		if !errors.Is(err, errDiagnosticDNSResolverUnavailable) || !errors.Is(err, cause) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("managed health error = %v, want resolver classification, cause, and deadline", err)
		}
		if calls != 3 {
			t.Fatalf("managed health calls = %d, want three attempts before deadline", calls)
		}
		if elapsed := time.Since(started); elapsed != timeout {
			t.Fatalf("managed resolver exhaustion elapsed = %s, want %s", elapsed, timeout)
		}
	})
}

func TestDiagnosticHealthClientDialsOriginOverIPv4(t *testing.T) {
	var dialNetwork, dialAddress string
	dialErr := errors.New("stop after observing diagnostic health dial")
	client := newDiagnosticHealthClient(diagnosticDNSResolverAddress, func(_ context.Context, network, address string) (net.Conn, error) {
		dialNetwork = network
		dialAddress = address
		return nil, dialErr
	})

	request, err := http.NewRequest(http.MethodGet, "https://93.184.216.34/healthz", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(request)
	if !errors.Is(err, dialErr) {
		t.Fatalf("diagnostic health request error = %v, want dial sentinel", err)
	}
	if dialNetwork != "tcp4" || dialAddress != "93.184.216.34:443" {
		t.Fatalf("diagnostic health dial = %q %q, want tcp4 origin", dialNetwork, dialAddress)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("diagnostic health transport = %#v, want direct transport without proxy", client.Transport)
	}
}

func TestDiagnosticHealthClientRejectsPrivateResolutionBeforeDial(t *testing.T) {
	dnsServer := startTestDNSServer(t, testDNSBehavior{expectedName: "diagnostic.example.", publishAOn: 1})
	dialCalls := 0
	client := newDiagnosticHealthClient(dnsServer.address, func(context.Context, string, string) (net.Conn, error) {
		dialCalls++
		return nil, errors.New("private address reached final dialer")
	})
	request, err := http.NewRequest(http.MethodGet, "http://diagnostic.example.:8080/healthz", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(request)
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("diagnostic health error = %v, want non-public address rejection", err)
	}
	if dialCalls != 0 {
		t.Fatalf("final origin dial calls = %d, want private address rejected before dialing", dialCalls)
	}
}

func TestDiagnosticIPv4LookupBypassesHostsFile(t *testing.T) {
	dnsServer := startTestDNSServer(t, testDNSBehavior{
		expectedName: "localhost.",
		publishAOn:   1,
		aRecord:      [4]byte{93, 184, 216, 34},
	})

	addresses, err := newDiagnosticIPv4Lookup(dnsServer.address)(context.Background(), "localhost")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
	if !slices.Equal(addresses, want) {
		t.Fatalf("diagnostic IPv4 addresses = %v, want configured-DNS answer %v", addresses, want)
	}
	if events := dnsServer.Events(); !slices.Contains(events, "A-published") {
		t.Fatalf("DNS events = %v, want configured resolver query", events)
	}
}

func TestDiagnosticIPv4LookupFallsBackToTCPAfterTruncatedUDPResponse(t *testing.T) {
	dnsServer := startTruncatedTestDNSServer(t, "diagnostic.example.", [4]byte{93, 184, 216, 34})

	addresses, err := newDiagnosticIPv4Lookup(dnsServer.address)(context.Background(), "diagnostic.example")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
	if !slices.Equal(addresses, want) {
		t.Fatalf("diagnostic IPv4 addresses = %v, want TCP answer %v", addresses, want)
	}
	if got := dnsServer.udpQueries.Load(); got != 1 {
		t.Fatalf("UDP DNS queries = %d, want 1", got)
	}
	if got := dnsServer.tcpQueries.Load(); got != 1 {
		t.Fatalf("TCP DNS queries = %d, want 1 after truncated UDP response", got)
	}
}

func TestDiagnosticIPv4LookupRejectsTrailingWireData(t *testing.T) {
	t.Run("UDP", func(t *testing.T) {
		dnsServer := startTestDNSServer(t, testDNSBehavior{
			expectedName:     "diagnostic.example.",
			publishAOn:       1,
			aRecord:          [4]byte{93, 184, 216, 34},
			trailingResponse: []byte{0xde, 0xad},
		})
		_, err := newDiagnosticIPv4Lookup(dnsServer.address)(context.Background(), "diagnostic.example")
		if err == nil || !retryableDiagnosticIPv4LookupError(err) {
			t.Fatalf("diagnostic UDP lookup error = %v, want retryable trailing-data rejection", err)
		}
	})

	t.Run("TCP", func(t *testing.T) {
		dnsServer := startTruncatedTestDNSServer(
			t,
			"diagnostic.example.",
			[4]byte{93, 184, 216, 34},
			truncatedTestDNSOptions{trailingTCP: []byte{0xde, 0xad}},
		)
		_, err := newDiagnosticIPv4Lookup(dnsServer.address)(context.Background(), "diagnostic.example")
		if err == nil || !retryableDiagnosticIPv4LookupError(err) {
			t.Fatalf("diagnostic TCP lookup error = %v, want retryable trailing-data rejection", err)
		}
	})
}

func TestValidateDiagnosticDNSWireLengthRejectsExtraConsumedRecordData(t *testing.T) {
	name, err := dnsmessage.NewName("diagnostic.example.")
	if err != nil {
		t.Fatal(err)
	}
	targetName, err := dnsmessage.NewName("target.example.")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		typ       dnsmessage.Type
		body      dnsmessage.ResourceBody
		typeBytes []byte
	}{
		{
			name:      "A",
			typ:       dnsmessage.TypeA,
			body:      &dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}},
			typeBytes: []byte{0, byte(dnsmessage.TypeA)},
		},
		{
			name:      "CNAME",
			typ:       dnsmessage.TypeCNAME,
			body:      &dnsmessage.CNAMEResource{CNAME: targetName},
			typeBytes: []byte{0, byte(dnsmessage.TypeCNAME)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message, err := (&dnsmessage.Message{
				Header: dnsmessage.Header{ID: 7, Response: true},
				Questions: []dnsmessage.Question{{
					Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
				}},
				Answers: []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: name, Type: tc.typ, Class: dnsmessage.ClassINET, TTL: 1},
					Body:   tc.body,
				}},
			}).Pack()
			if err != nil {
				t.Fatal(err)
			}
			headerPrefix := append(slices.Clone(tc.typeBytes), 0, 1, 0, 0, 0, 1)
			headerOffset := bytes.LastIndex(message, headerPrefix)
			if headerOffset < 0 {
				t.Fatalf("packed %s resource header not found", tc.name)
			}
			lengthOffset := headerOffset + len(headerPrefix)
			dataLength := binary.BigEndian.Uint16(message[lengthOffset : lengthOffset+2])
			binary.BigEndian.PutUint16(message[lengthOffset:lengthOffset+2], dataLength+1)
			message = append(message, 0xde)

			if err := validateDiagnosticDNSWireLength(message); err == nil {
				t.Fatalf("strict DNS validation accepted %s RDATA with an extra consumed byte", tc.name)
			}
		})
	}
}

func TestTruncatedTestDNSServerClosesStalledTCPConnection(t *testing.T) {
	var connection net.Conn
	t.Cleanup(func() {
		if connection != nil {
			_ = connection.Close()
		}
	})
	dnsServer := startTruncatedTestDNSServer(t, "diagnostic.example.", [4]byte{93, 184, 216, 34})
	var err error
	connection, err = net.Dial("tcp4", dnsServer.address)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-dnsServer.tcpAccepted:
	case <-time.After(time.Second):
		t.Fatal("truncated test DNS server did not accept stalled TCP connection")
	}
}

func TestListenTruncatedTestDNSRetriesTCPPortCollision(t *testing.T) {
	var packets []*trackingPacketConn
	tcpCalls := 0
	udpConnection, tcpListener, err := listenTruncatedTestDNS(
		func(network, address string) (net.PacketConn, error) {
			connection, err := net.ListenPacket(network, address)
			if err != nil {
				return nil, err
			}
			tracked := &trackingPacketConn{PacketConn: connection}
			packets = append(packets, tracked)
			return tracked, nil
		},
		func(network, address string) (net.Listener, error) {
			tcpCalls++
			if tcpCalls == 1 {
				return nil, &net.OpError{Op: "listen", Net: network, Err: syscall.EADDRINUSE}
			}
			return net.Listen(network, address)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = udpConnection.Close()
		_ = tcpListener.Close()
	})
	if tcpCalls < 2 {
		t.Fatalf("TCP listen calls = %d, want retry after port collision", tcpCalls)
	}
	if len(packets) < 2 || !packets[0].closed.Load() {
		t.Fatalf("UDP attempts = %d/first closed=%t, want collided UDP listener closed before retry", len(packets), len(packets) > 0 && packets[0].closed.Load())
	}
}

func TestListenTruncatedTestDNSBoundsTCPPortCollisions(t *testing.T) {
	var packets []*trackingPacketConn
	tcpCalls := 0
	udpConnection, tcpListener, err := listenTruncatedTestDNS(
		func(network, address string) (net.PacketConn, error) {
			connection, err := net.ListenPacket(network, address)
			if err != nil {
				return nil, err
			}
			tracked := &trackingPacketConn{PacketConn: connection}
			packets = append(packets, tracked)
			return tracked, nil
		},
		func(network, _ string) (net.Listener, error) {
			tcpCalls++
			return nil, &net.OpError{Op: "listen", Net: network, Err: syscall.EADDRINUSE}
		},
	)
	if udpConnection != nil || tcpListener != nil || !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("bounded listener result = %v/%v/%v, want nil listeners and address-in-use cause", udpConnection, tcpListener, err)
	}
	if tcpCalls != 32 || len(packets) != 32 {
		t.Fatalf("bounded listener calls = TCP %d/UDP %d, want 32 each", tcpCalls, len(packets))
	}
	for attempt, packet := range packets {
		if !packet.closed.Load() {
			t.Fatalf("UDP listener attempt %d remained open after collision exhaustion", attempt+1)
		}
	}
}

func TestListenTruncatedTestDNSDoesNotRetryOtherTCPError(t *testing.T) {
	var packets []*trackingPacketConn
	tcpCalls := 0
	wantErr := errors.New("TCP listener denied")
	udpConnection, tcpListener, err := listenTruncatedTestDNS(
		func(network, address string) (net.PacketConn, error) {
			connection, err := net.ListenPacket(network, address)
			if err != nil {
				return nil, err
			}
			tracked := &trackingPacketConn{PacketConn: connection}
			packets = append(packets, tracked)
			return tracked, nil
		},
		func(string, string) (net.Listener, error) {
			tcpCalls++
			return nil, wantErr
		},
	)
	if udpConnection != nil || tcpListener != nil || !errors.Is(err, wantErr) {
		t.Fatalf("non-retryable listener result = %v/%v/%v, want nil listeners and original cause", udpConnection, tcpListener, err)
	}
	if tcpCalls != 1 || len(packets) != 1 || !packets[0].closed.Load() {
		t.Fatalf("non-retryable listener calls = TCP %d/UDP %d/closed %t, want 1/1/true", tcpCalls, len(packets), len(packets) == 1 && packets[0].closed.Load())
	}
}

func TestDiagnosticIPv4LookupClassifiesProtocolFailures(t *testing.T) {
	tests := []struct {
		name       string
		start      func(*testing.T) string
		wantDetail string
	}{
		{
			name: "malformed packet",
			start: func(t *testing.T) string {
				return startTestDNSServer(t, testDNSBehavior{
					expectedName:      "diagnostic.example.",
					malformedResponse: true,
				}).address
			},
			wantDetail: "parse diagnostic DNS response header",
		},
		{
			name: "wrong transaction ID",
			start: func(t *testing.T) string {
				return startTestDNSServer(t, testDNSBehavior{
					expectedName:    "diagnostic.example.",
					responseIDDelta: 1,
				}).address
			},
			wantDetail: "response header does not match request",
		},
		{
			name: "mismatched question",
			start: func(t *testing.T) string {
				return startTestDNSServer(t, testDNSBehavior{
					expectedName: "diagnostic.example.",
					responseName: "other.example.",
				}).address
			},
			wantDetail: "response question does not match request",
		},
		{
			name: "truncated TCP response",
			start: func(t *testing.T) string {
				return startTruncatedTestDNSServer(t, "diagnostic.example.", [4]byte{93, 184, 216, 34}, truncatedTestDNSOptions{truncateTCP: true}).address
			},
			wantDetail: "TCP response is truncated",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newDiagnosticIPv4Lookup(tc.start(t))(context.Background(), "diagnostic.example")
			if err == nil {
				t.Fatal("diagnostic IPv4 lookup accepted invalid DNS response")
			}
			var dnsErr *net.DNSError
			if !errors.As(err, &dnsErr) || !dnsErr.IsTemporary {
				t.Fatalf("diagnostic IPv4 error = %v, want temporary DNS classification", err)
			}
			if !retryableDiagnosticIPv4LookupError(err) {
				t.Fatalf("diagnostic IPv4 error = %v, want direct resolver retry classification", err)
			}
			if !strings.Contains(err.Error(), tc.wantDetail) {
				t.Fatalf("diagnostic IPv4 error = %v, want preserved detail %q", err, tc.wantDetail)
			}
		})
	}
}

func TestDiagnosticDNSProtocolErrorPreservesCause(t *testing.T) {
	cause := errors.New("protocol cause")
	err := diagnosticDNSProtocolError("diagnostic.example", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("diagnostic DNS protocol error = %v, want original cause", err)
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsTemporary {
		t.Fatalf("diagnostic DNS protocol error = %v, want temporary DNS classification", err)
	}
}

func TestDiagnosticIPv4LookupValidatesAnswerOwnership(t *testing.T) {
	t.Run("rejects unrelated A owner", func(t *testing.T) {
		dnsServer := startTestDNSServer(t, testDNSBehavior{
			expectedName: "diagnostic.example.",
			publishAOn:   1,
			aRecord:      [4]byte{93, 184, 216, 34},
			aRecordName:  "unrelated.example.",
		})
		_, err := newDiagnosticIPv4Lookup(dnsServer.address)(context.Background(), "diagnostic.example")
		if err == nil {
			t.Fatal("diagnostic IPv4 lookup accepted an unrelated A answer")
		}
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) || !dnsErr.IsTemporary || !strings.Contains(err.Error(), "unrelated A answer") {
			t.Fatalf("diagnostic IPv4 error = %v, want temporary unrelated-answer rejection", err)
		}
	})

	t.Run("accepts CNAME-owned A answer", func(t *testing.T) {
		dnsServer := startTestDNSServer(t, testDNSBehavior{
			expectedName: "diagnostic.example.",
			publishAOn:   1,
			aRecord:      [4]byte{93, 184, 216, 34},
			cnameTarget:  "alias.example.",
		})
		addresses, err := newDiagnosticIPv4Lookup(dnsServer.address)(context.Background(), "diagnostic.example")
		if err != nil {
			t.Fatal(err)
		}
		want := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
		if !slices.Equal(addresses, want) {
			t.Fatalf("diagnostic IPv4 addresses = %v, want CNAME-owned answer %v", addresses, want)
		}
	})

	t.Run("rejects CNAME loop", func(t *testing.T) {
		dnsServer := startTestDNSServer(t, testDNSBehavior{
			expectedName: "diagnostic.example.",
			publishAOn:   1,
			cnameTarget:  "diagnostic.example.",
			omitA:        true,
		})
		_, err := newDiagnosticIPv4Lookup(dnsServer.address)(context.Background(), "diagnostic.example")
		if err == nil {
			t.Fatal("diagnostic IPv4 lookup accepted a CNAME loop")
		}
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) || !dnsErr.IsTemporary || !strings.Contains(err.Error(), "CNAME loop") {
			t.Fatalf("diagnostic IPv4 error = %v, want temporary CNAME-loop rejection", err)
		}
	})
}

func TestDiagnosticDNSIPv4AnswersValidatesCNAMEBranches(t *testing.T) {
	name := func(value string) dnsmessage.Name {
		result, err := dnsmessage.NewName(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	aResource := func(owner string) dnsmessage.Resource {
		return dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: name(owner), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1},
			Body:   &dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}},
		}
	}
	cnameResource := func(owner, target string) dnsmessage.Resource {
		return dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: name(owner), Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 1},
			Body:   &dnsmessage.CNAMEResource{CNAME: name(target)},
		}
	}
	chain := func(hops int) []dnsmessage.Resource {
		answers := make([]dnsmessage.Resource, 0, hops+1)
		for hop := range hops {
			answers = append(answers, cnameResource(
				fmt.Sprintf("hop-%d.example.", hop),
				fmt.Sprintf("hop-%d.example.", hop+1),
			))
		}
		return append(answers, aResource(fmt.Sprintf("hop-%d.example.", hops)))
	}

	t.Run("accepts hop limit", func(t *testing.T) {
		addresses, err := diagnosticDNSIPv4Answers(chain(maxDiagnosticDNSCNAMEHops), name("hop-0.example."))
		if err != nil {
			t.Fatal(err)
		}
		want := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
		if !slices.Equal(addresses, want) {
			t.Fatalf("diagnostic IPv4 answers = %v, want %v", addresses, want)
		}
	})

	for _, tc := range []struct {
		name       string
		answers    []dnsmessage.Resource
		query      string
		wantDetail string
	}{
		{
			name:       "rejects excessive hops",
			answers:    chain(maxDiagnosticDNSCNAMEHops + 1),
			query:      "hop-0.example.",
			wantDetail: "hop limit",
		},
		{
			name: "rejects conflicting CNAMEs",
			answers: []dnsmessage.Resource{
				cnameResource("diagnostic.example.", "alias-a.example."),
				cnameResource("diagnostic.example.", "alias-b.example."),
			},
			query:      "diagnostic.example.",
			wantDetail: "conflicting CNAME",
		},
		{
			name: "rejects A and CNAME owner",
			answers: []dnsmessage.Resource{
				aResource("diagnostic.example."),
				cnameResource("diagnostic.example.", "alias.example."),
			},
			query:      "diagnostic.example.",
			wantDetail: "both A and CNAME",
		},
		{
			name: "rejects unrelated CNAME",
			answers: []dnsmessage.Resource{
				aResource("diagnostic.example."),
				cnameResource("unrelated.example.", "alias.example."),
			},
			query:      "diagnostic.example.",
			wantDetail: "unrelated CNAME",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := diagnosticDNSIPv4Answers(tc.answers, name(tc.query))
			if err == nil || !strings.Contains(err.Error(), tc.wantDetail) {
				t.Fatalf("diagnostic CNAME error = %v, want containing %q", err, tc.wantDetail)
			}
		})
	}
}

func TestDiagnosticHealthClientDoesNotDependOnGlobalTransportType(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("global transport must not be used")
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("newDiagnosticHealthClient panicked with custom global transport: %v", recovered)
		}
	}()

	client := newDiagnosticHealthClient(diagnosticDNSResolverAddress, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("stop after transport construction")
	})
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatalf("diagnostic health transport = %T, want dedicated *http.Transport", client.Transport)
	}
}

func TestDiagnosticHealthClientDoesNotInheritGlobalTLSPolicy(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // Deliberately unsafe global fixture.
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("unsafe global TLS dialer")
		},
	}
	t.Cleanup(func() { http.DefaultTransport = original })

	client := newDiagnosticHealthClient(diagnosticDNSResolverAddress, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("stop after transport construction")
	})
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("diagnostic health transport = %T, want dedicated *http.Transport", client.Transport)
	}
	if transport.TLSClientConfig != nil || transport.DialTLSContext != nil {
		t.Fatalf("diagnostic health transport inherited global TLS policy: %#v", transport)
	}
}

func TestDiagnosticHealthClientRejectsRedirectWithoutReferer(t *testing.T) {
	const secretURL = "https://secret-tunnel.trycloudflare.com/runs/secret-run/healthz"
	client := newDiagnosticHealthClient(diagnosticDNSResolverAddress, nil)
	var requests []*http.Request
	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Clone(request.Context()))
		if len(requests) == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://collector.example/redirected"}},
				Body:       io.NopCloser(strings.NewReader("redirect")),
				Request:    request,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("followed")),
			Request:    request,
		}, nil
	})

	response, err := client.Get(secretURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusFound || len(requests) != 1 {
		t.Fatalf("managed redirect status/requests = %d/%d, want original 302 and one request", response.StatusCode, len(requests))
	}
	if referer := requests[0].Referer(); referer != "" {
		t.Fatalf("managed health request Referer = %q, want empty", referer)
	}
}

func TestDiagnosticHealthClientWaitsForPublishedARecord(t *testing.T) {
	dnsServer := startTestDNSServer(t, testDNSBehavior{expectedName: "diagnostic.example.", publishAOn: 2, aRecord: [4]byte{93, 184, 216, 34}})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema":"v1","run_id":"run-123"}`))
	}))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ipv6, err := lookupTestDNSAAAA(ctx, dnsServer.address, "diagnostic.example.")
	if err != nil || len(ipv6) != 1 || ipv6[0] != netip.IPv6Loopback() {
		t.Fatalf("initial AAAA lookup = %v, %v; want published ::1", ipv6, err)
	}
	client := newDiagnosticHealthClient(dnsServer.address, func(ctx context.Context, network, address string) (net.Conn, error) {
		_, dialPort, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, splitErr
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", dialPort))
	})
	retryWaits := 0
	if err := fetchRunHealth(
		ctx,
		client,
		"http://diagnostic.example.:"+port+"/runs/run-123/healthz",
		"run-123",
		time.Millisecond,
		true,
		func(ctx context.Context, _ time.Duration) error {
			retryWaits++
			return ctx.Err()
		},
	); err != nil {
		t.Fatal(err)
	}
	if retryWaits != 1 {
		t.Fatalf("health retry waits = %d, want one injected publication wait", retryWaits)
	}
	events := dnsServer.Events()
	aaaaIndex := slices.Index(events, "AAAA-published")
	unpublishedAIndex := slices.Index(events, "A-unpublished")
	publishedAIndex := slices.Index(events, "A-published")
	if aaaaIndex < 0 || unpublishedAIndex < 0 || publishedAIndex < 0 || aaaaIndex > publishedAIndex || unpublishedAIndex > publishedAIndex {
		t.Fatalf("DNS events = %v, want AAAA and unpublished A before published A", events)
	}
}

func TestDiagnosticHealthClientPreservesOriginalTLSHostWhenPinningIPv4(t *testing.T) {
	sni := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		sni <- request.TLS.ServerName
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	certificate := server.Certificate()
	if len(certificate.DNSNames) == 0 {
		t.Fatal("httptest certificate has no DNS name")
	}
	host := certificate.DNSNames[0]
	dnsServer := startTestDNSServer(t, testDNSBehavior{expectedName: host + ".", publishAOn: 1, aRecord: [4]byte{93, 184, 216, 34}})

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	dialed := make(chan string, 1)
	client := newDiagnosticHealthClient(dnsServer.address, func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed <- address
		_, dialPort, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, splitErr
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", dialPort))
	})
	transport := client.Transport.(*http.Transport)
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(certificate)
	transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs, MinVersion: tls.VersionTLS12}
	request, err := http.NewRequest(http.MethodGet, "https://"+net.JoinHostPort(host, port)+"/healthz", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("diagnostic TLS response status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	if got := <-dialed; got != net.JoinHostPort("93.184.216.34", port) {
		t.Fatalf("diagnostic TLS dial address = %q, want pinned public IPv4", got)
	}
	if got := <-sni; got != host {
		t.Fatalf("diagnostic TLS SNI = %q, want original host %q", got, host)
	}
}

func TestRunnerSelectsHealthClientByOriginOwnership(t *testing.T) {
	healthResponse := func(request *http.Request) (*http.Response, error) {
		runID := strings.Split(strings.Trim(request.URL.Path, "/"), "/")[1]
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"` + runID + `"}`)),
			Request:    request,
		}, nil
	}
	launchMustNotRun := func(context.Context, string, string, bool) error {
		return errors.New("browser launch must not run before lifecycle validation")
	}
	versions := func(context.Context, string) (Versions, error) {
		return Versions{}, nil
	}

	t.Run("managed tunnel", func(t *testing.T) {
		var operatorCalls, tunnelCalls int
		var managedTunnel bool
		lifecycleErr := errors.New("stop after managed lifecycle ownership at https://managed.trycloudflare.com")
		runner := Runner{Dependencies: Dependencies{
			Tunnel: &fakeTunnel{origin: "https://managed.trycloudflare.com"},
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				operatorCalls++
				return nil, errors.New("operator health client must not serve a managed tunnel")
			})},
			TunnelHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				tunnelCalls++
				return healthResponse(request)
			})},
			LaunchDirect:   launchMustNotRun,
			LaunchAttached: launchMustNotRun,
			ValidateLifecycle: func(_ context.Context, _, _ string, got bool) error {
				managedTunnel = got
				return lifecycleErr
			},
			InspectVersions: versions,
		}}
		err := runner.Run(context.Background(), Options{Image: "candidate:test", Output: filepath.Join(t.TempDir(), "conformance.json")})
		if !errors.Is(err, lifecycleErr) {
			t.Fatalf("Run error = %v, want lifecycle ownership sentinel", err)
		}
		if operatorCalls != 0 || tunnelCalls != 1 {
			t.Fatalf("health calls = operator:%d tunnel:%d, want 0/1", operatorCalls, tunnelCalls)
		}
		if !managedTunnel {
			t.Fatal("managed tunnel ownership was not propagated to candidate lifecycle")
		}
		if strings.Contains(err.Error(), "managed.trycloudflare.com") {
			t.Fatalf("managed lifecycle error leaked tunnel origin: %v", err)
		}
	})

	t.Run("operator origin", func(t *testing.T) {
		var operatorCalls, tunnelCalls int
		managedTunnel := true
		lifecycleErr := errors.New("stop after operator lifecycle ownership")
		runner := Runner{Dependencies: Dependencies{
			HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				operatorCalls++
				return healthResponse(request)
			})},
			TunnelHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				tunnelCalls++
				return nil, errors.New("tunnel health client must not serve an operator origin")
			})},
			LaunchDirect:   launchMustNotRun,
			LaunchAttached: launchMustNotRun,
			ValidateLifecycle: func(_ context.Context, _, _ string, got bool) error {
				managedTunnel = got
				return lifecycleErr
			},
			InspectVersions: versions,
		}}
		err := runner.Run(context.Background(), Options{
			Image:  "candidate:test",
			Output: filepath.Join(t.TempDir(), "conformance.json"),
			Origin: "https://operator.trycloudflare.com",
		})
		if !errors.Is(err, lifecycleErr) {
			t.Fatalf("Run error = %v, want lifecycle ownership sentinel", err)
		}
		if operatorCalls != 1 || tunnelCalls != 0 {
			t.Fatalf("health calls = operator:%d tunnel:%d, want 1/0", operatorCalls, tunnelCalls)
		}
		if managedTunnel {
			t.Fatal("explicit Quick Tunnel origin was marked as managed")
		}
	})
}

func TestRunnerRedactsManagedDirectLaunchFailure(t *testing.T) {
	const origin = "https://managed.trycloudflare.com"
	var launchErr error
	healthClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		runID := strings.Split(strings.Trim(request.URL.Path, "/"), "/")[1]
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"` + runID + `"}`)),
			Request:    request,
		}, nil
	})}
	runner := Runner{Dependencies: Dependencies{
		Tunnel:           &fakeTunnel{origin: origin},
		HTTPClient:       healthClient,
		TunnelHTTPClient: healthClient,
		LaunchDirect: func(_ context.Context, _, target string, _ bool) error {
			launchErr = errors.New("chrome failed to navigate to " + target)
			return launchErr
		},
		LaunchAttached: func(context.Context, string, string, bool) error {
			t.Fatal("attached launch ran after direct failure")
			return nil
		},
		ValidateLifecycle: func(context.Context, string, string, bool) error { return nil },
		InspectVersions:   func(context.Context, string) (Versions, error) { return Versions{}, nil },
	}}
	err := runner.Run(context.Background(), Options{Image: "candidate:test", Output: filepath.Join(t.TempDir(), "conformance.json")})
	if !errors.Is(err, launchErr) {
		t.Fatalf("Run error = %v, want direct launch cause", err)
	}
	if strings.Contains(err.Error(), "managed.trycloudflare.com") || strings.Contains(err.Error(), "/runs/") {
		t.Fatalf("managed direct error leaked tunnel target: %v", err)
	}
}

func TestRedactManagedTunnelErrorMatchesHostnameCaseInsensitively(t *testing.T) {
	cause := errors.New("launch HTTPS://SECRET-TUNNEL.TRYCLOUDFLARE.COM/runs/secret-run/direct failed")
	err := redactManagedTunnelError(cause, true, "https://secret-tunnel.trycloudflare.com")
	if !errors.Is(err, cause) {
		t.Fatalf("redacted managed error = %v, want original cause", err)
	}
	for _, forbidden := range []string{"secret-tunnel", "trycloudflare.com"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Fatalf("redacted managed error leaked case-variant %q: %v", forbidden, err)
		}
	}
}

func TestRedactManagedTunnelErrorRedactsPathAndRunIdentifier(t *testing.T) {
	const target = "https://secret-tunnel.trycloudflare.com/runs/secret-run-42/direct"
	for _, message := range []string{
		"launch failed at /RUNS/SECRET-RUN-42/DIRECT",
		"launch failed for SECRET-RUN-42",
	} {
		cause := errors.New(message)
		err := redactManagedTunnelError(cause, true, target)
		if !errors.Is(err, cause) {
			t.Fatalf("redacted managed error = %v, want original cause", err)
		}
		for _, forbidden := range []string{"secret-run-42", "/runs/"} {
			if strings.Contains(strings.ToLower(err.Error()), forbidden) {
				t.Fatalf("redacted managed error leaked %q: %v", forbidden, err)
			}
		}
	}
}

func TestRedactManagedTunnelErrorFailsClosedForInvalidPatternText(t *testing.T) {
	secret := string([]byte{0xff})
	cause := errors.New("launch failed for " + secret)
	err := redactManagedTunnelError(cause, true, secret)
	if !errors.Is(err, cause) {
		t.Fatalf("redacted managed error = %v, want original cause", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("redacted managed error retained invalid secret bytes: %q", err.Error())
	}
}

func TestRunnerRejectsManagedTunnelWithoutDedicatedHealthClient(t *testing.T) {
	tunnel := &sequenceTunnel{origins: []string{"https://managed.trycloudflare.com"}}
	runner := Runner{Dependencies: Dependencies{
		Tunnel: tunnel,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			runID := strings.Split(strings.Trim(request.URL.Path, "/"), "/")[1]
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"` + runID + `"}`)),
				Request:    request,
			}, nil
		})},
	}}
	err := runner.Run(context.Background(), Options{Image: "candidate:test", Output: filepath.Join(t.TempDir(), "conformance.json")})
	if err == nil || !strings.Contains(err.Error(), "managed diagnostic tunnel health client is unavailable") {
		t.Fatalf("Run error = %v, want missing managed health client rejection", err)
	}
	if tunnel.startCalls != 0 {
		t.Fatalf("tunnel start calls = %d, want rejection before activation", tunnel.startCalls)
	}
}

func TestDefaultDependenciesConfigureDedicatedManagedHealthClient(t *testing.T) {
	managedClient := &http.Client{}
	var resolverAddress string
	dependencies := defaultDependencies(io.Discard, func(address string, dial diagnosticHealthDialer) *http.Client {
		resolverAddress = address
		if dial != nil {
			t.Fatal("default dependencies injected a final origin dialer")
		}
		return managedClient
	})
	if dependencies.HTTPClient == nil || dependencies.TunnelHTTPClient == nil || dependencies.HTTPClient == dependencies.TunnelHTTPClient {
		t.Fatalf("default health clients = operator:%p managed:%p, want distinct configured clients", dependencies.HTTPClient, dependencies.TunnelHTTPClient)
	}
	if dependencies.TunnelHTTPClient != managedClient || resolverAddress != "1.1.1.1:53" {
		t.Fatalf("managed health client/address = %p/%q, want injected client and 1.1.1.1:53", dependencies.TunnelHTTPClient, resolverAddress)
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

func TestRunnerRetriesTransientQuickTunnelHealthAndCleansEachAttempt(t *testing.T) {
	for name, firstStatus := range map[string]int{
		"service unavailable": http.StatusServiceUnavailable,
		"origin DNS failure":  cloudflareOriginDNSFailureStatusCode,
	} {
		t.Run(name, func(t *testing.T) {
			tunnel := &sequenceTunnel{origins: []string{"https://first.invalid", "https://second.test"}}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "first.invalid" {
					return &http.Response{
						StatusCode: firstStatus,
						Header:     make(http.Header),
						Body:       http.NoBody,
						Request:    request,
					}, nil
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
				TunnelHTTPClient:    client,
				TunnelHealthTimeout: time.Second,
				HealthRetryWait: func(context.Context, time.Duration) error {
					return context.DeadlineExceeded
				},
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
		})
	}
}

func TestRunnerRejectsNonPublicManagedDNSWithoutRetryingOrRotating(t *testing.T) {
	tunnel := &sequenceTunnel{origins: []string{"https://private-answer.test"}}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errDiagnosticNonPublicIPv4
	})}
	retryWaits := 0
	runner := Runner{Dependencies: Dependencies{
		Tunnel:           tunnel,
		HTTPClient:       client,
		TunnelHTTPClient: client,
		HealthRetryWait: func(context.Context, time.Duration) error {
			retryWaits++
			return errors.New("unexpected health retry")
		},
	}}
	err := runner.Run(context.Background(), Options{
		Image:  "candidate:test",
		Output: filepath.Join(t.TempDir(), "conformance.json"),
	})
	if !errors.Is(err, errDiagnosticNonPublicIPv4) {
		t.Fatalf("Run error = %v, want non-public IPv4 rejection", err)
	}
	if errors.Is(err, errDiagnosticHealthEndpointUnreachable) {
		t.Fatalf("Run error = %v, do not want generic endpoint-unreachable classification", err)
	}
	if retryWaits != 0 {
		t.Fatalf("managed health retry waits = %d, want 0", retryWaits)
	}
	if tunnel.startCalls != 1 || tunnel.stopCalls != 1 {
		t.Fatalf("tunnel calls = start:%d stop:%d, want fail-closed cleanup at 1/1", tunnel.startCalls, tunnel.stopCalls)
	}
}

func TestRunnerRetriesTransientQuickTunnelStartFailure(t *testing.T) {
	tunnel := &sequenceTunnel{
		origins: []string{"", "https://second.test"},
		startErrors: []error{
			&net.DNSError{Err: "temporary failure", IsTemporary: true},
			nil,
		},
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
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
		TunnelHTTPClient:    client,
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

func TestRunnerRedactsFailedTunnelStartCleanup(t *testing.T) {
	cleanupErr := errors.New("cleanup failed for https://secret-tunnel.trycloudflare.com")
	tunnel := &sequenceTunnel{
		origins: []string{"", "https://second.test"},
		startErrors: []error{
			&net.DNSError{Err: "temporary failure", IsTemporary: true},
			nil,
		},
		stopErrors: []error{cleanupErr},
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		runID := strings.Split(strings.Trim(request.URL.Path, "/"), "/")[1]
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"` + runID + `"}`)),
			Request:    request,
		}, nil
	})}
	runner := Runner{Dependencies: Dependencies{Tunnel: tunnel, HTTPClient: client, TunnelHTTPClient: client}}
	err := runner.Run(context.Background(), Options{
		Image:  "candidate:test",
		Output: filepath.Join(t.TempDir(), "conformance.json"),
	})
	if !errors.Is(err, errDiagnosticTunnelCleanupFailed) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Run error = %v, want fixed redacted cleanup classification and preserved cause", err)
	}
	if tunnel.startCalls != 1 || tunnel.stopCalls != 1 {
		t.Fatalf("tunnel calls = start:%d stop:%d, want retry aborted at 1/1", tunnel.startCalls, tunnel.stopCalls)
	}
	if strings.Contains(err.Error(), "secret-tunnel") || strings.Contains(err.Error(), "trycloudflare.com") {
		t.Fatalf("Run cleanup error leaked tunnel origin: %v", err)
	}
}

func TestFetchRunHealthRedactsEndpointOnFailure(t *testing.T) {
	const secretURL = "https://secret-tunnel.trycloudflare.com/runs/secret-run/healthz"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		return nil, &url.Error{
			Op:  request.Method,
			URL: secretURL,
			Err: &net.DNSError{Err: "no such host", Name: request.URL.Hostname(), IsNotFound: true},
		}
	})}
	err := fetchRunHealth(ctx, client, secretURL, "secret-run", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if err == nil {
		t.Fatal("health check unexpectedly succeeded")
	}
	for _, forbidden := range []string{"secret-tunnel", "secret-run", "trycloudflare.com"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("health error leaked %q: %v", forbidden, err)
		}
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("managed health error lost DNS cause: %v", err)
	}
}

func TestFetchRunHealthPreservesManagedTransportCause(t *testing.T) {
	const secretURL = "https://secret-tunnel.trycloudflare.com/runs/secret-run/healthz"
	cause := errors.New("TLS failure for " + secretURL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, cause
	})}
	err := fetchRunHealth(ctx, client, secretURL, "secret-run", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if !errors.Is(err, cause) || !errors.Is(err, errDiagnosticHealthEndpointUnreachable) {
		t.Fatalf("managed health error = %v, want redacted classification and original cause", err)
	}
	for _, forbidden := range []string{"secret-tunnel", "secret-run", "trycloudflare.com"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("managed transport error leaked %q: %v", forbidden, err)
		}
	}
}

func TestFetchRunHealthPreservesRedactedManagedResponseCauses(t *testing.T) {
	const secretURL = "https://secret-tunnel.trycloudflare.com/runs/secret-run/healthz"
	readErr := errors.New("read failed for " + strings.ToUpper(secretURL))
	closeErr := errors.New("close failed for " + strings.ToUpper(secretURL))
	body := &trackingReadCloser{
		Reader:   readFunc(func([]byte) (int, error) { return 0, readErr }),
		closeErr: closeErr,
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})}
	err := fetchRunHealth(context.Background(), client, secretURL, "secret-run", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if !errors.Is(err, errDiagnosticHealthEndpointRejected) || !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("managed response error = %v, want fixed classification plus read and close causes", err)
	}
	for _, forbidden := range []string{"secret-tunnel", "secret-run", "trycloudflare.com"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Fatalf("managed response error leaked %q: %v", forbidden, err)
		}
	}
}

func TestFetchRunHealthRejectsTrailingContent(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"schema":"v1","run_id":"run-123"}garbage`)),
			Request:    request,
		}, nil
	})}
	err := fetchRunHealth(context.Background(), client, "https://diagnostic.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if !errors.Is(err, errDiagnosticHealthEndpointRejected) {
		t.Fatalf("managed health error = %v, want trailing-content rejection", err)
	}
}

func TestFetchRunHealthRejectsOversizedSuccessBody(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader(
		`{"schema":"v1","run_id":"run-123"}` + strings.Repeat(" ", 4096),
	)}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})}
	err := fetchRunHealth(context.Background(), client, "https://diagnostic.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if !errors.Is(err, errDiagnosticHealthEndpointRejected) {
		t.Fatalf("managed health error = %v, want oversized-body rejection", err)
	}
	if !body.closed {
		t.Fatal("oversized managed health response body remained open")
	}
}

func TestFetchRunHealthPreservesReadErrorAfterBoundedBody(t *testing.T) {
	readErr := errors.New("late bounded health response read failure")
	body := &trackingReadCloser{Reader: io.MultiReader(
		strings.NewReader(strings.Repeat("x", 4096)),
		readFunc(func([]byte) (int, error) { return 0, readErr }),
	)}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})}
	stopErr := errors.New("stop after response classification")
	err := fetchRunHealth(
		context.Background(),
		client,
		"https://diagnostic.example/runs/run-123/healthz",
		"run-123",
		time.Millisecond,
		true,
		func(context.Context, time.Duration) error { return stopErr },
	)
	if !errors.Is(err, readErr) || !errors.Is(err, stopErr) {
		t.Fatalf("managed health error = %v, want late read and retry-stop causes", err)
	}
	if !body.closed {
		t.Fatal("managed health response body remained open after late read failure")
	}
}

func TestFetchRunHealthPreservesRedactedManagedNonOKResponseCauses(t *testing.T) {
	const secretURL = "https://secret-tunnel.trycloudflare.com/runs/secret-run/healthz"
	for _, tc := range []struct {
		name           string
		status         int
		wantRetryWaits int
	}{
		{name: "nonretryable redirect", status: http.StatusFound},
		{name: "transient unavailable", status: http.StatusServiceUnavailable, wantRetryWaits: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readErr := errors.New("read failed for " + strings.ToUpper(secretURL))
			closeErr := errors.New("close failed for " + strings.ToUpper(secretURL))
			body := &trackingReadCloser{
				Reader:   readFunc(func([]byte) (int, error) { return 0, readErr }),
				closeErr: closeErr,
			}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.status,
					Header:     make(http.Header),
					Body:       body,
					Request:    request,
				}, nil
			})}
			retryWaits := 0
			stopErr := errors.New("stop after response classification")
			err := fetchRunHealth(
				context.Background(),
				client,
				secretURL,
				"secret-run",
				time.Millisecond,
				true,
				func(context.Context, time.Duration) error {
					retryWaits++
					return stopErr
				},
			)
			if !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
				t.Fatalf("managed status %d error = %v, want read and close causes", tc.status, err)
			}
			if retryWaits != tc.wantRetryWaits {
				t.Fatalf("managed status %d retry waits = %d, want %d", tc.status, retryWaits, tc.wantRetryWaits)
			}
			if tc.wantRetryWaits > 0 && !errors.Is(err, stopErr) {
				t.Fatalf("managed status %d error = %v, want retry stop cause", tc.status, err)
			}
			for _, forbidden := range []string{"secret-tunnel", "secret-run", "trycloudflare.com"} {
				if strings.Contains(strings.ToLower(err.Error()), forbidden) {
					t.Fatalf("managed status %d error leaked %q: %v", tc.status, forbidden, err)
				}
			}
		})
	}
}

func TestRunnerAbortsManagedTunnelOnResolverFailureWithoutRotating(t *testing.T) {
	dnsServer := startTestDNSServer(t, testDNSBehavior{expectedName: "secret-tunnel.trycloudflare.com.", rcode: dnsmessage.RCodeServerFailure})
	tunnel := &sequenceTunnel{origins: []string{
		"https://secret-tunnel.trycloudflare.com.",
		"https://must-not-start.trycloudflare.com.",
	}}
	runner := Runner{Dependencies: Dependencies{
		Tunnel:              tunnel,
		HTTPClient:          &http.Client{Timeout: time.Second},
		TunnelHTTPClient:    newDiagnosticHealthClient(dnsServer.address, nil),
		TunnelHealthTimeout: time.Second,
		HealthRetryWait: func(context.Context, time.Duration) error {
			return context.DeadlineExceeded
		},
	}}
	err := runner.Run(context.Background(), Options{Image: "candidate:test", Output: filepath.Join(t.TempDir(), "conformance.json")})
	if !errors.Is(err, errDiagnosticDNSResolverUnavailable) {
		t.Fatalf("Run error = %v, want resolver-unavailable classification", err)
	}
	if tunnel.startCalls != 1 || tunnel.stopCalls != 1 {
		t.Fatalf("tunnel calls = start:%d stop:%d, want resolver failure to abort after 1/1", tunnel.startCalls, tunnel.stopCalls)
	}
	if !slices.Contains(dnsServer.Events(), "SERVFAIL") {
		t.Fatalf("DNS events = %v, want production resolver SERVFAIL", dnsServer.Events())
	}
	for _, forbidden := range []string{"secret-tunnel", "must-not-start", "trycloudflare.com"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("Run error leaked %q: %v", forbidden, err)
		}
	}
}

func TestRunnerAbortsManagedTunnelOnPublicationExhaustionWithoutRotating(t *testing.T) {
	dnsServer := startTestDNSServer(t, testDNSBehavior{
		expectedName: "secret-tunnel.trycloudflare.com.",
		publishAOn:   100,
	})
	tunnel := &sequenceTunnel{origins: []string{
		"https://secret-tunnel.trycloudflare.com.",
		"https://secret-tunnel.trycloudflare.com.",
		"https://secret-tunnel.trycloudflare.com.",
	}}
	runner := Runner{Dependencies: Dependencies{
		Tunnel:              tunnel,
		HTTPClient:          &http.Client{Timeout: time.Second},
		TunnelHTTPClient:    newDiagnosticHealthClient(dnsServer.address, nil),
		TunnelHealthTimeout: time.Second,
		HealthRetryWait: func(context.Context, time.Duration) error {
			return context.DeadlineExceeded
		},
	}}
	err := runner.Run(context.Background(), Options{Image: "candidate:test", Output: filepath.Join(t.TempDir(), "conformance.json")})
	if !errors.Is(err, errDiagnosticDNSResolverUnavailable) || !errors.Is(err, errDiagnosticDNSPublicationNotReady) {
		t.Fatalf("Run error = %v, want resolver-unavailable exhaustion with publication cause", err)
	}
	if tunnel.startCalls != 1 || tunnel.stopCalls != 1 {
		t.Fatalf("tunnel calls = start:%d stop:%d, want publication exhaustion to abort after 1/1", tunnel.startCalls, tunnel.stopCalls)
	}
	if !slices.Contains(dnsServer.Events(), "A-unpublished") {
		t.Fatalf("DNS events = %v, want unpublished A result", dnsServer.Events())
	}
	for _, forbidden := range []string{"secret-tunnel", "trycloudflare.com"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("Run error leaked %q: %v", forbidden, err)
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

func TestCollectorRejectsMissingOrNullStableAutomationSignals(t *testing.T) {
	tests := map[string]func(map[string]any){
		"missing webdriver": func(signals map[string]any) {
			delete(signals["navigator"].(map[string]any), "webdriver")
		},
		"null webdriver": func(signals map[string]any) {
			signals["navigator"].(map[string]any)["webdriver"] = nil
		},
		"missing playwright binding": func(signals map[string]any) {
			delete(signals["automation"].(map[string]any), "playwright_binding_present")
		},
		"null playwright init scripts": func(signals map[string]any) {
			signals["automation"].(map[string]any)["playwright_init_scripts_present"] = nil
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			collector := NewCollector("run-123")
			handler := collector.Handler()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "https://diagnostic.example/runs/run-123/direct", http.NoBody))
			var payload map[string]any
			if err := json.Unmarshal([]byte(providerPayload(matchingObservation("direct").Browser)), &payload); err != nil {
				t.Fatal(err)
			}
			mutate(payload["browser_signals"].(map[string]any))
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "https://diagnostic.example/runs/run-123/direct", bytes.NewReader(body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
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
	for _, want := range []string{"navigator.webdriver", "navigator.userAgentData", "screen.width", "screen.height", "__playwright__binding__", "__pwInitScripts", "fetch(location.href"} {
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

func TestMainWritesRedactedReportAndReturnsBoundedConformanceFailure(t *testing.T) {
	client := &http.Client{}
	tunnel := &proxyTunnel{client: client}
	var targetMu sync.Mutex
	var observedTargets []string
	launch := func(kind string, mismatch bool) func(context.Context, string, string, bool) error {
		return func(ctx context.Context, image, target string, _ bool) error {
			targetMu.Lock()
			observedTargets = append(observedTargets, target)
			targetMu.Unlock()
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
	dependencies := Dependencies{
		Tunnel:           tunnel,
		HTTPClient:       client,
		TunnelHTTPClient: client,
		LaunchDirect:     launch("direct", false),
		LaunchAttached:   launch("attached", true),
		ValidateLifecycle: func(context.Context, string, string, bool) error {
			lifecycleCalls++
			return nil
		},
		InspectVersions: func(context.Context, string) (Versions, error) {
			return Versions{ImageID: "sha256:image", Chrome: "Google Chrome 140", Playwright: "1.57.0", Xvfb: "1.20.14"}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	code := MainContext(context.Background(), []string{"--image", "candidate:test", "--output", output}, &stdout, &stderr, dependencies)
	if code != 1 || !strings.Contains(stderr.String(), "conformance failed") {
		t.Fatalf("Main code/stdout/stderr = %d/%q/%q, want conformance failure", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "browser.navigator.user_agent") {
		t.Fatalf("Main stderr = %q, want mismatched stable field", stderr.String())
	}
	for _, observedValue := range []string{
		"Mozilla/5.0 Chrome/140", "mismatched", "Google Chrome", "Chromium", "Linux", "en-US", "Win32", "diagnostic.example",
	} {
		if strings.Contains(stderr.String(), observedValue) {
			t.Fatalf("Main stderr exposes observed value %q: %q", observedValue, stderr.String())
		}
	}
	targetMu.Lock()
	targets := append([]string(nil), observedTargets...)
	targetMu.Unlock()
	if len(targets) == 0 || tunnel.server == nil {
		t.Fatalf("missing observed diagnostic targets: %v", targets)
	}
	parsedTarget, parseErr := url.Parse(targets[0])
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	pathParts := strings.Split(strings.Trim(parsedTarget.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[0] != "runs" {
		t.Fatalf("diagnostic target path = %q, want run correlation", parsedTarget.Path)
	}
	correlatedValues := []string{tunnel.server.URL, parsedTarget.Host, pathParts[1]}
	for _, correlatedValue := range correlatedValues {
		if strings.Contains(stderr.String(), correlatedValue) {
			t.Fatalf("Main stderr exposes correlated value %q: %q", correlatedValue, stderr.String())
		}
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
	for _, correlatedValue := range correlatedValues {
		if strings.Contains(string(data), correlatedValue) {
			t.Fatalf("report contains correlated value %q: %s", correlatedValue, data)
		}
	}
}

func TestLaunchAndCollectWaitsForLauncherCleanupAfterCancellation(t *testing.T) {
	collector := NewCollector("run-cancel")
	cleanupComplete := make(chan struct{})
	launch := func(ctx context.Context, _, _ string, _ bool) error {
		<-ctx.Done()
		time.Sleep(25 * time.Millisecond)
		close(cleanupComplete)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := launchAndCollect(ctx, collector, "direct", "candidate:test", "https://diagnostic.example/direct", false, launch)
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

func TestLaunchCleanupBudgetCoversLifecycleCleanupBound(t *testing.T) {
	if launchCleanupWaitTimeout <= candidateLifecycleCleanupBound {
		t.Fatalf("launch cleanup timeout %s must exceed lifecycle cleanup bound %s", launchCleanupWaitTimeout, candidateLifecycleCleanupBound)
	}
}

func TestWaitForLaunchCleanupWithinSupportsDelayedCompletion(t *testing.T) {
	result := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		result <- nil
	}()
	canceled := false
	err := waitForLaunchCleanupWithin("direct", func() { canceled = true }, result, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForLaunchCleanupWithin: %v", err)
	}
	if !canceled {
		t.Fatal("launch context was not canceled before cleanup wait")
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
	got := directChromeContainerArgs("candidate:test", "https://diagnostic.example/runs/run/direct", "/tmp/profile", false, "")
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
	for _, forbidden := range []string{"--dns", "--headless", "remote-debugging", "playwright", "product-capture-provider", "AutomationControlled"} {
		if strings.Contains(strings.ToLower(joined), strings.ToLower(forbidden)) {
			t.Errorf("direct args contain forbidden %q: %s", forbidden, joined)
		}
	}
}

func TestDirectChromeContainerArgsUseBoundedXvfbSocketReadiness(t *testing.T) {
	joined := strings.Join(directChromeContainerArgs("candidate:test", "https://diagnostic.example/direct", "/tmp/profile", false, ""), " ")
	for _, required := range []string{
		"--entrypoint /bin/sh",
		"Xvfb :99",
		"/tmp/.X11-unix/X99",
		"PRODUCT_CAPTURE_XVFB_READY_TIMEOUT",
		"google-chrome",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("direct headed args missing %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, "xvfb-run") {
		t.Fatalf("direct headed args retain xvfb-run signal handshake: %s", joined)
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

func TestHeadedContainerWrapperAvoidsDelayedNumericPIDWatchdog(t *testing.T) {
	for _, required := range []string{
		"child_process_state()",
		`/proc/$pid/stat`,
		`[ "$state" = Z ]`,
		`attempts=$((timeout * 20))`,
		`kill -KILL "$child_pid"`,
		`wait "$child_pid"`,
	} {
		if !strings.Contains(headedContainerScript, required) {
			t.Errorf("headed cleanup missing checked child wait behavior %q", required)
		}
	}
	for _, forbidden := range []string{"watchdog_pid", `sleep "${PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT:-10}"`} {
		if strings.Contains(headedContainerScript, forbidden) {
			t.Errorf("headed cleanup retains delayed numeric PID watchdog %q", forbidden)
		}
	}
}

func TestAttachedContainerArgsRunRealProviderDiagnostic(t *testing.T) {
	got := attachedProviderContainerArgs("candidate:test", "https://diagnostic.example/runs/run/attached", false)
	want := []string{
		"run", "--rm", "--platform", "linux/amd64",
		"-e", "PRODUCT_CAPTURE_BROWSER_HEADLESS=false",
		"-e", "PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS=https://diagnostic.example",
		"--entrypoint", "/usr/local/bin/product-capture-provider", "candidate:test",
		"--browser-diagnostic-url", "https://diagnostic.example/runs/run/attached",
	}
	if !reflect.DeepEqual(stripContainerName(got), want) {
		t.Fatalf("attached args = %#v, want %#v", stripContainerName(got), want)
	}
	joined := strings.Join(got, " ")
	for _, forbidden := range []string{"DISPLAY=", "Xvfb :99", headedContainerScript} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("attached provider must own its headed display; args contain %q", forbidden)
		}
	}
}

func TestQuickTunnelContainerArgsUseAdjacentCloudflareDNS(t *testing.T) {
	target := "https://managed.trycloudflare.com/runs/run/observation"
	directResolverRule := "MAP managed.trycloudflare.com 104.16.133.229"
	tests := map[string][]string{
		"direct":   stripContainerName(directChromeContainerArgs("candidate:test", target, "/tmp/profile", true, directResolverRule)),
		"attached": stripContainerName(attachedProviderContainerArgs("candidate:test", target, true)),
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			index := slices.Index(args, "--dns")
			if index < 0 || index+1 >= len(args) || args[index+1] != diagnosticDNSResolverIP {
				t.Fatalf("container args = %#v, want adjacent --dns %s", args, diagnosticDNSResolverIP)
			}
		})
	}
	attached := tests["attached"]
	if !slices.Contains(attached, "--browser-diagnostic-require-ipv4") {
		t.Fatalf("attached args = %#v, want managed IPv4 policy flag", attached)
	}
	if !slices.Contains(tests["direct"], "--host-resolver-rules="+directResolverRule) {
		t.Fatalf("direct args = %#v, want managed public IPv4 resolver pin", tests["direct"])
	}

	for _, target := range []string{
		"https://trycloudflare.com.evil.example/runs/run/direct",
		"https://operator.example/runs/run/direct",
	} {
		if args := quickTunnelDNSArgs(target, true); len(args) != 0 {
			t.Fatalf("quickTunnelDNSArgs(%q) = %#v, want no resolver override", target, args)
		}
	}
	if args := quickTunnelDNSArgs(target, false); len(args) != 0 {
		t.Fatalf("explicit quickTunnelDNSArgs(%q) = %#v, want operator resolver", target, args)
	}
}

func TestManagedDirectChromeResolverRuleWaitsForPublishedPublicIPv4(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		started := time.Now()
		rule, err := resolveManagedDirectChromeResolverRule(
			context.Background(),
			"https://managed.trycloudflare.com/runs/run/direct",
			func(context.Context, string) ([]netip.Addr, error) {
				calls++
				if calls == 1 {
					return nil, &net.DNSError{Err: "no such host", Name: "managed.trycloudflare.com", IsNotFound: true}
				}
				return []netip.Addr{netip.MustParseAddr("104.16.133.229")}, nil
			},
			50*time.Millisecond,
			time.Millisecond,
		)
		if err != nil {
			t.Fatalf("resolve managed direct Chrome rule: %v", err)
		}
		if calls != 2 {
			t.Fatalf("managed direct DNS calls = %d, want retry after unpublished A record", calls)
		}
		if rule != "MAP managed.trycloudflare.com 104.16.133.229" {
			t.Fatalf("managed direct resolver rule = %q", rule)
		}
		if elapsed := time.Since(started); elapsed != time.Millisecond {
			t.Fatalf("managed direct DNS retry elapsed = %s, want %s", elapsed, time.Millisecond)
		}
	})
}

func TestManagedDirectChromeResolverRuleRejectsSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := resolveManagedDirectChromeResolverRule(
		ctx,
		"https://managed.trycloudflare.com/runs/run/direct",
		func(context.Context, string) ([]netip.Addr, error) {
			cancel()
			return []netip.Addr{netip.MustParseAddr("104.16.133.229")}, nil
		},
		50*time.Millisecond,
		time.Millisecond,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("managed direct resolver error = %v, want cancellation after lookup", err)
	}
}

func TestResolvePublicDiagnosticIPv4RejectsLookupSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := resolvePublicDiagnosticIPv4(ctx, "diagnostic.example", func(context.Context, string) ([]netip.Addr, error) {
		cancel()
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("diagnostic IPv4 resolver error = %v, want canceled lookup rejection", err)
	}
}

func TestFetchRunHealthRejectsCorrelatedSuccessWhenCanceledDuringBodyRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := &trackingReadCloser{
		Reader: strings.NewReader(`{"schema":"v1","run_id":"run-123"}`),
		onRead: cancel,
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})}
	err := fetchRunHealth(ctx, client, "https://diagnostic.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("diagnostic health error = %v, want canceled success rejection", err)
	}
	if !body.closed {
		t.Fatal("diagnostic health response body remained open after cancellation")
	}
}

func TestFetchRunHealthRedactsCloseErrorWhenCanceledBeforeBodyRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closeErr := errors.New("close failed for HTTPS://SECRET-TUNNEL.TRYCLOUDFLARE.COM/runs/secret-run/healthz")
	body := &trackingReadCloser{
		Reader:   strings.NewReader(`{"schema":"v1","run_id":"run-123"}`),
		closeErr: closeErr,
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})}
	err := fetchRunHealth(ctx, client, "https://diagnostic.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errDiagnosticHealthEndpointRejected) || !errors.Is(err, closeErr) {
		t.Fatalf("diagnostic health error = %v, want cancellation, fixed classification, and close cause", err)
	}
	for _, forbidden := range []string{"secret-tunnel", "secret-run", "trycloudflare.com"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Fatalf("pre-read cancellation error leaked %q: %v", forbidden, err)
		}
	}
	if !body.closed {
		t.Fatal("diagnostic health response body remained open after pre-read cancellation")
	}
}

func TestFetchRunHealthPreservesBodyErrorWhenCanceledDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readErr := errors.New("health body read failed for HTTPS://SECRET-TUNNEL.TRYCLOUDFLARE.COM/runs/secret-run/healthz")
	body := &trackingReadCloser{Reader: readFunc(func([]byte) (int, error) {
		cancel()
		return 0, readErr
	})}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    request,
		}, nil
	})}
	err := fetchRunHealth(ctx, client, "https://diagnostic.example/runs/run-123/healthz", "run-123", time.Millisecond, true, waitForDiagnosticHealthRetry)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errDiagnosticHealthEndpointRejected) || !errors.Is(err, readErr) {
		t.Fatalf("diagnostic health error = %v, want cancellation, fixed classification, and body read cause", err)
	}
	for _, forbidden := range []string{"secret-tunnel", "secret-run", "trycloudflare.com"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Fatalf("canceled managed health error leaked %q: %v", forbidden, err)
		}
	}
	if !body.closed {
		t.Fatal("diagnostic health response body remained open after read failure")
	}
}

type testDNSBehavior struct {
	expectedName      string
	publishAOn        int32
	rcode             dnsmessage.RCode
	aRecord           [4]byte
	responseIDDelta   uint16
	responseName      string
	malformedResponse bool
	aRecordName       string
	cnameTarget       string
	omitA             bool
	trailingResponse  []byte
}

type testDNSServer struct {
	address string

	aQueries atomic.Int32
	mu       sync.Mutex
	events   []string
}

func (s *testDNSServer) record(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *testDNSServer) Events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func lookupTestDNSAAAA(ctx context.Context, resolverAddress, host string) ([]netip.Addr, error) {
	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return nil, err
	}
	query, err := (&dnsmessage.Message{
		Header: dnsmessage.Header{ID: 0xa11a, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name:  name,
			Type:  dnsmessage.TypeAAAA,
			Class: dnsmessage.ClassINET,
		}},
	}).Pack()
	if err != nil {
		return nil, err
	}
	response, err := exchangeDiagnosticDNS(ctx, "udp", resolverAddress, query)
	if err != nil {
		return nil, err
	}
	var message dnsmessage.Message
	if err := message.Unpack(response); err != nil {
		return nil, err
	}
	addresses := make([]netip.Addr, 0, len(message.Answers))
	for _, answer := range message.Answers {
		if resource, ok := answer.Body.(*dnsmessage.AAAAResource); ok {
			addresses = append(addresses, netip.AddrFrom16(resource.AAAA))
		}
	}
	return addresses, nil
}

func startTestDNSServer(t *testing.T, behavior testDNSBehavior) *testDNSServer {
	t.Helper()
	connection, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &testDNSServer{address: connection.LocalAddr().String()}
	serveErr := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		buffer := make([]byte, 1232)
		for {
			count, remote, err := connection.ReadFrom(buffer)
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				serveErr <- err
				return
			}
			var parser dnsmessage.Parser
			header, err := parser.Start(buffer[:count])
			if err != nil {
				serveErr <- err
				return
			}
			question, err := parser.Question()
			if err != nil {
				serveErr <- err
				return
			}
			if got := question.Name.String(); got != behavior.expectedName {
				serveErr <- fmt.Errorf("DNS question name = %q, want %q", got, behavior.expectedName)
				return
			}
			if behavior.malformedResponse {
				if _, err := connection.WriteTo([]byte{0, 1, 2}, remote); err != nil && !errors.Is(err, net.ErrClosed) {
					serveErr <- err
					return
				}
				continue
			}
			responseQuestion := question
			if behavior.responseName != "" {
				responseName, err := dnsmessage.NewName(behavior.responseName)
				if err != nil {
					serveErr <- err
					return
				}
				responseQuestion.Name = responseName
			}
			builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
				ID:                 header.ID + behavior.responseIDDelta,
				Response:           true,
				Authoritative:      true,
				RecursionDesired:   header.RecursionDesired,
				RecursionAvailable: true,
				RCode:              behavior.rcode,
			})
			builder.EnableCompression()
			if err := builder.StartQuestions(); err != nil {
				serveErr <- err
				return
			}
			if err := builder.Question(responseQuestion); err != nil {
				serveErr <- err
				return
			}
			if err := builder.StartAnswers(); err != nil {
				serveErr <- err
				return
			}
			if behavior.rcode == dnsmessage.RCodeSuccess {
				resourceName := responseQuestion.Name
				if behavior.cnameTarget != "" {
					cnameTarget, err := dnsmessage.NewName(behavior.cnameTarget)
					if err != nil {
						serveErr <- err
						return
					}
					resourceHeader := dnsmessage.ResourceHeader{Name: responseQuestion.Name, Class: dnsmessage.ClassINET, TTL: 1}
					if err := builder.CNAMEResource(resourceHeader, dnsmessage.CNAMEResource{CNAME: cnameTarget}); err != nil {
						serveErr <- err
						return
					}
					resourceName = cnameTarget
				}
				if behavior.aRecordName != "" {
					aRecordName, err := dnsmessage.NewName(behavior.aRecordName)
					if err != nil {
						serveErr <- err
						return
					}
					resourceName = aRecordName
				}
				resourceHeader := dnsmessage.ResourceHeader{Name: resourceName, Class: dnsmessage.ClassINET, TTL: 1}
				switch question.Type {
				case dnsmessage.TypeA:
					if server.aQueries.Add(1) >= behavior.publishAOn {
						server.record("A-published")
						if behavior.omitA {
							break
						}
						aRecord := behavior.aRecord
						if aRecord == ([4]byte{}) {
							aRecord = [4]byte{127, 0, 0, 1}
						}
						if err := builder.AResource(resourceHeader, dnsmessage.AResource{A: aRecord}); err != nil {
							serveErr <- err
							return
						}
					} else {
						server.record("A-unpublished")
					}
				case dnsmessage.TypeAAAA:
					server.record("AAAA-published")
					if err := builder.AAAAResource(resourceHeader, dnsmessage.AAAAResource{AAAA: [16]byte{15: 1}}); err != nil {
						serveErr <- err
						return
					}
				}
			} else {
				server.record("SERVFAIL")
			}
			response, err := builder.Finish()
			if err != nil {
				serveErr <- err
				return
			}
			response = append(response, behavior.trailingResponse...)
			if _, err := connection.WriteTo(response, remote); err != nil && !errors.Is(err, net.ErrClosed) {
				serveErr <- err
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = connection.Close()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Error("test DNS server did not stop")
			return
		}
		select {
		case err := <-serveErr:
			t.Errorf("serve test DNS: %v", err)
		default:
		}
	})
	return server
}

type truncatedTestDNSServer struct {
	address string

	udpQueries  atomic.Int32
	tcpQueries  atomic.Int32
	tcpAccepted chan struct{}
	tcpMu       sync.Mutex
	tcpClosing  bool
	tcpActive   map[net.Conn]struct{}
}

type truncatedTestDNSOptions struct {
	truncateTCP bool
	trailingTCP []byte
}

type trackingPacketConn struct {
	net.PacketConn
	closed atomic.Bool
}

func (c *trackingPacketConn) Close() error {
	c.closed.Store(true)
	return c.PacketConn.Close()
}

func listenTruncatedTestDNS(
	listenPacket func(string, string) (net.PacketConn, error),
	listenTCP func(string, string) (net.Listener, error),
) (net.PacketConn, net.Listener, error) {
	const maxAttempts = 32
	var collisionErr error
	for range maxAttempts {
		udpConnection, err := listenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return nil, nil, err
		}
		tcpListener, err := listenTCP("tcp4", udpConnection.LocalAddr().String())
		if err == nil {
			return udpConnection, tcpListener, nil
		}
		if closeErr := udpConnection.Close(); closeErr != nil {
			return nil, nil, errors.Join(err, closeErr)
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, nil, err
		}
		collisionErr = err
	}
	return nil, nil, fmt.Errorf("reserve shared UDP/TCP test DNS port after %d attempts: %w", maxAttempts, collisionErr)
}

func startTruncatedTestDNSServer(t *testing.T, expectedName string, aRecord [4]byte, options ...truncatedTestDNSOptions) *truncatedTestDNSServer {
	t.Helper()
	var option truncatedTestDNSOptions
	if len(options) > 0 {
		option = options[0]
	}
	udpConnection, tcpListener, err := listenTruncatedTestDNS(net.ListenPacket, net.Listen)
	if err != nil {
		t.Fatal(err)
	}
	server := &truncatedTestDNSServer{
		address:     udpConnection.LocalAddr().String(),
		tcpAccepted: make(chan struct{}, 1),
		tcpActive:   make(map[net.Conn]struct{}),
	}
	serveErr := make(chan error, 2)
	var serveWG sync.WaitGroup
	reportErr := func(err error) {
		select {
		case serveErr <- err:
		default:
		}
	}
	buildResponse := func(query []byte, truncated bool) ([]byte, error) {
		var parser dnsmessage.Parser
		header, err := parser.Start(query)
		if err != nil {
			return nil, err
		}
		question, err := parser.Question()
		if err != nil {
			return nil, err
		}
		if got := question.Name.String(); got != expectedName {
			return nil, fmt.Errorf("DNS question name = %q, want %q", got, expectedName)
		}
		builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
			ID:                 header.ID,
			Response:           true,
			Authoritative:      true,
			Truncated:          truncated,
			RecursionDesired:   header.RecursionDesired,
			RecursionAvailable: true,
		})
		builder.EnableCompression()
		if err := builder.StartQuestions(); err != nil {
			return nil, err
		}
		if err := builder.Question(question); err != nil {
			return nil, err
		}
		if truncated {
			return builder.Finish()
		}
		if err := builder.StartAnswers(); err != nil {
			return nil, err
		}
		resourceHeader := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 1}
		if err := builder.AResource(resourceHeader, dnsmessage.AResource{A: aRecord}); err != nil {
			return nil, err
		}
		return builder.Finish()
	}

	serveWG.Add(2)
	go func() {
		defer serveWG.Done()
		buffer := make([]byte, 1232)
		for {
			count, remote, err := udpConnection.ReadFrom(buffer)
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				reportErr(err)
				return
			}
			server.udpQueries.Add(1)
			response, err := buildResponse(buffer[:count], true)
			if err != nil {
				reportErr(err)
				return
			}
			if _, err := udpConnection.WriteTo(response, remote); err != nil && !errors.Is(err, net.ErrClosed) {
				reportErr(err)
				return
			}
		}
	}()
	go func() {
		defer serveWG.Done()
		for {
			connection, err := tcpListener.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				reportErr(err)
				return
			}
			server.tcpMu.Lock()
			if server.tcpClosing {
				server.tcpMu.Unlock()
				_ = connection.Close()
				return
			}
			server.tcpActive[connection] = struct{}{}
			server.tcpMu.Unlock()

			err = func() error {
				defer func() {
					server.tcpMu.Lock()
					delete(server.tcpActive, connection)
					server.tcpMu.Unlock()
					_ = connection.Close()
				}()
				select {
				case server.tcpAccepted <- struct{}{}:
				default:
				}
				var size [2]byte
				if _, err := io.ReadFull(connection, size[:]); err != nil {
					return err
				}
				query := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(connection, query); err != nil {
					return err
				}
				server.tcpQueries.Add(1)
				response, err := buildResponse(query, option.truncateTCP)
				if err != nil {
					return err
				}
				response = append(response, option.trailingTCP...)
				frame := make([]byte, 2+len(response))
				binary.BigEndian.PutUint16(frame[:2], uint16(len(response)))
				copy(frame[2:], response)
				_, err = connection.Write(frame)
				return err
			}()
			if err != nil {
				server.tcpMu.Lock()
				closing := server.tcpClosing
				server.tcpMu.Unlock()
				if !closing {
					reportErr(err)
				}
				return
			}
		}
	}()
	t.Cleanup(func() {
		server.tcpMu.Lock()
		server.tcpClosing = true
		active := make([]net.Conn, 0, len(server.tcpActive))
		for connection := range server.tcpActive {
			active = append(active, connection)
		}
		server.tcpMu.Unlock()
		for _, connection := range active {
			_ = connection.Close()
		}
		_ = udpConnection.Close()
		_ = tcpListener.Close()
		done := make(chan struct{})
		go func() {
			serveWG.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("truncated test DNS server did not stop")
		}
		select {
		case err := <-serveErr:
			t.Errorf("serve truncated test DNS: %v", err)
		default:
		}
	})
	return server
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

func TestPinnedCloudflaredStartCleansTempDirAfterDownloadFailure(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	tunnel := &pinnedCloudflaredTunnel{client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("unavailable")),
			Request:    request,
		}, nil
	})}}
	if _, err := tunnel.Start(context.Background(), "http://127.0.0.1:8080"); err == nil {
		t.Fatal("Start succeeded after cloudflared download failure")
	}
	if tunnel.tempDir != "" {
		t.Fatalf("tunnel retained failed-start temp directory ownership: %s", tunnel.tempDir)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed cloudflared start left temp entries: %v", entries)
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

func TestScanTunnelOutputDrainsBeyondRetainedLogLimit(t *testing.T) {
	wantOrigin := "https://native-session-123.trycloudflare.com"
	line := strings.Repeat("x", 1024) + "\n"
	reader := bytes.NewBufferString(strings.Repeat(line, 140) + wantOrigin + "\n")
	var retained bytes.Buffer
	origins := make(chan string, 1)
	if err := scanTunnelOutput(reader, &retained, origins); err != nil {
		t.Fatalf("scanTunnelOutput: %v", err)
	}
	if reader.Len() != 0 {
		t.Fatalf("tunnel reader retained %d bytes", reader.Len())
	}
	if retained.Len() > tunnelRetainedLogLimit {
		t.Fatalf("retained tunnel log = %d bytes, limit %d", retained.Len(), tunnelRetainedLogLimit)
	}
	select {
	case origin := <-origins:
		if origin != wantOrigin {
			t.Fatalf("origin = %q, want %q", origin, wantOrigin)
		}
	default:
		t.Fatal("origin after retained log limit was not observed")
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
	containerPresent := true
	tunnel := &pinnedCloudflaredTunnel{
		cmd:           cmd,
		done:          done,
		containerName: "product-capture-cloudflared-test",
		tempDir:       tempDir,
		reapTimeout:   250 * time.Millisecond,
		docker: func(_ context.Context, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			if len(args) >= 2 && args[0] == "rm" && args[1] == "-f" {
				if !containerPresent {
					return errors.New("Error response from daemon: No such container")
				}
				containerPresent = false
				return cmd.Process.Kill()
			}
			if len(args) >= 2 && args[0] == "container" && args[1] == "inspect" {
				if containerPresent {
					return nil
				}
				return errors.New("Error response from daemon: No such container")
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

func TestPinnedCloudflaredStopRemovesContainerAfterTimelyReap(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("helper process did not reap")
	}

	tempDir := filepath.Join(t.TempDir(), "cloudflared")
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	containerPresent := true
	var calls []string
	tunnel := &pinnedCloudflaredTunnel{
		cmd:           cmd,
		done:          done,
		containerName: "product-capture-cloudflared-timely",
		tempDir:       tempDir,
		reapTimeout:   time.Second,
		docker: func(_ context.Context, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			switch args[0] {
			case "stop":
				return nil
			case "rm":
				containerPresent = false
				return nil
			case "container":
				if containerPresent {
					return nil
				}
				return errors.New("Error response from daemon: No such container")
			default:
				return fmt.Errorf("unexpected docker command %q", args)
			}
		},
	}
	if err := tunnel.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after timely reap: %v", err)
	}
	if containerPresent {
		t.Fatal("timely reap left the named tunnel container")
	}
	for _, want := range []string{
		"stop --timeout 3 product-capture-cloudflared-timely",
		"rm -f product-capture-cloudflared-timely",
		"container inspect product-capture-cloudflared-timely",
	} {
		if !slices.Contains(calls, want) {
			t.Fatalf("docker calls = %v, missing %q", calls, want)
		}
	}
}

func TestPinnedCloudflaredStopPreservesForceKillError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal-resistant helper requires a POSIX shell")
	}
	cmd := exec.Command("sh", "-c", `trap '' INT TERM; echo ready; while :; do sleep 1; done`)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("wait for signal-resistant helper: line=%q err=%v", line, err)
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
	killErr := errors.New("injected force-kill failure")
	tunnel := &pinnedCloudflaredTunnel{
		cmd:         cmd,
		done:        done,
		tempDir:     tempDir,
		reapTimeout: 20 * time.Millisecond,
		killProcess: func(*os.Process) error { return killErr },
	}
	err = tunnel.Stop(context.Background())
	if !errors.Is(err, killErr) {
		t.Fatalf("Stop error = %v, want force-kill failure", err)
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

func TestRunLifecycleScenarioCleansUpAfterContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker executable requires a POSIX shell")
	}
	stateDir := t.TempDir()
	binDir := t.TempDir()
	dockerPath := filepath.Join(binDir, "docker")
	fakeDocker := `#!/bin/sh
set -eu
state=${PRODUCT_CAPTURE_TEST_DOCKER_STATE:?}
case "${1:-}" in
run)
  printf '%s\n' "$*" >"$state/run.args"
  printf '%s\n' "$$" >"$state/run.pid"
  : >"$state/container"
  trap 'rm -f "$state/container"; : >"$state/stopped"; exit 0' TERM INT
  while :; do sleep 1; done
  ;;
container)
  if [ "${2:-}" = inspect ] && [ -e "$state/container" ]; then
    exit 0
  fi
  echo 'Error response from daemon: No such container' >&2
  exit 1
  ;;
stop)
  printf '%s\n' "$*" >>"$state/calls"
  kill -TERM "$(cat "$state/run.pid")"
  ;;
rm)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  kill -KILL "$(cat "$state/run.pid")" 2>/dev/null || true
  ;;
*)
  echo "unexpected docker command: $*" >&2
  exit 2
  ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(fakeDocker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRODUCT_CAPTURE_TEST_DOCKER_STATE", stateDir)
	t.Cleanup(func() {
		pidBytes, err := os.ReadFile(filepath.Join(stateDir, "run.pid"))
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil {
			return
		}
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runLifecycleScenario(ctx, "candidate:test", "https://example.test/lifecycle-hang", time.Minute, "stop", false)
	}()
	container := filepath.Join(stateDir, "container")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(container); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake lifecycle container did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runLifecycleScenario error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runLifecycleScenario did not return after cancellation")
	}
	calls, _ := os.ReadFile(filepath.Join(stateDir, "calls"))
	if !strings.Contains(string(calls), "stop --time") {
		t.Fatalf("docker calls = %q, want bounded stop after cancellation", calls)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "stopped")); err != nil {
		t.Fatalf("docker run process returned before termination: %v", err)
	}
	if _, err := os.Stat(container); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fake lifecycle container remains after cancellation: %v", err)
	}
	runArgs, err := os.ReadFile(filepath.Join(stateDir, "run.args"))
	if err != nil {
		t.Fatal(err)
	}
	joinedArgs := string(runArgs)
	for _, required := range []string{"/usr/local/bin/product-capture-provider", "--browser-diagnostic-url", "https://example.test/lifecycle-hang"} {
		if !strings.Contains(joinedArgs, required) {
			t.Errorf("lifecycle run args missing %q: %s", required, joinedArgs)
		}
	}
}

func TestRunLifecycleScenarioCleansUpWhenCanceledBeforeReadiness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker executable requires a POSIX shell")
	}
	stateDir := t.TempDir()
	binDir := t.TempDir()
	dockerPath := filepath.Join(binDir, "docker")
	fakeDocker := `#!/bin/sh
set -eu
state=${PRODUCT_CAPTURE_TEST_DOCKER_STATE:?}
case "${1:-}" in
run)
  printf '%s\n' "$$" >"$state/run.pid"
  : >"$state/container"
  trap 'rm -f "$state/container"; exit 0' TERM INT
  while :; do sleep 1; done
  ;;
container)
  echo 'Error response from daemon: No such container' >&2
  exit 1
  ;;
stop)
  printf '%s\n' "$*" >>"$state/calls"
  kill -TERM "$(cat "$state/run.pid")" 2>/dev/null || true
  ;;
rm)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  kill -KILL "$(cat "$state/run.pid")" 2>/dev/null || true
  ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(fakeDocker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRODUCT_CAPTURE_TEST_DOCKER_STATE", stateDir)
	t.Cleanup(func() { killRecordedProcess(filepath.Join(stateDir, "run.pid")) })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runLifecycleScenario(ctx, "candidate:test", "https://example.test/lifecycle-hang", time.Minute, "stop", false)
	}()
	waitForPath(t, filepath.Join(stateDir, "container"))
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runLifecycleScenario error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runLifecycleScenario did not return after pre-readiness cancellation")
	}
	calls, _ := os.ReadFile(filepath.Join(stateDir, "calls"))
	if !strings.Contains(string(calls), "stop --time") || !strings.Contains(string(calls), "rm -f") {
		t.Fatalf("docker calls = %q, want bounded stop and removal", calls)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fake lifecycle container remains after pre-readiness cancellation: %v", err)
	}
}

func TestRunLifecycleScenarioCleansUpAfterEarlyDockerExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker executable requires a POSIX shell")
	}
	stateDir := t.TempDir()
	binDir := t.TempDir()
	dockerPath := filepath.Join(binDir, "docker")
	fakeDocker := `#!/bin/sh
set -eu
state=${PRODUCT_CAPTURE_TEST_DOCKER_STATE:?}
case "${1:-}" in
run)
  printf '%s\n' "$$" >"$state/run.pid"
  : >"$state/container"
  while [ ! -e "$state/inspected" ]; do sleep 0.01; done
  exit 42
  ;;
container)
  if [ "${2:-}" = inspect ] && [ -e "$state/container" ]; then
    : >"$state/inspected"
    exit 0
  fi
  echo 'Error response from daemon: No such container' >&2
  exit 1
  ;;
stop)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  ;;
rm)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(fakeDocker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRODUCT_CAPTURE_TEST_DOCKER_STATE", stateDir)

	err := runLifecycleScenario(context.Background(), "candidate:test", "https://example.test/lifecycle-hang", time.Minute, "stop", false)
	if err == nil || !strings.Contains(err.Error(), "container exited before stop termination") {
		t.Fatalf("runLifecycleScenario error = %v, want early exit", err)
	}
	calls, _ := os.ReadFile(filepath.Join(stateDir, "calls"))
	if !strings.Contains(string(calls), "stop --time") || !strings.Contains(string(calls), "rm -f") {
		t.Fatalf("docker calls = %q, want bounded stop and removal", calls)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fake lifecycle container remains after Docker exit: %v", err)
	}
}

func TestRunManagedContainerCleansUpAfterDockerExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker executable requires a POSIX shell")
	}
	stateDir := t.TempDir()
	binDir := t.TempDir()
	dockerPath := filepath.Join(binDir, "docker")
	fakeDocker := `#!/bin/sh
set -eu
state=${PRODUCT_CAPTURE_TEST_DOCKER_STATE:?}
case "${1:-}" in
run)
  : >"$state/container"
  exit 42
  ;;
container)
  if [ "${2:-}" = inspect ] && [ -e "$state/container" ]; then exit 0; fi
  echo 'Error response from daemon: No such container' >&2
  exit 1
  ;;
stop)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  ;;
rm)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(fakeDocker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRODUCT_CAPTURE_TEST_DOCKER_STATE", stateDir)

	err := runManagedContainer(context.Background(), "candidate-exit", []string{"run", "--name", "candidate-exit", "candidate:test"}, nil)
	if err == nil || !strings.Contains(err.Error(), "candidate container candidate-exit") {
		t.Fatalf("runManagedContainer error = %v, want candidate exit failure", err)
	}
	calls, _ := os.ReadFile(filepath.Join(stateDir, "calls"))
	if !strings.Contains(string(calls), "stop --time") || !strings.Contains(string(calls), "rm -f") {
		t.Fatalf("docker calls = %q, want bounded stop and removal", calls)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fake candidate container remains after Docker exit: %v", err)
	}
}

func TestRunManagedContainerCancellationRemovesContainerAfterTimelyReap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker executable requires a POSIX shell")
	}
	stateDir := t.TempDir()
	binDir := t.TempDir()
	dockerPath := filepath.Join(binDir, "docker")
	fakeDocker := `#!/bin/sh
set -eu
state=${PRODUCT_CAPTURE_TEST_DOCKER_STATE:?}
case "${1:-}" in
run)
  printf '%s\n' "$$" >"$state/run.pid"
  : >"$state/container"
  trap 'exit 0' TERM INT
  while :; do sleep 0.01; done
  ;;
container)
  if [ "${2:-}" = inspect ] && [ -e "$state/container" ]; then exit 0; fi
  echo 'Error response from daemon: No such container' >&2
  exit 1
  ;;
stop)
  printf '%s\n' "$*" >>"$state/calls"
  kill -TERM "$(cat "$state/run.pid")" 2>/dev/null || true
  ;;
rm)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(fakeDocker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRODUCT_CAPTURE_TEST_DOCKER_STATE", stateDir)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runManagedContainer(ctx, "candidate-canceled", []string{"run", "--name", "candidate-canceled", "candidate:test"}, nil)
	}()
	waitForPath(t, filepath.Join(stateDir, "container"))
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runManagedContainer error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runManagedContainer did not return after cancellation")
	}
	calls, _ := os.ReadFile(filepath.Join(stateDir, "calls"))
	if !strings.Contains(string(calls), "stop --time") || !strings.Contains(string(calls), "rm -f") {
		t.Fatalf("docker calls = %q, want bounded stop and final removal", calls)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fake candidate container remains after cancellation: %v", err)
	}
}

func TestDockerOutputRunCleansUpAfterCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker executable requires a POSIX shell")
	}
	stateDir := t.TempDir()
	binDir := t.TempDir()
	dockerPath := filepath.Join(binDir, "docker")
	fakeDocker := `#!/bin/sh
set -eu
state=${PRODUCT_CAPTURE_TEST_DOCKER_STATE:?}
case "${1:-}" in
run)
  printf '%s\n' "$$" >"$state/run.pid"
  : >"$state/container"
  trap 'rm -f "$state/container"; exit 0' TERM INT
  while :; do sleep 1; done
  ;;
container)
  if [ "${2:-}" = inspect ] && [ -e "$state/container" ]; then exit 0; fi
  echo 'Error response from daemon: No such container' >&2
  exit 1
  ;;
stop)
  printf '%s\n' "$*" >>"$state/calls"
  kill -TERM "$(cat "$state/run.pid")" 2>/dev/null || true
  ;;
rm)
  printf '%s\n' "$*" >>"$state/calls"
  rm -f "$state/container"
  kill -KILL "$(cat "$state/run.pid")" 2>/dev/null || true
  ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(fakeDocker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PRODUCT_CAPTURE_TEST_DOCKER_STATE", stateDir)
	t.Cleanup(func() { killRecordedProcess(filepath.Join(stateDir, "run.pid")) })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := dockerOutput(ctx, "run", "--rm", "candidate:test", "--version")
		result <- err
	}()
	waitForPath(t, filepath.Join(stateDir, "container"))
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("dockerOutput succeeded after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dockerOutput did not return after cancellation")
	}
	calls, _ := os.ReadFile(filepath.Join(stateDir, "calls"))
	if !strings.Contains(string(calls), "stop --time") || !strings.Contains(string(calls), "rm -f") {
		t.Fatalf("docker calls = %q, want bounded stop and removal", calls)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("version probe container remains after cancellation: %v", err)
	}
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("path did not appear: %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func killRecordedProcess(path string) {
	pidBytes, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
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
	var release struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(manifest), &release); err != nil {
		t.Fatalf("decode plugin manifest: %v", err)
	}
	if release.Version == "" {
		t.Fatal("plugin manifest version is required")
	}
	tag := "v" + release.Version
	releaseTagPattern := regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+`)
	manifestTags := releaseTagPattern.FindAllString(manifest, -1)
	if len(manifestTags) == 0 {
		t.Fatal("plugin manifest must contain versioned download URLs")
	}
	for _, referencedTag := range manifestTags {
		if referencedTag != tag {
			t.Errorf("plugin manifest contains stale release tag %q; want only %q", referencedTag, tag)
		}
	}
	readme := readRepositoryFile(t, "README.md")
	for _, want := range []string{
		"go run ./cmd/browser-runtime-conformance",
		"product-capture:" + tag,
		"decisions/0002-use-ephemeral-diagnostic-tunnel.md",
		"provider_image_ref",
		"provider_component_ref",
		"provider_component_digest",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README missing %q", want)
		}
	}
	liveUsage := readRepositoryFile(t, "docs/buymywishlist-live-usage.md")
	_, currentUsage, found := strings.Cut(liveUsage, "## Current Release Target")
	if !found {
		t.Fatal("BuyMyWishlist live-usage documentation missing current release section")
	}
	currentUsage, _, found = strings.Cut(currentUsage, "## Verified wfcompute Staging Baseline")
	if !found {
		t.Fatal("BuyMyWishlist live-usage documentation missing verified baseline section")
	}
	for path, content := range map[string]string{
		"README.md": readme,
		"docs/buymywishlist-live-usage.md current target": currentUsage,
	} {
		if !strings.Contains(content, tag) {
			t.Errorf("%s missing manifest tag %q", path, tag)
		}
		for _, referencedTag := range releaseTagPattern.FindAllString(content, -1) {
			if referencedTag != tag {
				t.Errorf("%s contains stale release tag %q; want only %q", path, referencedTag, tag)
			}
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

type failingListener struct {
	err error
}

func (l *failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (*failingListener) Close() error                { return nil }
func (*failingListener) Addr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 43123} }

type contextCancellationTunnel struct{}

func (contextCancellationTunnel) Start(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (contextCancellationTunnel) Stop(context.Context) error { return nil }

type sequenceTunnel struct {
	origins     []string
	startErrors []error
	stopErrors  []error
	startCalls  int
	stopCalls   int
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type trackingReadCloser struct {
	io.Reader
	closed   bool
	closeErr error
	onRead   func()
	once     sync.Once
}

func (r *trackingReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.Reader.Read(buffer)
	if r.onRead != nil {
		r.once.Do(r.onRead)
	}
	return count, err
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return r.closeErr
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(buffer []byte) (int, error) {
	return f(buffer)
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
	index := t.startCalls
	origin := t.origins[index]
	t.startCalls++
	if index < len(t.startErrors) && t.startErrors[index] != nil {
		return "", t.startErrors[index]
	}
	return origin, nil
}

func (t *sequenceTunnel) Stop(context.Context) error {
	index := t.stopCalls
	t.stopCalls++
	if index < len(t.stopErrors) {
		return t.stopErrors[index]
	}
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
			Screen: ScreenSignals{Width: 1920, Height: 1080},
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
			HeaderNames: []string{"user-agent", "sec-ch-ua", "sec-fetch-site"},
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
