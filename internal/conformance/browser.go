package conformance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	SchemaV1                  = "v1"
	VerdictPass               = "pass"
	VerdictFail               = "fail"
	MaxObservationBytes       = 64 << 10
	MaxPageBytes              = 16 << 10
	CloudflaredVersion        = "2026.7.1"
	CloudflaredSHA256         = "79a0ade7fc854f62c1aaef48424d9d979e8c2fcd039189d24db82b84cd146be1"
	CloudflaredDownloadURL    = "https://github.com/cloudflare/cloudflared/releases/download/" + CloudflaredVersion + "/cloudflared-linux-amd64"
	candidateStopSeconds      = 10
	candidateReapGrace        = 12 * time.Second
	defaultConformanceTimeout = 12 * time.Minute
)

type Brand struct {
	Brand   string `json:"brand"`
	Version string `json:"version"`
}

type UserAgentData struct {
	Brands   []Brand `json:"brands"`
	Mobile   bool    `json:"mobile"`
	Platform string  `json:"platform"`
}

type NavigatorSignals struct {
	Webdriver           bool          `json:"webdriver"`
	UserAgent           string        `json:"user_agent"`
	UserAgentData       UserAgentData `json:"user_agent_data"`
	Language            string        `json:"language"`
	Languages           []string      `json:"languages"`
	Platform            string        `json:"platform"`
	HardwareConcurrency int           `json:"hardware_concurrency,omitempty"`
	DeviceMemory        float64       `json:"device_memory,omitempty"`
}

type WindowSignals struct {
	OuterWidth  int `json:"outer_width"`
	OuterHeight int `json:"outer_height"`
	InnerWidth  int `json:"inner_width"`
	InnerHeight int `json:"inner_height"`
}

type AutomationSignals struct {
	PlaywrightBindingPresent     bool `json:"playwright_binding_present"`
	PlaywrightInitScriptsPresent bool `json:"playwright_init_scripts_present"`
}

type DocumentSignals struct {
	CookiePresent bool `json:"cookie_present"`
	CookieLength  int  `json:"cookie_length"`
}

type WebGLSignals struct {
	Available bool   `json:"available"`
	Vendor    string `json:"vendor,omitempty"`
	Renderer  string `json:"renderer,omitempty"`
}

type BrowserSignals struct {
	Navigator  NavigatorSignals  `json:"navigator"`
	Window     WindowSignals     `json:"window"`
	Automation AutomationSignals `json:"automation"`
	Document   DocumentSignals   `json:"document"`
	WebGL      WebGLSignals      `json:"webgl"`
}

type ClientHintSignals struct {
	Brands   string `json:"brands"`
	Mobile   string `json:"mobile"`
	Platform string `json:"platform"`
}

type SecFetchSignals struct {
	Dest string `json:"dest"`
	Mode string `json:"mode"`
	Site string `json:"site"`
	User string `json:"user"`
}

type RequestSignals struct {
	UserAgent   string            `json:"user_agent"`
	ClientHints ClientHintSignals `json:"client_hints"`
	SecFetch    SecFetchSignals   `json:"sec_fetch"`
	HeaderOrder []string          `json:"header_order,omitempty"`
}

type Observation struct {
	Schema                string             `json:"schema"`
	RunID                 string             `json:"run_id"`
	Kind                  string             `json:"kind"`
	Browser               BrowserSignals     `json:"browser"`
	Request               RequestSignals     `json:"request"`
	FirstNavigationOrigin string             `json:"first_navigation_origin"`
	Timing                map[string]float64 `json:"timing,omitempty"`
}

type Versions struct {
	ImageID    string `json:"image_id,omitempty"`
	Chrome     string `json:"chrome"`
	Playwright string `json:"playwright"`
	Xvfb       string `json:"xvfb"`
}

type Comparison struct {
	Field     string `json:"field"`
	Direct    any    `json:"direct"`
	Attached  any    `json:"attached"`
	Tolerance int    `json:"tolerance,omitempty"`
	Match     bool   `json:"match"`
}

type InformationalPair struct {
	Direct   any `json:"direct"`
	Attached any `json:"attached"`
}

type Report struct {
	Schema            string                       `json:"schema"`
	Versions          Versions                     `json:"versions"`
	StableComparisons []Comparison                 `json:"stable_comparisons"`
	Informational     map[string]InformationalPair `json:"informational"`
	Errors            []string                     `json:"errors,omitempty"`
	Verdict           string                       `json:"verdict"`
}

func (r Report) ExitCode() int {
	if r.Verdict == VerdictPass {
		return 0
	}
	return 1
}

