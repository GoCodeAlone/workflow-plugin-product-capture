package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	coreprotocol "github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/snapshot"
)

const (
	ProviderName           = "workflow-plugin-product-capture"
	ExecutorProvider       = "product-capture-browser"
	WorkloadKind           = "provider"
	CaptureOperation       = "capture_product"
	ProductJSONArtifact    = "product_json"
	CaptureModeBrowser     = "browser"
	CaptureModeMeta        = "metadata"
	ComputeProtocolVersion = "compute.v1alpha1"
)

var Version = "0.1.0"

var supportedAmazonHosts = map[string]struct{}{
	"amazon.com":     {},
	"www.amazon.com": {},
}

type Request struct {
	Workload Workload `json:"workload"`
}

type dynamicEnvelope struct {
	ProtocolVersion string                              `json:"protocol_version"`
	TaskID          string                              `json:"task_id"`
	LeaseID         string                              `json:"lease_id"`
	WorkloadKind    coreprotocol.WorkloadKind           `json:"workload_kind,omitempty"`
	ProviderConfig  coreprotocol.ProviderConfig         `json:"provider_config"`
	Operation       string                              `json:"operation"`
	Input           json.RawMessage                     `json:"input"`
	Executor        coreprotocol.ExecutorRef            `json:"executor,omitzero"`
	RuntimeProfile  coreprotocol.ProviderRuntimeProfile `json:"runtime_profile,omitzero"`
	RuntimeBackend  coreprotocol.RuntimeBackendReport   `json:"runtime_backend,omitzero"`
	Env             map[string]string                   `json:"env,omitempty"`
	Limits          coreprotocol.ResourceLimits         `json:"limits,omitzero"`
}

