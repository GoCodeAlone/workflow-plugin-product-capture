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
	"slices"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/snapshot"
)

const (
	ProviderName       = "workflow-plugin-product-capture"
	ExecutorProvider   = "product-capture-browser"
	WorkloadKind       = "product-capture"
	CaptureModeBrowser = "browser"
	CaptureModeMeta    = "metadata"
)

var Version = "0.1.0"

var supportedAmazonHosts = map[string]struct{}{
	"amazon.com":     {},
	"www.amazon.com": {},
}

type Request struct {
	Workload Workload `json:"workload"`
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
		RuntimeTools:          []string{"node", "playwright"},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func Main(args []string, stdout, stderr io.Writer) int {
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
	if *requestPath == "" || *outputPath == "" {
		fmt.Fprintln(stderr, "--request and --output are required")
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
	if err := validateWorkload(req.Workload); err != nil {
		return err
	}

	htmlText, err := captureHTML(req.Workload)
	if err != nil {
		return err
	}
	snap, err := snapshot.ExtractAmazon(htmlText, snapshot.ExtractOptions{
		URL:        req.Workload.URL,
		CapturedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if req.Workload.MaxImageCount > 0 && len(snap.Images) > req.Workload.MaxImageCount {
		snap.Images = snap.Images[:req.Workload.MaxImageCount]
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

async function main() {
  const url = process.argv[2];
  const timeout = Number(process.argv[3] || 45000);
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  try {
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout });
    const button = page.locator('form[action*="/errors/validateCaptcha"] button, form[action*="/errors/validateCaptcha"] input[type="submit"], button:has-text("Continue shopping")').first();
    if (await button.count()) {
      await button.click({ timeout: Math.min(timeout, 10000) });
      await page.waitForLoadState('domcontentloaded', { timeout });
    }
    if (await page.locator('form[action*="/errors/validateCaptcha"]').count()) {
      throw new Error('amazon interstitial still present after continue');
    }
    await page.locator('#productTitle').waitFor({ timeout: Math.min(timeout, 15000) });
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