func Compare(direct, attached Observation, versions Versions) Report {
	report := Report{
		Schema:        SchemaV1,
		Versions:      versions,
		Informational: make(map[string]InformationalPair),
		Verdict:       VerdictPass,
	}
	if direct.Schema != SchemaV1 || attached.Schema != SchemaV1 {
		report.Errors = append(report.Errors, fmt.Sprintf("both observations must use schema %q", SchemaV1))
	}
	if direct.RunID == "" || direct.RunID != attached.RunID {
		report.Errors = append(report.Errors, "direct and attached observations must have the same nonempty run_id")
	}
	if direct.Kind != "direct" || attached.Kind != "attached" {
		report.Errors = append(report.Errors, "observations must be ordered direct then attached")
	}

	addExact := func(field string, directValue, attachedValue any) {
		report.StableComparisons = append(report.StableComparisons, Comparison{
			Field: field, Direct: directValue, Attached: attachedValue, Match: reflect.DeepEqual(directValue, attachedValue),
		})
	}
	addWindow := func(field string, directValue, attachedValue int) {
		difference := directValue - attachedValue
		if difference < 0 {
			difference = -difference
		}
		report.StableComparisons = append(report.StableComparisons, Comparison{
			Field: field, Direct: directValue, Attached: attachedValue, Tolerance: 2, Match: difference <= 2,
		})
	}

	addExact("browser.navigator.webdriver", direct.Browser.Navigator.Webdriver, attached.Browser.Navigator.Webdriver)
	addExact("browser.navigator.user_agent", direct.Browser.Navigator.UserAgent, attached.Browser.Navigator.UserAgent)
	addExact("browser.navigator.user_agent_data.brands", direct.Browser.Navigator.UserAgentData.Brands, attached.Browser.Navigator.UserAgentData.Brands)
	addExact("browser.navigator.user_agent_data.platform", direct.Browser.Navigator.UserAgentData.Platform, attached.Browser.Navigator.UserAgentData.Platform)
	addExact("browser.navigator.language", direct.Browser.Navigator.Language, attached.Browser.Navigator.Language)
	addExact("browser.navigator.languages", direct.Browser.Navigator.Languages, attached.Browser.Navigator.Languages)
	addExact("browser.navigator.platform", direct.Browser.Navigator.Platform, attached.Browser.Navigator.Platform)
	addExact("browser.automation.playwright_binding_present", direct.Browser.Automation.PlaywrightBindingPresent, attached.Browser.Automation.PlaywrightBindingPresent)
	addExact("browser.automation.playwright_init_scripts_present", direct.Browser.Automation.PlaywrightInitScriptsPresent, attached.Browser.Automation.PlaywrightInitScriptsPresent)
	addExact("request.user_agent", direct.Request.UserAgent, attached.Request.UserAgent)
	addExact("request.client_hints.brands", direct.Request.ClientHints.Brands, attached.Request.ClientHints.Brands)
	addExact("request.client_hints.mobile", direct.Request.ClientHints.Mobile, attached.Request.ClientHints.Mobile)
	addExact("request.client_hints.platform", direct.Request.ClientHints.Platform, attached.Request.ClientHints.Platform)
	addExact("request.sec_fetch.dest", direct.Request.SecFetch.Dest, attached.Request.SecFetch.Dest)
	addExact("request.sec_fetch.mode", direct.Request.SecFetch.Mode, attached.Request.SecFetch.Mode)
	addExact("request.sec_fetch.site", direct.Request.SecFetch.Site, attached.Request.SecFetch.Site)
	addExact("request.sec_fetch.user", direct.Request.SecFetch.User, attached.Request.SecFetch.User)
	originMatch := direct.FirstNavigationOrigin == attached.FirstNavigationOrigin
	report.StableComparisons = append(report.StableComparisons, Comparison{
		Field: "first_navigation_origin", Direct: "<redacted-origin>", Attached: "<redacted-origin>", Match: originMatch,
	})
	addWindow("browser.window.outer_width", direct.Browser.Window.OuterWidth, attached.Browser.Window.OuterWidth)
	addWindow("browser.window.outer_height", direct.Browser.Window.OuterHeight, attached.Browser.Window.OuterHeight)
	addWindow("browser.window.inner_width", direct.Browser.Window.InnerWidth, attached.Browser.Window.InnerWidth)
	addWindow("browser.window.inner_height", direct.Browser.Window.InnerHeight, attached.Browser.Window.InnerHeight)

	report.Informational["request.header_order"] = InformationalPair{Direct: direct.Request.HeaderOrder, Attached: attached.Request.HeaderOrder}
	report.Informational["timing"] = InformationalPair{Direct: direct.Timing, Attached: attached.Timing}
	report.Informational["browser.webgl"] = InformationalPair{Direct: direct.Browser.WebGL, Attached: attached.Browser.WebGL}
	report.Informational["browser.navigator.hardware_concurrency"] = InformationalPair{Direct: direct.Browser.Navigator.HardwareConcurrency, Attached: attached.Browser.Navigator.HardwareConcurrency}
	report.Informational["browser.navigator.device_memory"] = InformationalPair{Direct: direct.Browser.Navigator.DeviceMemory, Attached: attached.Browser.Navigator.DeviceMemory}
	report.Informational["browser.document.cookie_present"] = InformationalPair{Direct: direct.Browser.Document.CookiePresent, Attached: attached.Browser.Document.CookiePresent}
	report.Informational["browser.document.cookie_length"] = InformationalPair{Direct: direct.Browser.Document.CookieLength, Attached: attached.Browser.Document.CookieLength}

	for _, comparison := range report.StableComparisons {
		if !comparison.Match {
			report.Verdict = VerdictFail
		}
	}
	if direct.Browser.Automation.PlaywrightBindingPresent || direct.Browser.Automation.PlaywrightInitScriptsPresent ||
		attached.Browser.Automation.PlaywrightBindingPresent || attached.Browser.Automation.PlaywrightInitScriptsPresent {
		report.Errors = append(report.Errors, "checked Playwright automation globals must be absent")
	}
	if len(report.Errors) > 0 {
		report.Verdict = VerdictFail
	}
	return report
}

type Collector struct {
	runID        string
	mu           sync.Mutex
	navigations  map[string]navigationObservation
	observations map[string]Observation
	updated      chan struct{}
}

func NewCollector(runID string) *Collector {
	return &Collector{
		runID:        runID,
		navigations:  make(map[string]navigationObservation),
		observations: make(map[string]Observation),
		updated:      make(chan struct{}, 1),
	}
}