type Workload struct {
	URL            string   `json:"url"`
	AllowedHosts   []string `json:"allowed_hosts"`
	CaptureMode    string   `json:"capture_mode,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MaxHTMLBytes   int64    `json:"max_html_bytes,omitempty"`
	MaxImageCount  int      `json:"max_image_count,omitempty"`
	MetadataOnly   bool     `json:"metadata_only,omitempty"`
}

type probeResponse struct {
	Provider              string   `json:"provider"`
	ProviderVersion       string   `json:"provider_version"`
	Status                string   `json:"status"`
	WorkloadKind          string   `json:"workload_kind"`
	ExecutorProvider      string   `json:"executor_provider"`
	ExecutionSecurityTier string   `json:"execution_security_tier"`
	ProofTier             string   `json:"proof_tier"`
	SupportedHosts        []string `json:"supported_hosts"`
	RuntimeTools          []string `json:"runtime_tools"`
}

func WriteProbe(w io.Writer) error {
	resp := probeResponse{
		Provider:              ProviderName,
		ProviderVersion:       Version,
		Status:                "supported",
		WorkloadKind:          WorkloadKind,
		ExecutorProvider:      ExecutorProvider,
		ExecutionSecurityTier: "sandboxed-container",
		ProofTier:             "artifact-hash",
		SupportedHosts:        supportedHosts(),
		RuntimeTools:          []string{"node", "chrome"},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func Main(args []string, stdout, stderr io.Writer, stdin ...io.Reader) int {
	fs := flag.NewFlagSet("product-capture-provider", flag.ContinueOnError)
	fs.SetOutput(stderr)
	requestPath := fs.String("request", "", "path to product capture request JSON")
	outputPath := fs.String("output", "", "path to write product snapshot JSON")
	probe := fs.Bool("probe", false, "print provider capability probe")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *probe {
		if err := WriteProbe(stdout); err != nil {
			fmt.Fprintf(stderr, "probe: %v\n", err)
			return 1
		}
		return 0
	}
	if *requestPath == "" && *outputPath == "" {
		input := io.Reader(os.Stdin)
		if len(stdin) > 0 && stdin[0] != nil {
			input = stdin[0]
		}
		if err := runDynamic(input, stdout); err != nil {
			fmt.Fprintf(stderr, "product capture: %v\n", err)
			return 1
		}
		return 0
	}
	if *requestPath == "" || *outputPath == "" {
		fmt.Fprintln(stderr, "--request and --output are both required")
		return 2
	}
	if err := run(*requestPath, *outputPath); err != nil {
		fmt.Fprintf(stderr, "product capture: %v\n", err)
		return 1
	}
	return 0
}

func run(requestPath, outputPath string) error {
	req, err := readRequest(requestPath)
	if err != nil {
		return err
	}
	return runWorkload(req.Workload, outputPath)
}

func runDynamic(r io.Reader, stdout io.Writer) error {
	env, err := readDynamicEnvelope(r)
	if err != nil {
		return err
	}
	if err := validateDynamicEnvelope(env); err != nil {
		return err
	}
	var workload Workload
	dec := json.NewDecoder(bytes.NewReader(env.Input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&workload); err != nil {
		return fmt.Errorf("decode operation input: %w", err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("decode operation input: multiple JSON values")
	}
	if err := runWorkload(workload, ProductJSONArtifact); err != nil {
		return err
	}
	resp := struct {
		Artifacts []string `json:"artifacts"`
	}{
		Artifacts: []string{ProductJSONArtifact},
	}
	enc := json.NewEncoder(stdout)
	return enc.Encode(resp)
}

func readDynamicEnvelope(r io.Reader) (dynamicEnvelope, error) {
	var env dynamicEnvelope
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return dynamicEnvelope{}, fmt.Errorf("decode provider envelope: %w", err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return dynamicEnvelope{}, errors.New("decode provider envelope: multiple JSON values")
	}
	return env, nil
}

func validateDynamicEnvelope(env dynamicEnvelope) error {
	if env.ProtocolVersion != ComputeProtocolVersion {
		return fmt.Errorf("unsupported protocol_version %q", env.ProtocolVersion)
	}
	if env.WorkloadKind != "" && env.WorkloadKind != coreprotocol.WorkloadProvider {
		return fmt.Errorf("unsupported workload_kind %q", env.WorkloadKind)
	}
	if err := env.ProviderConfig.Validate(); err != nil {
		return fmt.Errorf("provider_config: %w", err)
	}
	if env.ProviderConfig.PluginID != ProviderName ||
		env.ProviderConfig.ProviderID != "browser" ||
		env.ProviderConfig.ContractID != "product-capture.browser.v1" ||
		env.ProviderConfig.Version != "v1.0.0" {
		return fmt.Errorf("provider_config does not match product-capture browser v1")
	}
	if env.Operation != CaptureOperation {
		return fmt.Errorf("unsupported operation %q", env.Operation)
	}
	if err := validateExecutorMetadata(env.Executor); err != nil {
		return fmt.Errorf("executor: %w", err)
	}
	if err := validateRuntimeProfileMetadata(env.RuntimeProfile); err != nil {
		return fmt.Errorf("runtime_profile: %w", err)
	}
	if err := validateRuntimeBackendMetadata(env.RuntimeBackend); err != nil {
		return fmt.Errorf("runtime_backend: %w", err)
	}
	if err := validateRuntimeMetadataConsistency(env); err != nil {
		return err
	}
	if err := env.Limits.Validate(); err != nil {
		return fmt.Errorf("limits: %w", err)
	}
	if len(env.Input) == 0 {
		return errors.New("input is required")
	}
	return nil
}

func validateExecutorMetadata(executor coreprotocol.ExecutorRef) error {
	if executor == (coreprotocol.ExecutorRef{}) {
		return nil
	}
	if executor.Provider != ExecutorProvider {
		return fmt.Errorf("provider %q does not match %q", executor.Provider, ExecutorProvider)
	}
	if executor.ExecutionSecurityTier != "" && executor.ExecutionSecurityTier != coreprotocol.ExecutionSandboxedContainer {
		return fmt.Errorf("execution_security_tier %q is unsupported", executor.ExecutionSecurityTier)
	}
	if executor.ProofTier != "" && executor.ProofTier != coreprotocol.ProofArtifactHash {
		return fmt.Errorf("proof_tier %q is unsupported", executor.ProofTier)
	}
	if err := executor.ValidateForProof(); err != nil {
		return err
	}
	return nil
}

func validateRuntimeProfileMetadata(profile coreprotocol.ProviderRuntimeProfile) error {
	if isZeroMetadata(profile) {
		return nil
	}
	if profile.ExecutorProvider != ExecutorProvider {
		return fmt.Errorf("executor_provider %q does not match %q", profile.ExecutorProvider, ExecutorProvider)
	}
	if profile.RuntimeProfile != coreprotocol.RuntimeProfileSandboxedOCI {
		return fmt.Errorf("runtime_profile %q is unsupported", profile.RuntimeProfile)
	}
	if profile.ExecutionSecurityTier != coreprotocol.ExecutionSandboxedContainer {
		return fmt.Errorf("execution_security_tier %q is unsupported", profile.ExecutionSecurityTier)
	}
	if profile.ProofTier != coreprotocol.ProofArtifactHash {
		return fmt.Errorf("proof_tier %q is unsupported", profile.ProofTier)
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	return nil
}

func validateRuntimeBackendMetadata(report coreprotocol.RuntimeBackendReport) error {
	if isZeroMetadata(report) {
		return nil
	}
	if err := report.Validate(); err != nil {
		return err
	}
	if report.Status != coreprotocol.RuntimeBackendSupported {
		return fmt.Errorf("status %q is unsupported", report.Status)
	}
	if !slices.Contains(report.ExecutorProviders, ExecutorProvider) {
		return fmt.Errorf("executor provider %q is not supported by backend %q", ExecutorProvider, report.BackendID)
	}
	return nil
}

func validateRuntimeMetadataConsistency(env dynamicEnvelope) error {
	if isZeroMetadata(env.RuntimeBackend) {
		return nil
	}
	if !runtimeBackendHasProductCaptureExecutor(env.RuntimeBackend) {
		return fmt.Errorf("runtime_backend does not include a matching %q executor", ExecutorProvider)
	}
	if !isZeroMetadata(env.Executor) && !runtimeBackendHasMatchingExecutor(env.RuntimeBackend, env.Executor) {
		return fmt.Errorf("runtime_backend does not match selected executor %q", env.Executor.Provider)
	}
	if !isZeroMetadata(env.RuntimeProfile) {
		if !slices.Contains(env.RuntimeBackend.RuntimeProfiles, env.RuntimeProfile.RuntimeProfile) {
			return fmt.Errorf("runtime_backend does not support runtime profile %q", env.RuntimeProfile.RuntimeProfile)
		}
		if !slices.Contains(env.RuntimeProfile.ConformanceProfiles, "product-capture-v1") ||
			!slices.Contains(env.RuntimeBackend.ConformanceProfiles, "product-capture-v1") {
			return errors.New("runtime_backend and runtime_profile must include product-capture-v1 conformance")
		}
	}
	return nil
}

func runtimeBackendHasProductCaptureExecutor(report coreprotocol.RuntimeBackendReport) bool {
	for _, executor := range report.Executors {
		if executorMeetsProductCaptureFloor(executor) {
			return true
		}
	}
	return false
}

func executorMeetsProductCaptureFloor(executor coreprotocol.ExecutorRef) bool {
	if executor.Provider != ExecutorProvider {
		return false
	}
	if executor.ExecutionSecurityTier != coreprotocol.ExecutionSandboxedContainer {
		return false
	}
	if executor.ProofTier != coreprotocol.ProofArtifactHash {
		return false
	}
	return executor.ValidateForProof() == nil
}

func runtimeBackendHasMatchingExecutor(report coreprotocol.RuntimeBackendReport, want coreprotocol.ExecutorRef) bool {
	for _, got := range report.Executors {
		if got.Provider != want.Provider {
			continue
		}
		if want.Version != "" && got.Version != want.Version {
			continue
		}
		if want.ExecutionSecurityTier != "" && got.ExecutionSecurityTier != want.ExecutionSecurityTier {
			continue
		}
		if want.ProofTier != "" && got.ProofTier != want.ProofTier {
			continue
		}
		if want.ImageDigest != "" && got.ImageDigest != want.ImageDigest {
			continue
		}
		if want.RootFSDigest != "" && got.RootFSDigest != want.RootFSDigest {
			continue
		}
		return true
	}
	return false
}

func isZeroMetadata(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return true
	}
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if reflected.IsNil() {
			return true
		}
	}
	return reflected.IsZero()
}

func runWorkload(workload Workload, outputPath string) error {
	if err := validateWorkload(workload); err != nil {
		return err
	}

	htmlText, err := captureHTML(workload)
	if err != nil {
		return err
	}
	snap, err := snapshot.ExtractAmazon(htmlText, snapshot.ExtractOptions{
		URL:        workload.URL,
		CapturedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if workload.MaxImageCount > 0 && len(snap.Images) > workload.MaxImageCount {
		snap.Images = snap.Images[:workload.MaxImageCount]
	}
	return writeSnapshot(outputPath, snap)
}

func readRequest(path string) (Request, error) {
	f, err := os.Open(path)
	if err != nil {
		return Request{}, fmt.Errorf("open request: %w", err)
	}
	defer f.Close()

	var req Request
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Request{}, errors.New("decode request: multiple JSON values")
	}
	return req, nil
}

func validateWorkload(w Workload) error {
	parsed, err := url.Parse(w.URL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	host := canonicalHost(parsed.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return fmt.Errorf("unsupported host %q", parsed.Hostname())
	}
	if len(w.AllowedHosts) == 0 {
		return errors.New("allowed_hosts is required")
	}
	allowed := false
	for _, raw := range w.AllowedHosts {
		if canonicalHost(raw) == host {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("url host %q is not in allowed_hosts", host)
	}
	if _, ok := supportedAmazonHosts[host]; !ok {
		return fmt.Errorf("unsupported host %q", host)
	}
	if w.CaptureMode != "" && w.CaptureMode != CaptureModeBrowser && w.CaptureMode != CaptureModeMeta {
		return fmt.Errorf("unsupported capture_mode %q", w.CaptureMode)
	}
	if w.TimeoutSeconds < 0 {
		return errors.New("timeout_seconds cannot be negative")
	}
	if w.MaxHTMLBytes < 0 {
		return errors.New("max_html_bytes cannot be negative")
	}
	if w.MaxImageCount < 0 {
		return errors.New("max_image_count cannot be negative")
	}
	return nil
}

func canonicalHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}

func captureHTML(w Workload) (string, error) {
	if fixture := os.Getenv("PRODUCT_CAPTURE_HTML_FIXTURE"); fixture != "" {
		return readBoundedFile(fixture, maxHTMLBytes(w.MaxHTMLBytes))
	}
	return captureHTMLWithPlaywright(w)
}

func readBoundedFile(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open html fixture: %w", err)
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, f, maxBytes+1); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read html fixture: %w", err)
	}
	if int64(buf.Len()) > maxBytes {
		return "", fmt.Errorf("html fixture exceeds max_html_bytes %d", maxBytes)
	}
	return buf.String(), nil
}

func captureHTMLWithPlaywright(w Workload) (string, error) {
	timeout := time.Duration(timeoutSeconds(w.TimeoutSeconds)) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()

	scriptPath, err := writePlaywrightScript()
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(filepath.Dir(scriptPath))

	cmd := exec.CommandContext(ctx, "node", scriptPath, w.URL, fmt.Sprintf("%d", timeout.Milliseconds()))
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	var stdout limitedBuffer
	stdout.max = maxHTMLBytes(w.MaxHTMLBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("playwright capture failed: %s", msg)
	}
	return stdout.String(), nil
}

func writePlaywrightScript() (string, error) {
	dir, err := os.MkdirTemp("", "product-capture-playwright-*")
	if err != nil {
		return "", fmt.Errorf("create playwright temp dir: %w", err)
	}
	path := filepath.Join(dir, "capture.js")
	if err := os.WriteFile(path, []byte(playwrightCaptureScript), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("write playwright script: %w", err)
	}
	return path, nil
}

func timeoutSeconds(value int) int {
	if value <= 0 {
		return 45
	}
	return min(value, 300)
}

func maxHTMLBytes(value int64) int64 {
	if value <= 0 {
		return 2 << 20
	}
	return min(value, 10<<20)
}

func writeSnapshot(path string, snap snapshot.Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	return nil
}

type limitedBuffer struct {
	buf bytes.Buffer
	max int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len()+len(p)) > b.max {
		return 0, fmt.Errorf("capture output exceeds max_html_bytes %d", b.max)
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func supportedHosts() []string {
	out := make([]string, 0, len(supportedAmazonHosts))
	for host := range supportedAmazonHosts {
		out = append(out, host)
	}
	slices.Sort(out)
	return out
}

const playwrightCaptureScript = `
const { chromium } = require('playwright');

async function launchChromeBrowser() {
  return await chromium.launch({
    channel: 'chrome',
    headless: true,
    args: [
      '--disable-blink-features=AutomationControlled',
      '--no-sandbox',
      '--disable-setuid-sandbox',
      '--disable-dev-shm-usage',
    ],
  });
}

function isTransientNavigationError(err) {
  const message = err && (err.stack || err.message) ? String(err.stack || err.message) : String(err);
  return [
    'Timeout',
    'net::ERR_NETWORK_CHANGED',
    'net::ERR_NETWORK_RESET',
    'net::ERR_TIMED_OUT',
  ].some((needle) => message.includes(needle));
}

async function productTitleReady(page) {
  return await page.evaluate(() => {
    const visibleTitle = document.querySelector('span#productTitle');
    if (visibleTitle && visibleTitle.textContent && visibleTitle.textContent.trim()) return true;
    const hiddenTitle = document.querySelector('input#productTitle');
    return Boolean(hiddenTitle && hiddenTitle.value && hiddenTitle.value.trim());
  }).catch(() => false);
}

function remainingTimeout(deadline) {
  return Math.max(0, deadline - Date.now());
}

function requireTimeout(deadline, label, max = Infinity) {
  const timeout = Math.min(remainingTimeout(deadline), max);
  if (timeout <= 0) throw new Error(label + ' timed out');
  return timeout;
}

async function waitForProductTitle(page, deadline) {
  const timeout = remainingTimeout(deadline);
  if (timeout <= 0) return await productTitleReady(page);
  return await page.waitForFunction(() => {
    const visibleTitle = document.querySelector('span#productTitle');
    if (visibleTitle && visibleTitle.textContent && visibleTitle.textContent.trim()) return true;
    const hiddenTitle = document.querySelector('input#productTitle');
    return Boolean(hiddenTitle && hiddenTitle.value && hiddenTitle.value.trim());
  }, { timeout }).then(() => true).catch(() => productTitleReady(page));
}

async function gotoWithTransientRetry(page, url, deadline) {
  let lastErr;
  for (let attempt = 0; attempt < 3 && remainingTimeout(deadline) > 0; attempt++) {
    const budget = remainingTimeout(deadline);
    if (budget <= 0) break;
    const timeout = Math.min(budget, attempt === 0 ? Math.max(15000, Math.floor(budget * 0.65)) : budget);
    try {
      await page.goto(url, { waitUntil: 'domcontentloaded', timeout });
      return;
    } catch (err) {
      lastErr = err;
      if (!isTransientNavigationError(err)) {
        throw err;
      }
      if (await productTitleReady(page)) return;
      if (page.url() && page.url() !== 'about:blank') {
        const loadTimeout = Math.min(5000, remainingTimeout(deadline));
        if (loadTimeout > 0) await page.waitForLoadState('domcontentloaded', { timeout: loadTimeout }).catch(() => {});
        if (await waitForProductTitle(page, deadline)) return;
      }
      if (attempt < 2) {
        const backoff = Math.min(500 * (attempt + 1), remainingTimeout(deadline));
        if (backoff > 0) await page.waitForTimeout(backoff);
      }
    }
  }
  if (!lastErr) throw new Error('navigation timed out before capture started');
  throw lastErr;
}

async function main() {
  const url = process.argv[2];
  const timeout = Number(process.argv[3] || 45000);
  const deadline = Date.now() + timeout;
  const browser = await launchChromeBrowser();
  const page = await browser.newPage({
    userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
  });
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
  });
  try {
    await gotoWithTransientRetry(page, url, deadline);
    if (await page.locator('form[action*="/errors/validateCaptcha"]').count()) {
      throw new Error('amazon interstitial requires manual review');
    }
    await page.waitForFunction(() => {
      const visibleTitle = document.querySelector('span#productTitle');
      if (visibleTitle && visibleTitle.textContent && visibleTitle.textContent.trim()) return true;
      const hiddenTitle = document.querySelector('input#productTitle');
      return Boolean(hiddenTitle && hiddenTitle.value && hiddenTitle.value.trim());
    }, { timeout: requireTimeout(deadline, 'product title wait', 15000) });
    const optionalWait = Math.min(remainingTimeout(deadline), 5000);
    if (optionalWait > 0) {
      await page.waitForFunction(() => {
        const text = (selector) => {
          const node = document.querySelector(selector);
          return node && node.textContent && node.textContent.trim();
        };
        const attr = (selector, name) => {
          const node = document.querySelector(selector);
          return node && node.getAttribute(name);
        };
        return Boolean(
          attr('#landingImage', 'src') ||
          attr('#landingImage', 'data-a-dynamic-image') ||
          text('#corePrice_feature_div .a-offscreen') ||
          text('#apex_desktop .a-offscreen') ||
          text('.apexPriceToPay .a-offscreen') ||
          text('.priceToPay .a-offscreen') ||
          text('#mir-layout-DELIVERY_BLOCK-slot-PRIMARY_DELIVERY_MESSAGE_LARGE') ||
          text('#mir-layout-DELIVERY_BLOCK-slot-SECONDARY_DELIVERY_MESSAGE_LARGE') ||
          text('#deliveryBlockMessage') ||
          text('#primeShippingMessage_feature_div')
        );
      }, { timeout: optionalWait }).catch(() => {});
    }
    process.stdout.write(await page.content());
  } finally {
    await browser.close();
  }
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
});
`