func (c *Collector) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lifecycle-hang" && r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/plain")
			select {
			case <-r.Context().Done():
			case <-time.After(30 * time.Second):
			}
			return
		}
		prefix := "/runs/" + c.runID + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		kind := strings.TrimPrefix(r.URL.Path, prefix)
		if kind == "healthz" && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]string{"schema": SchemaV1, "run_id": c.runID})
			return
		}
		if kind != "direct" && kind != "attached" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			c.recordNavigation(kind, r)
			serveSelfReportingPage(w, kind == "direct")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxObservationBytes))
		if err != nil {
			http.Error(w, "observation exceeds limit", http.StatusRequestEntityTooLarge)
			return
		}
		var payload diagnosticPayload
		if len(body) == 0 || json.Unmarshal(body, &payload) != nil || payload.Source != "product_capture_browser_diagnostic" {
			http.Error(w, "invalid observation", http.StatusBadRequest)
			return
		}
		if err := c.recordObservation(kind, payload); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	})
}

type navigationObservation struct {
	request RequestSignals
	origin  string
}

type diagnosticPayload struct {
	Source         string             `json:"source"`
	BrowserSignals BrowserSignals     `json:"browser_signals"`
	Timing         map[string]float64 `json:"timing,omitempty"`
}

func (c *Collector) recordNavigation(kind string, r *http.Request) {
	headerOrder := make([]string, 0, len(r.Header))
	for key := range r.Header {
		headerOrder = append(headerOrder, strings.ToLower(key))
	}
	slices.Sort(headerOrder)
	navigation := navigationObservation{
		request: RequestSignals{
			UserAgent: r.UserAgent(),
			ClientHints: ClientHintSignals{
				Brands:   r.Header.Get("Sec-CH-UA"),
				Mobile:   r.Header.Get("Sec-CH-UA-Mobile"),
				Platform: r.Header.Get("Sec-CH-UA-Platform"),
			},
			SecFetch: SecFetchSignals{
				Dest: r.Header.Get("Sec-Fetch-Dest"),
				Mode: r.Header.Get("Sec-Fetch-Mode"),
				Site: r.Header.Get("Sec-Fetch-Site"),
				User: r.Header.Get("Sec-Fetch-User"),
			},
			HeaderOrder: headerOrder,
		},
		origin: requestOrigin(r),
	}
	c.mu.Lock()
	if _, exists := c.navigations[kind]; !exists {
		c.navigations[kind] = navigation
	}
	c.mu.Unlock()
}

func requestOrigin(r *http.Request) string {
	scheme := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	if comma := strings.IndexByte(scheme, ','); comma >= 0 {
		scheme = strings.TrimSpace(scheme[:comma])
	}
	if scheme != "https" && scheme != "http" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if strings.ContainsAny(host, "\r\n/\\") {
		return ""
	}
	return scheme + "://" + host
}

func (c *Collector) recordObservation(kind string, payload diagnosticPayload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	navigation, exists := c.navigations[kind]
	if !exists {
		return errors.New("first navigation was not observed")
	}
	if _, exists := c.observations[kind]; exists {
		return errors.New("observation already recorded")
	}
	c.observations[kind] = Observation{
		Schema:                SchemaV1,
		RunID:                 c.runID,
		Kind:                  kind,
		Browser:               payload.BrowserSignals,
		Request:               navigation.request,
		FirstNavigationOrigin: navigation.origin,
		Timing:                payload.Timing,
	}
	select {
	case c.updated <- struct{}{}:
	default:
	}
	return nil
}

func (c *Collector) Wait(ctx context.Context, kind string) (Observation, error) {
	if kind != "direct" && kind != "attached" {
		return Observation{}, errors.New("observation kind must be direct or attached")
	}
	for {
		c.mu.Lock()
		observation, exists := c.observations[kind]
		c.mu.Unlock()
		if exists {
			return observation, nil
		}
		select {
		case <-ctx.Done():
			c.mu.Lock()
			_, navigationObserved := c.navigations[kind]
			c.mu.Unlock()
			return Observation{}, fmt.Errorf("wait for %s observation (navigation_observed=%t): %w", kind, navigationObserved, ctx.Err())
		case <-c.updated:
		}
	}
}

func serveSelfReportingPage(w http.ResponseWriter, autoSubmit bool) {
	w.Header().Set("Accept-CH", "Sec-CH-UA, Sec-CH-UA-Mobile, Sec-CH-UA-Platform")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	page := fmt.Sprintf(selfReportingPage, autoSubmit)
	_, _ = io.WriteString(w, page)
}

const selfReportingPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>Browser runtime conformance</title></head><body>
<script>
(() => {
  if (!%t) return;
  const safe = (fn, fallback = null) => { try { const value = fn(); return value === undefined ? fallback : value; } catch { return fallback; } };
  const cookie = safe(() => document.cookie || '', '');
  const userAgentData = safe(() => navigator.userAgentData ? {
    brands: Array.from(navigator.userAgentData.brands || []).map(({brand, version}) => ({brand: String(brand), version: String(version)})).slice(0, 20),
    mobile: Boolean(navigator.userAgentData.mobile), platform: String(navigator.userAgentData.platform || '')
  } : {}, {});
  const canvas = document.createElement('canvas');
  const gl = safe(() => canvas.getContext('webgl') || canvas.getContext('experimental-webgl'), null);
  const debug = gl ? safe(() => gl.getExtension('WEBGL_debug_renderer_info'), null) : null;
  const payload = {
    source: 'product_capture_browser_diagnostic',
    browser_signals: {
      navigator: {
        webdriver: safe(() => navigator.webdriver, false), user_agent: safe(() => navigator.userAgent, ''), user_agent_data: userAgentData,
        language: safe(() => navigator.language, ''), languages: safe(() => Array.from(navigator.languages || []).map(String).slice(0, 20), []),
        platform: safe(() => navigator.platform, ''), hardware_concurrency: safe(() => navigator.hardwareConcurrency, 0), device_memory: safe(() => navigator.deviceMemory, 0)
      },
      window: { outer_width: safe(() => outerWidth, 0), outer_height: safe(() => outerHeight, 0), inner_width: safe(() => innerWidth, 0), inner_height: safe(() => innerHeight, 0) },
      automation: {
        playwright_binding_present: safe(() => typeof window.__playwright__binding__ !== 'undefined', false),
        playwright_init_scripts_present: safe(() => typeof window.__pwInitScripts !== 'undefined', false)
      },
      document: { cookie_present: Boolean(cookie), cookie_length: String(cookie).length },
      webgl: { available: Boolean(gl), vendor: gl ? safe(() => String(gl.getParameter(debug ? debug.UNMASKED_VENDOR_WEBGL : gl.VENDOR)), '') : '', renderer: gl ? safe(() => String(gl.getParameter(debug ? debug.UNMASKED_RENDERER_WEBGL : gl.RENDERER)), '') : '' }
    },
    timing: { navigation_ms: safe(() => Math.max(0, performance.now()), 0) }
  };
  fetch(location.href, {method: 'POST', headers: {'content-type': 'application/json'}, credentials: 'same-origin', body: JSON.stringify(payload)}).catch(() => {});
})();
</script></body></html>`

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type Tunnel interface {
	Start(context.Context, string) (string, error)
	Stop(context.Context) error
}

type Dependencies struct {
	Tunnel              Tunnel
	HTTPClient          *http.Client
	TunnelHealthTimeout time.Duration
	LaunchDirect        func(context.Context, string, string) error
	LaunchAttached      func(context.Context, string, string) error
	ValidateLifecycle   func(context.Context, string, string) error
	InspectVersions     func(context.Context, string) (Versions, error)
}

type Options struct {
	Image  string
	Output string
	Origin string
	Listen string
}

type Runner struct {
	Dependencies Dependencies
}

func (r Runner) Run(ctx context.Context, options Options) (runErr error) {
	if strings.TrimSpace(options.Image) == "" {
		return errors.New("--image is required")
	}
	if strings.TrimSpace(options.Output) == "" {
		return errors.New("--output is required")
	}
	runID, err := randomRunID()
	if err != nil {
		return err
	}
	collector := NewCollector(runID)
	listenAddress := strings.TrimSpace(options.Listen)
	if listenAddress == "" {
		listenAddress = "0.0.0.0:0"
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen for diagnostic endpoint: %w", err)
	}
	server := &http.Server{Handler: collector.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, server.Shutdown(shutdownCtx))
	}()

	client := r.Dependencies.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	origin := strings.TrimRight(strings.TrimSpace(options.Origin), "/")
	if origin == "" {
		if r.Dependencies.Tunnel == nil {
			return errors.New("diagnostic tunnel dependency is unavailable")
		}
		_, port, splitErr := net.SplitHostPort(listener.Addr().String())
		if splitErr != nil {
			return fmt.Errorf("resolve diagnostic listen port: %w", splitErr)
		}
		healthTimeout := tunnelHealthTimeout(r.Dependencies.TunnelHealthTimeout)
		localURL := "http://127.0.0.1:" + port
		for attempt := 0; attempt < 3; attempt++ {
			candidate, startErr := r.Dependencies.Tunnel.Start(ctx, localURL)
			if startErr != nil {
				return fmt.Errorf("start diagnostic tunnel: %w", startErr)
			}
			if err := validateDiagnosticOrigin(candidate); err != nil {
				return errors.Join(err, stopTunnel(r.Dependencies.Tunnel))
			}
			healthCtx, healthCancel := context.WithTimeout(ctx, healthTimeout)
			healthErr := fetchRunHealth(healthCtx, client, candidate+"/runs/"+runID+"/healthz", runID, 500*time.Millisecond)
			healthCancel()
			if healthErr == nil {
				origin = candidate
				break
			}
			if stopErr := stopTunnel(r.Dependencies.Tunnel); stopErr != nil {
				return errors.Join(healthErr, stopErr)
			}
			if !errors.Is(healthErr, context.DeadlineExceeded) || ctx.Err() != nil || attempt == 2 {
				return healthErr
			}
		}
		defer func() { runErr = errors.Join(runErr, stopTunnel(r.Dependencies.Tunnel)) }()
	} else {
		if err := validateDiagnosticOrigin(origin); err != nil {
			return err
		}
		healthCtx, healthCancel := context.WithTimeout(ctx, 90*time.Second)
		defer healthCancel()
		if err := fetchRunHealth(healthCtx, client, origin+"/runs/"+runID+"/healthz", runID, 500*time.Millisecond); err != nil {
			return err
		}
	}
	if r.Dependencies.LaunchDirect == nil || r.Dependencies.LaunchAttached == nil || r.Dependencies.ValidateLifecycle == nil {
		return errors.New("browser launch dependencies are unavailable")
	}
	if r.Dependencies.InspectVersions == nil {
		return errors.New("runtime version dependency is unavailable")
	}
	versions, err := r.Dependencies.InspectVersions(ctx, options.Image)
	if err != nil {
		return fmt.Errorf("inspect candidate versions: %w", err)
	}
	if err := r.Dependencies.ValidateLifecycle(ctx, options.Image, origin); err != nil {
		return fmt.Errorf("validate candidate lifecycle: %w", err)
	}
	direct, err := launchAndCollect(ctx, collector, "direct", options.Image, origin+"/runs/"+runID+"/direct", r.Dependencies.LaunchDirect)
	if err != nil {
		return err
	}
	attached, err := launchAndCollect(ctx, collector, "attached", options.Image, origin+"/runs/"+runID+"/attached", r.Dependencies.LaunchAttached)
	if err != nil {
		return err
	}
	report := Compare(direct, attached, versions)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal conformance report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(options.Output, data, 0o600); err != nil {
		return fmt.Errorf("write conformance report: %w", err)
	}
	if report.ExitCode() != 0 {
		return errors.New("browser runtime conformance failed")
	}
	return nil
}

func tunnelHealthTimeout(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return 2 * time.Minute
}

func validateDiagnosticOrigin(origin string) error {
	parsedOrigin, err := url.Parse(origin)
	if err != nil || parsedOrigin.Scheme != "https" || parsedOrigin.Host == "" || parsedOrigin.User != nil || parsedOrigin.Path != "" || parsedOrigin.RawQuery != "" || parsedOrigin.Fragment != "" {
		return errors.New("diagnostic origin must be an exact HTTPS origin")
	}
	return nil
}

func stopTunnel(tunnel Tunnel) error {
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tunnel.Stop(stopCtx)
}

func fetchRunHealth(ctx context.Context, client *http.Client, healthURL, runID string, retryInterval time.Duration) error {
	if retryInterval <= 0 {
		retryInterval = 500 * time.Millisecond
	}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody)
		if err != nil {
			return fmt.Errorf("create diagnostic health request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			var health struct {
				Schema string `json:"schema"`
				RunID  string `json:"run_id"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health)
			closeErr := resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if decodeErr != nil || closeErr != nil || health.Schema != SchemaV1 || health.RunID != runID {
					return errors.New("run-correlated diagnostic health endpoint rejected")
				}
				return nil
			}
			if !retryableHealthStatus(resp.StatusCode) {
				return fmt.Errorf("run-correlated diagnostic health endpoint returned status %d", resp.StatusCode)
			}
			lastErr = fmt.Errorf("diagnostic health endpoint returned transient status %d", resp.StatusCode)
		} else {
			lastErr = errors.New("fetch run-correlated diagnostic health endpoint failed")
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
}

func retryableHealthStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func launchAndCollect(
	ctx context.Context,
	collector *Collector,
	kind, image, target string,
	launch func(context.Context, string, string) error,
) (Observation, error) {
	launchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	launchResult := make(chan error, 1)
	go func() { launchResult <- launch(launchCtx, image, target) }()
	observationResult := make(chan struct {
		observation Observation
		err         error
	}, 1)
	go func() {
		observation, err := collector.Wait(launchCtx, kind)
		observationResult <- struct {
			observation Observation
			err         error
		}{observation: observation, err: err}
	}()

	select {
	case result := <-observationResult:
		if result.err != nil {
			return Observation{}, errors.Join(result.err, waitForLaunchCleanup(kind, cancel, launchResult))
		}
		if err := waitForLaunchCleanup(kind, cancel, launchResult); err != nil {
			return Observation{}, err
		}
		return result.observation, nil
	case err := <-launchResult:
		if err != nil {
			return Observation{}, fmt.Errorf("launch %s browser: %w", kind, err)
		}
		select {
		case result := <-observationResult:
			if result.err != nil {
				return Observation{}, result.err
			}
			return result.observation, nil
		case <-ctx.Done():
			return Observation{}, ctx.Err()
		}
	case <-ctx.Done():
		cleanupErr := waitForLaunchCleanup(kind, cancel, launchResult)
		var observationErr error
		select {
		case result := <-observationResult:
			observationErr = result.err
		case <-time.After(2 * time.Second):
			observationErr = fmt.Errorf("wait for %s observation cancellation timed out", kind)
		}
		return Observation{}, errors.Join(ctx.Err(), observationErr, cleanupErr)
	}
}

func waitForLaunchCleanup(kind string, cancel context.CancelFunc, launchResult <-chan error) error {
	cancel()
	select {
	case err := <-launchResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("%s browser cleanup: %w", kind, err)
		}
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("%s browser cleanup timed out", kind)
	}
}

func randomRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create diagnostic run id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func VerifyCloudflaredArtifact(path, expectedDigest, versionOutput string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open cloudflared artifact: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 256<<20)); err != nil {
		return fmt.Errorf("hash cloudflared artifact: %w", err)
	}
	actualDigest := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualDigest, strings.TrimSpace(expectedDigest)) {
		return fmt.Errorf("cloudflared digest mismatch: got %s", actualDigest)
	}
	fields := strings.Fields(versionOutput)
	version := ""
	for index := range fields {
		if fields[index] == "version" && index+1 < len(fields) {
			version = fields[index+1]
			break
		}
	}
	if version != CloudflaredVersion {
		return fmt.Errorf("cloudflared version mismatch: got %q, want %q", version, CloudflaredVersion)
	}
	return nil
}

func Main(args []string, stdout, stderr io.Writer, dependencies ...Dependencies) int {
	return MainContext(context.Background(), args, stdout, stderr, dependencies...)
}

func MainContext(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies ...Dependencies) int {
	fs := flag.NewFlagSet("browser-runtime-conformance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var options Options
	fs.StringVar(&options.Image, "image", "", "candidate runtime image tag")
	fs.StringVar(&options.Output, "output", "", "path for redacted conformance JSON")
	fs.StringVar(&options.Origin, "origin", "", "explicit HTTPS origin forwarding to this endpoint")
	fs.StringVar(&options.Listen, "listen", "", "local diagnostic listen address")
	timeout := fs.Duration("timeout", defaultConformanceTimeout, "overall conformance timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(options.Image) == "" {
		_, _ = fmt.Fprintln(stderr, "--image is required")
		return 2
	}
	if strings.TrimSpace(options.Output) == "" {
		_, _ = fmt.Fprintln(stderr, "--output is required")
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	var deps Dependencies
	if len(dependencies) > 0 {
		deps = dependencies[0]
	} else {
		deps = DefaultDependencies(stderr)
	}
	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err := (Runner{Dependencies: deps}).Run(runCtx, options); err != nil {
		_, _ = fmt.Fprintf(stderr, "browser runtime conformance: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "browser runtime conformance passed: %s\n", options.Output)
	return 0
}

func DefaultDependencies(stderr io.Writer) Dependencies {
	client := &http.Client{Timeout: 45 * time.Second}
	return Dependencies{
		Tunnel:            &pinnedCloudflaredTunnel{client: client, stderr: stderr},
		HTTPClient:        client,
		LaunchDirect:      launchDirectChrome,
		LaunchAttached:    launchAttachedProvider,
		ValidateLifecycle: validateCandidateLifecycle,
		InspectVersions:   inspectCandidateVersions,
	}
}

const headedContainerScript = `set -eu
xvfb_pid=
child_pid=
cleanup() {
  if [ -n "$child_pid" ]; then
    kill -TERM "$child_pid" 2>/dev/null || true
    (
      sleep "${PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT:-10}"
      kill -KILL "$child_pid" 2>/dev/null || true
    ) &
    watchdog_pid=$!
    wait "$child_pid" 2>/dev/null || true
    kill "$watchdog_pid" 2>/dev/null || true
    wait "$watchdog_pid" 2>/dev/null || true
    child_pid=
  fi
  if [ -n "$xvfb_pid" ]; then kill -TERM "$xvfb_pid" 2>/dev/null || true; fi
  if [ -n "$xvfb_pid" ]; then wait "$xvfb_pid" 2>/dev/null || true; fi
}
trap cleanup EXIT
trap 'exit 143' TERM INT
rm -f /tmp/.X99-lock /tmp/.X11-unix/X99
Xvfb :99 -screen 0 1920x1080x24 -nolisten tcp >/tmp/product-capture-xvfb.log 2>&1 &
xvfb_pid=$!
timeout=${PRODUCT_CAPTURE_XVFB_READY_TIMEOUT:-10}
attempts=$((timeout * 20))
while [ ! -S /tmp/.X11-unix/X99 ]; do
  if ! kill -0 "$xvfb_pid" 2>/dev/null; then
    cat /tmp/product-capture-xvfb.log >&2 || true
    exit 1
  fi
  attempts=$((attempts - 1))
  if [ "$attempts" -le 0 ]; then
    echo "Xvfb socket readiness timed out" >&2
    exit 1
  fi
  sleep 0.05
done
export DISPLAY=:99
"$@" &
child_pid=$!
set +e
wait "$child_pid"
status=$?
set -e
child_pid=
exit "$status"`

func directChromeContainerArgs(image, target, hostProfile string) []string {
	name := "product-capture-direct-" + mustRandomSuffix()
	return []string{
		"run", "--rm", "--platform", "linux/amd64", "--name", name,
		"-e", "PRODUCT_CAPTURE_XVFB_READY_TIMEOUT=10",
		"-e", "PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT=10",
		"-v", hostProfile + ":/tmp/conformance-profile",
		"--entrypoint", "/bin/sh", image,
		"-c", headedContainerScript, "--", "google-chrome",
		"--user-data-dir=/tmp/conformance-profile",
		"--window-size=1920,1080",
		"--no-first-run", "--no-default-browser-check",
		"--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage",
		target,
	}
}

func attachedProviderContainerArgs(image, target string) []string {
	parsed, _ := url.Parse(target)
	origin := parsed.Scheme + "://" + parsed.Host
	return []string{
		"run", "--rm", "--platform", "linux/amd64", "--name", "product-capture-attached-" + mustRandomSuffix(),
		"-e", "PRODUCT_CAPTURE_BROWSER_HEADLESS=false",
		"-e", "PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS=" + origin,
		"-e", "PRODUCT_CAPTURE_XVFB_READY_TIMEOUT=10",
		"-e", "PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT=10",
		"--entrypoint", "/bin/sh", image,
		"-c", headedContainerScript, "--", "/usr/local/bin/product-capture-provider", "--browser-diagnostic-url", target,
	}
}

func launchDirectChrome(ctx context.Context, image, target string) error {
	profile, err := os.MkdirTemp("", "product-capture-conformance-profile-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	if err := os.Chmod(profile, 0o777); err != nil {
		return err
	}
	args := directChromeContainerArgs(image, target, profile)
	name := containerName(args)
	runErr := runManagedContainer(ctx, name, args, nil)
	return errors.Join(runErr, cleanupEphemeralProfile(profile))
}

func launchAttachedProvider(ctx context.Context, image, target string) error {
	args := attachedProviderContainerArgs(image, target)
	return runManagedContainer(ctx, containerName(args), args, nil)
}

func runManagedContainer(ctx context.Context, name string, args []string, started chan<- struct{}) error {
	cmd := exec.Command("docker", args...)
	var output boundedWriter
	output.limit = 32 << 10
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start docker container %s: %w", name, err)
	}
	if started != nil {
		close(started)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			return fmt.Errorf("candidate container %s: %w: %s", name, err, output.String())
		}
		return assertContainerGone(name)
	case <-ctx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		stopErr := dockerCommand(stopCtx, "stop", "--time", fmt.Sprintf("%d", candidateStopSeconds), name)
		reapErr := forceContainerAndWait(wait, candidateReapGrace, func() error {
			forceCtx, forceCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer forceCancel()
			return ignoreMissingContainer(dockerCommand(forceCtx, "rm", "-f", name))
		})
		return errors.Join(stopErr, reapErr, assertContainerGone(name))
	}
}

type boundedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	original := len(data)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(data[:min(remaining, len(data))])
	}
	return original, nil
}

func (w *boundedWriter) String() string { return strings.TrimSpace(w.buffer.String()) }

func dockerCommand(ctx context.Context, args ...string) error {
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func assertContainerGone(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "container", "inspect", name).CombinedOutput()
	if err == nil {
		return fmt.Errorf("candidate container %s remains after cleanup", name)
	}
	if !strings.Contains(string(output), "No such") {
		return fmt.Errorf("inspect candidate cleanup %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func assertNoProfileLocks(profile string) error {
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie", "DevToolsActivePort"} {
		if _, err := os.Lstat(filepath.Join(profile, name)); err == nil {
			return fmt.Errorf("chrome profile lock %s remains after cleanup", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func cleanupEphemeralProfile(profile string) error {
	if strings.TrimSpace(profile) == "" {
		return errors.New("ephemeral profile path is empty")
	}
	return errors.Join(os.RemoveAll(profile), assertNoProfileLocks(profile))
}

func validateCandidateLifecycle(ctx context.Context, image, origin string) error {
	scenarios := []struct {
		name   string
		target string
		delay  time.Duration
		signal string
	}{
		{name: "startup", target: origin + "/lifecycle-hang", delay: 150 * time.Millisecond, signal: "stop"},
		{name: "navigation-timeout", target: origin + "/lifecycle-hang", delay: 2 * time.Second, signal: "stop"},
		{name: "sigterm", target: origin + "/lifecycle-hang", delay: 750 * time.Millisecond, signal: "SIGTERM"},
	}
	for _, scenario := range scenarios {
		if err := runLifecycleScenario(ctx, image, scenario.target, scenario.delay, scenario.signal); err != nil {
			return fmt.Errorf("%s lifecycle: %w", scenario.name, err)
		}
	}
	return nil
}

func runLifecycleScenario(ctx context.Context, image, target string, delay time.Duration, signal string) error {
	profile, err := os.MkdirTemp("", "product-capture-lifecycle-profile-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	if err := os.Chmod(profile, 0o777); err != nil {
		return err
	}
	args := directChromeContainerArgs(image, target, profile)
	name := containerName(args)
	cmd := exec.Command("docker", args...)
	var output boundedWriter
	output.limit = 16 << 10
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	if err := waitForContainer(ctx, name); err != nil {
		_ = cmd.Process.Kill()
		<-wait
		return err
	}
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
	case err := <-wait:
		return fmt.Errorf("container exited before %s termination: %w: %s", signal, err, output.String())
	}
	commandCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if signal == "stop" {
		err = dockerCommand(commandCtx, "stop", "--time", fmt.Sprintf("%d", candidateStopSeconds), name)
	} else {
		err = dockerCommand(commandCtx, "kill", "--signal", signal, name)
	}
	reapErr := forceContainerAndWait(wait, candidateReapGrace, func() error {
		forceCtx, forceCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer forceCancel()
		return ignoreMissingContainer(dockerCommand(forceCtx, "rm", "-f", name))
	})
	return errors.Join(err, reapErr, assertContainerGone(name), cleanupEphemeralProfile(profile))
}

func forceContainerAndWait(wait <-chan error, grace time.Duration, force func() error) error {
	if grace <= 0 {
		grace = 5 * time.Second
	}
	timer := time.NewTimer(grace)
	select {
	case <-wait:
		timer.Stop()
		return nil
	case <-timer.C:
	}
	forceErr := force()
	reapTimer := time.NewTimer(grace)
	defer reapTimer.Stop()
	select {
	case <-wait:
		return forceErr
	case <-reapTimer.C:
		return errors.Join(forceErr, errors.New("container process did not reap after force removal"))
	}
}

func waitForContainer(ctx context.Context, name string) error {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		checkCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := dockerCommand(checkCtx, "container", "inspect", name)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("candidate container %s did not start", name)
		case <-ticker.C:
		}
	}
}

func inspectCandidateVersions(ctx context.Context, image string) (Versions, error) {
	imageID, err := dockerOutput(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return Versions{}, err
	}
	chrome, err := dockerOutput(ctx, "run", "--rm", "--platform", "linux/amd64", "--entrypoint", "google-chrome", image, "--version")
	if err != nil {
		return Versions{}, err
	}
	playwright, err := dockerOutput(ctx, "run", "--rm", "--platform", "linux/amd64", "--entrypoint", "node", image, "-e", "process.stdout.write(require('playwright/package.json').version)")
	if err != nil {
		return Versions{}, err
	}
	xvfb, err := dockerOutput(ctx, "run", "--rm", "--platform", "linux/amd64", "--entrypoint", "dpkg-query", image, "-W", "-f=${Version}", "xvfb")
	if err != nil {
		return Versions{}, err
	}
	return Versions{ImageID: imageID, Chrome: chrome, Playwright: playwright, Xvfb: xvfb}, nil
}

func dockerOutput(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	value := strings.TrimSpace(string(output))
	if value == "" || len(value) > 4096 {
		return "", fmt.Errorf("docker %s returned invalid bounded output", strings.Join(args, " "))
	}
	return value, nil
}

func containerName(args []string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--name" {
			return args[index+1]
		}
	}
	return ""
}

func mustRandomSuffix() string {
	value, err := randomRunID()
	if err != nil {
		panic(err)
	}
	return value[:12]
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type pinnedCloudflaredTunnel struct {
	client        *http.Client
	stderr        io.Writer
	docker        func(context.Context, ...string) error
	reapTimeout   time.Duration
	mu            sync.Mutex
	cmd           *exec.Cmd
	done          chan struct{}
	waitErr       error
	containerName string
	tempDir       string
}

var quickTunnelOrigin = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func parseQuickTunnelOrigin(line string) string {
	location := quickTunnelOrigin.FindStringIndex(line)
	if location == nil {
		return ""
	}
	origin := line[location[0]:location[1]]
	if origin == "https://api.trycloudflare.com" {
		return ""
	}
	if location[1] < len(line) && strings.ContainsRune("/?#", rune(line[location[1]])) {
		return ""
	}
	return origin
}

func (t *pinnedCloudflaredTunnel) Start(ctx context.Context, localURL string) (string, error) {
	tempDir, err := os.MkdirTemp("", "product-capture-cloudflared-*")
	if err != nil {
		return "", err
	}
	t.tempDir = tempDir
	path := filepath.Join(tempDir, "cloudflared-linux-amd64")
	if err := t.download(ctx, path); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", err
	}
	versionOutput, err := t.cloudflaredVersion(ctx, path)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", err
	}
	if err := VerifyCloudflaredArtifact(path, CloudflaredSHA256, versionOutput); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", err
	}
	cmd, containerName := cloudflaredCommand(path, localURL)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	t.mu.Lock()
	t.cmd = cmd
	t.containerName = containerName
	t.done = make(chan struct{})
	done := t.done
	t.mu.Unlock()
	go func() {
		err := cmd.Wait()
		t.mu.Lock()
		t.waitErr = err
		close(done)
		t.mu.Unlock()
	}()
	origins := make(chan string, 2)
	scan := func(reader io.Reader) {
		scanner := bufio.NewScanner(io.LimitReader(reader, 128<<10))
		for scanner.Scan() {
			line := scanner.Text()
			if t.stderr != nil {
				_, _ = fmt.Fprintln(t.stderr, redactTunnelLog(line))
			}
			if origin := parseQuickTunnelOrigin(line); origin != "" {
				select {
				case origins <- origin:
				default:
				}
			}
		}
	}
	go scan(stdout)
	go scan(stderr)
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case origin := <-origins:
		return origin, nil
	case <-done:
		t.mu.Lock()
		err := t.waitErr
		t.mu.Unlock()
		_ = t.Stop(context.Background())
		return "", fmt.Errorf("cloudflared exited before publishing an origin: %w", err)
	case <-timer.C:
		_ = t.Stop(context.Background())
		return "", errors.New("cloudflared timed out before publishing an origin")
	case <-ctx.Done():
		_ = t.Stop(context.Background())
		return "", ctx.Err()
	}
}

func (t *pinnedCloudflaredTunnel) download(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CloudflaredDownloadURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("download cloudflared: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download cloudflared: status %d", resp.StatusCode)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, (256<<20)+1))
	closeErr := file.Close()
	if written > 256<<20 {
		return errors.New("cloudflared artifact exceeds 256 MiB")
	}
	return errors.Join(copyErr, closeErr)
}

func (t *pinnedCloudflaredTunnel) cloudflaredVersion(ctx context.Context, path string) (string, error) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
		return string(output), err
	}
	return dockerOutput(ctx,
		"run", "--rm", "--platform", "linux/amd64",
		"-v", path+":/cloudflared:ro", "--entrypoint", "/cloudflared",
		"debian:bookworm-slim", "--version",
	)
}

func cloudflaredCommand(path, localURL string) (*exec.Cmd, string) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return exec.Command(path, "tunnel", "--url", localURL, "--no-autoupdate"), ""
	}
	parsed, _ := url.Parse(localURL)
	dockerURL := "http://host.docker.internal:" + parsed.Port()
	name := "product-capture-cloudflared-" + mustRandomSuffix()
	args := []string{
		"run", "--rm", "--platform", "linux/amd64", "--name", name,
		"-v", path + ":/cloudflared:ro",
	}
	if runtime.GOOS == "darwin" {
		args = append(args, "-v", "/etc/ssl/cert.pem:/etc/ssl/certs/ca-certificates.crt:ro")
	}
	args = append(args,
		"--entrypoint", "/cloudflared", "debian:bookworm-slim",
		"tunnel", "--url", dockerURL, "--no-autoupdate",
	)
	return exec.Command("docker", args...), name
}

func (t *pinnedCloudflaredTunnel) Stop(ctx context.Context) error {
	t.mu.Lock()
	cmd, done, name, tempDir := t.cmd, t.done, t.containerName, t.tempDir
	t.cmd, t.done, t.containerName, t.tempDir = nil, nil, "", ""
	t.mu.Unlock()
	docker := t.docker
	if docker == nil {
		docker = dockerCommand
	}
	reapTimeout := t.reapTimeout
	if reapTimeout <= 0 {
		reapTimeout = 5 * time.Second
	}
	if cmd == nil {
		var removeErr error
		if name != "" {
			forceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			removeErr = ignoreMissingContainer(docker(forceCtx, "rm", "-f", name))
			cancel()
		}
		if tempDir != "" {
			removeErr = errors.Join(removeErr, os.RemoveAll(tempDir))
		}
		return removeErr
	}
	var stopErr error
	if name != "" {
		stopErr = ignoreMissingContainer(docker(ctx, "stop", "--timeout", "3", name))
	} else if cmd.Process != nil {
		stopErr = cmd.Process.Signal(os.Interrupt)
	}
	timer := time.NewTimer(reapTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		var forceErr error
		if name != "" {
			forceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			forceErr = ignoreMissingContainer(docker(forceCtx, "rm", "-f", name))
			cancel()
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		stopErr = errors.Join(stopErr, forceErr, ctx.Err())
	case <-timer.C:
		var forceErr error
		if name != "" {
			forceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			forceErr = ignoreMissingContainer(docker(forceCtx, "rm", "-f", name))
			cancel()
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if name != "" && forceErr == nil {
			stopErr = nil
		} else {
			stopErr = errors.Join(stopErr, forceErr, errors.New("cloudflared did not reap after stop"))
		}
	}
	postKillTimer := time.NewTimer(reapTimeout)
	defer postKillTimer.Stop()
	select {
	case <-done:
	case <-postKillTimer.C:
		stopErr = errors.Join(stopErr, errors.New("cloudflared process did not reap after force removal"))
	}
	return errors.Join(stopErr, os.RemoveAll(tempDir))
}

func ignoreMissingContainer(err error) error {
	if strings.Contains(errorString(err), "No such container") {
		return nil
	}
	return err
}

func redactTunnelLog(line string) string {
	line = quickTunnelOrigin.ReplaceAllString(line, "https://<redacted>.trycloudflare.com")
	if len(line) > 500 {
		line = line[:500]
	}
	return line
}
