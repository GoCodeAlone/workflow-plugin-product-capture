package conformance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
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

	"golang.org/x/net/dns/dnsmessage"
)

const (
	SchemaV1                       = "v1"
	VerdictPass                    = "pass"
	VerdictFail                    = "fail"
	MaxObservationBytes            = 64 << 10
	MaxPageBytes                   = 16 << 10
	CloudflaredVersion             = "2026.7.1"
	CloudflaredSHA256              = "79a0ade7fc854f62c1aaef48424d9d979e8c2fcd039189d24db82b84cd146be1"
	CloudflaredDownloadURL         = "https://github.com/cloudflare/cloudflared/releases/download/" + CloudflaredVersion + "/cloudflared-linux-amd64"
	diagnosticDNSResolverIP        = "1.1.1.1"
	diagnosticDNSResolverAddress   = diagnosticDNSResolverIP + ":53"
	candidateStopSeconds           = 10
	candidateReapGrace             = 12 * time.Second
	candidateStopCommandTimeout    = 15 * time.Second
	candidateForceRemoveTimeout    = 5 * time.Second
	candidateFinalRemoveTimeout    = 5 * time.Second
	candidateInspectTimeout        = 5 * time.Second
	candidateLifecycleCleanupBound = candidateStopCommandTimeout + candidateReapGrace + candidateForceRemoveTimeout + candidateReapGrace + candidateFinalRemoveTimeout + candidateInspectTimeout
	launchCleanupWaitTimeout       = candidateLifecycleCleanupBound + 5*time.Second
	defaultConformanceTimeout      = 12 * time.Minute
	managedDirectDNSRetryWindow    = 15 * time.Second
	managedDirectDNSRetryInterval  = 250 * time.Millisecond
	diagnosticDNSQueryTimeout      = 5 * time.Second
	maxDiagnosticDNSResponseBytes  = 65535
	maxDiagnosticDNSCNAMEHops      = 16
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

type ScreenSignals struct {
	Width  int `json:"width"`
	Height int `json:"height"`
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
	Screen     ScreenSignals     `json:"screen"`
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
	HeaderNames []string          `json:"header_names,omitempty"`
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

type FailureClass string

const (
	failureClassObservationSchema         FailureClass = "observation.schema"
	failureClassObservationRunCorrelation FailureClass = "observation.run_correlation"
	failureClassObservationOrder          FailureClass = "observation.order"
	failureClassDirectInvalidEvidence     FailureClass = "direct.invalid_stable_evidence"
	failureClassAttachedInvalidEvidence   FailureClass = "attached.invalid_stable_evidence"
	failureClassAutomationGlobalsPresent  FailureClass = "browser.automation.globals_present"
	failureClassReportValidation          FailureClass = "report.validation"
)

const (
	comparisonFieldNavigatorWebdriver        = "browser.navigator.webdriver"
	comparisonFieldNavigatorUserAgent        = "browser.navigator.user_agent"
	comparisonFieldNavigatorBrands           = "browser.navigator.user_agent_data.brands"
	comparisonFieldNavigatorUAPlatform       = "browser.navigator.user_agent_data.platform"
	comparisonFieldNavigatorLanguage         = "browser.navigator.language"
	comparisonFieldNavigatorLanguages        = "browser.navigator.languages"
	comparisonFieldNavigatorPlatform         = "browser.navigator.platform"
	comparisonFieldPlaywrightBinding         = "browser.automation.playwright_binding_present"
	comparisonFieldPlaywrightInitScripts     = "browser.automation.playwright_init_scripts_present"
	comparisonFieldRequestUserAgent          = "request.user_agent"
	comparisonFieldRequestClientHintBrands   = "request.client_hints.brands"
	comparisonFieldRequestClientHintMobile   = "request.client_hints.mobile"
	comparisonFieldRequestClientHintPlatform = "request.client_hints.platform"
	comparisonFieldRequestSecFetchDest       = "request.sec_fetch.dest"
	comparisonFieldRequestSecFetchMode       = "request.sec_fetch.mode"
	comparisonFieldRequestSecFetchSite       = "request.sec_fetch.site"
	comparisonFieldRequestSecFetchUser       = "request.sec_fetch.user"
	comparisonFieldFirstNavigationOrigin     = "first_navigation_origin"
	comparisonFieldWindowOuterWidth          = "browser.window.outer_width"
	comparisonFieldWindowOuterHeight         = "browser.window.outer_height"
	comparisonFieldWindowInnerWidth          = "browser.window.inner_width"
	comparisonFieldScreenWidth               = "browser.screen.width"
	comparisonFieldScreenHeight              = "browser.screen.height"
)

func validFailureClass(value FailureClass) bool {
	switch value {
	case failureClassObservationSchema,
		failureClassObservationRunCorrelation,
		failureClassObservationOrder,
		failureClassDirectInvalidEvidence,
		failureClassAttachedInvalidEvidence,
		failureClassAutomationGlobalsPresent,
		failureClassReportValidation:
		return true
	default:
		return false
	}
}

func validComparisonField(value string) bool {
	switch value {
	case comparisonFieldNavigatorWebdriver,
		comparisonFieldNavigatorUserAgent,
		comparisonFieldNavigatorBrands,
		comparisonFieldNavigatorUAPlatform,
		comparisonFieldNavigatorLanguage,
		comparisonFieldNavigatorLanguages,
		comparisonFieldNavigatorPlatform,
		comparisonFieldPlaywrightBinding,
		comparisonFieldPlaywrightInitScripts,
		comparisonFieldRequestUserAgent,
		comparisonFieldRequestClientHintBrands,
		comparisonFieldRequestClientHintMobile,
		comparisonFieldRequestClientHintPlatform,
		comparisonFieldRequestSecFetchDest,
		comparisonFieldRequestSecFetchMode,
		comparisonFieldRequestSecFetchSite,
		comparisonFieldRequestSecFetchUser,
		comparisonFieldFirstNavigationOrigin,
		comparisonFieldWindowOuterWidth,
		comparisonFieldWindowOuterHeight,
		comparisonFieldWindowInnerWidth,
		comparisonFieldScreenWidth,
		comparisonFieldScreenHeight:
		return true
	default:
		return false
	}
}

type Report struct {
	Schema            string                       `json:"schema"`
	Versions          Versions                     `json:"versions"`
	StableComparisons []Comparison                 `json:"stable_comparisons"`
	Informational     map[string]InformationalPair `json:"informational"`
	Errors            []string                     `json:"errors,omitempty"`
	FailureClasses    []FailureClass               `json:"failure_classes,omitempty"`
	Verdict           string                       `json:"verdict"`
}

func normalizedBrandSet(brands []Brand) []Brand {
	normalized := append([]Brand{}, brands...)
	slices.SortFunc(normalized, func(left, right Brand) int {
		if order := strings.Compare(left.Brand, right.Brand); order != 0 {
			return order
		}
		return strings.Compare(left.Version, right.Version)
	})
	return slices.CompactFunc(normalized, func(left, right Brand) bool {
		return left.Brand == right.Brand && left.Version == right.Version
	})
}

func normalizedStringSet(values []string) []string {
	normalized := append([]string{}, values...)
	slices.Sort(normalized)
	return slices.Compact(normalized)
}

func parseClientHintBrandSet(value string) ([]Brand, bool) {
	index := 0
	brands := make([]Brand, 0, 4)
	for {
		skipOptionalWhitespace(value, &index)
		brand, ok := parseClientHintQuotedString(value, &index)
		if !ok || brand == "" {
			return nil, false
		}
		skipOptionalWhitespace(value, &index)
		if index >= len(value) || value[index] != ';' {
			return nil, false
		}
		index++
		skipOptionalWhitespace(value, &index)
		if index >= len(value) || value[index] != 'v' {
			return nil, false
		}
		index++
		skipOptionalWhitespace(value, &index)
		if index >= len(value) || value[index] != '=' {
			return nil, false
		}
		index++
		skipOptionalWhitespace(value, &index)
		version, ok := parseClientHintQuotedString(value, &index)
		if !ok || version == "" {
			return nil, false
		}
		brands = append(brands, Brand{Brand: brand, Version: version})
		skipOptionalWhitespace(value, &index)
		if index == len(value) {
			return normalizedBrandSet(brands), true
		}
		if value[index] != ',' {
			return nil, false
		}
		index++
	}
}

func skipOptionalWhitespace(value string, index *int) {
	for *index < len(value) && (value[*index] == ' ' || value[*index] == '\t') {
		*index++
	}
}

func parseClientHintQuotedString(value string, index *int) (string, bool) {
	if *index >= len(value) || value[*index] != '"' {
		return "", false
	}
	*index++
	var parsed strings.Builder
	for *index < len(value) {
		char := value[*index]
		*index++
		switch char {
		case '"':
			return parsed.String(), true
		case '\\':
			if *index >= len(value) {
				return "", false
			}
			parsed.WriteByte(value[*index])
			*index++
		default:
			if char < 0x20 || char == 0x7f {
				return "", false
			}
			parsed.WriteByte(char)
		}
	}
	return "", false
}

func (r Report) ExitCode() int {
	if r.Verdict == VerdictPass {
		return 0
	}
	return 1
}

func conformanceFailureError(report Report) error {
	const maxLabels = 12
	labels := make([]string, 0, len(report.FailureClasses)+len(report.StableComparisons))
	appendLabel := func(label string) {
		if !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	for _, failureClass := range report.FailureClasses {
		if validFailureClass(failureClass) {
			appendLabel(string(failureClass))
		} else {
			appendLabel(string(failureClassReportValidation))
		}
	}
	if len(report.Errors) > 0 {
		appendLabel(string(failureClassReportValidation))
	}
	for _, comparison := range report.StableComparisons {
		if !comparison.Match {
			if validComparisonField(comparison.Field) {
				appendLabel(comparison.Field)
			} else {
				appendLabel(string(failureClassReportValidation))
			}
		}
	}
	if len(labels) > maxLabels {
		visible := append([]string(nil), labels[:maxLabels-1]...)
		visible = append(visible, fmt.Sprintf("additional_labels:%d", len(labels)-len(visible)))
		labels = visible
	}
	if len(labels) == 0 {
		return errors.New("browser runtime conformance failed")
	}
	return fmt.Errorf("browser runtime conformance failed: %s", strings.Join(labels, ", "))
}

func stableEvidenceErrors(label string, observation Observation) []string {
	prefix := label + " observation has invalid "
	var result []string
	navigator := observation.Browser.Navigator
	if strings.TrimSpace(navigator.UserAgent) == "" {
		result = append(result, prefix+"navigator user_agent")
	}
	if len(navigator.UserAgentData.Brands) == 0 {
		result = append(result, prefix+"navigator brand set")
	} else {
		for _, brand := range navigator.UserAgentData.Brands {
			if strings.TrimSpace(brand.Brand) == "" || strings.TrimSpace(brand.Version) == "" {
				result = append(result, prefix+"navigator brand set")
				break
			}
		}
	}
	if strings.TrimSpace(navigator.UserAgentData.Platform) == "" {
		result = append(result, prefix+"navigator user-agent-data platform")
	}
	if strings.TrimSpace(navigator.Language) == "" {
		result = append(result, prefix+"navigator language")
	}
	if len(navigator.Languages) == 0 {
		result = append(result, prefix+"navigator language set")
	} else {
		for _, language := range navigator.Languages {
			if strings.TrimSpace(language) == "" {
				result = append(result, prefix+"navigator language set")
				break
			}
		}
	}
	if strings.TrimSpace(navigator.Platform) == "" {
		result = append(result, prefix+"navigator platform")
	}
	window := observation.Browser.Window
	if window.OuterWidth <= 0 || window.OuterHeight <= 0 || window.InnerWidth <= 0 {
		result = append(result, prefix+"window dimensions")
	}
	screen := observation.Browser.Screen
	if screen.Width <= 0 || screen.Height <= 0 {
		result = append(result, prefix+"screen dimensions")
	}
	if strings.TrimSpace(observation.Request.UserAgent) == "" {
		result = append(result, prefix+"request user_agent")
	}
	requestBrands, validRequestBrands := parseClientHintBrandSet(observation.Request.ClientHints.Brands)
	if !validRequestBrands || len(requestBrands) == 0 {
		result = append(result, prefix+"request client-hint brand set")
	}
	if mobile := strings.TrimSpace(observation.Request.ClientHints.Mobile); mobile != "?0" && mobile != "?1" {
		result = append(result, prefix+"request client-hint mobile")
	}
	if strings.Trim(strings.TrimSpace(observation.Request.ClientHints.Platform), `"`) == "" {
		result = append(result, prefix+"request client-hint platform")
	}
	secFetch := observation.Request.SecFetch
	if strings.TrimSpace(secFetch.Dest) != "document" {
		result = append(result, prefix+"request sec-fetch destination")
	}
	if strings.TrimSpace(secFetch.Mode) != "navigate" {
		result = append(result, prefix+"request sec-fetch mode")
	}
	switch strings.TrimSpace(secFetch.Site) {
	case "cross-site", "same-origin", "same-site", "none":
	default:
		result = append(result, prefix+"request sec-fetch site")
	}
	if strings.TrimSpace(secFetch.User) != "?1" {
		result = append(result, prefix+"request sec-fetch user")
	}
	origin, err := url.Parse(observation.FirstNavigationOrigin)
	if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		result = append(result, prefix+"first navigation origin")
	}
	return result
}

func Compare(direct, attached Observation, versions Versions) Report {
	report := Report{
		Schema:        SchemaV1,
		Versions:      versions,
		Informational: make(map[string]InformationalPair),
		Verdict:       VerdictPass,
	}
	addFailureClass := func(failureClass FailureClass) {
		if !slices.Contains(report.FailureClasses, failureClass) {
			report.FailureClasses = append(report.FailureClasses, failureClass)
		}
	}
	if direct.Schema != SchemaV1 || attached.Schema != SchemaV1 {
		report.Errors = append(report.Errors, fmt.Sprintf("both observations must use schema %q", SchemaV1))
		addFailureClass(failureClassObservationSchema)
	}
	if direct.RunID == "" || direct.RunID != attached.RunID {
		report.Errors = append(report.Errors, "direct and attached observations must have the same nonempty run_id")
		addFailureClass(failureClassObservationRunCorrelation)
	}
	if direct.Kind != "direct" || attached.Kind != "attached" {
		report.Errors = append(report.Errors, "observations must be ordered direct then attached")
		addFailureClass(failureClassObservationOrder)
	}
	directEvidenceErrors := stableEvidenceErrors("direct", direct)
	report.Errors = append(report.Errors, directEvidenceErrors...)
	if len(directEvidenceErrors) > 0 {
		addFailureClass(failureClassDirectInvalidEvidence)
	}
	attachedEvidenceErrors := stableEvidenceErrors("attached", attached)
	report.Errors = append(report.Errors, attachedEvidenceErrors...)
	if len(attachedEvidenceErrors) > 0 {
		addFailureClass(failureClassAttachedInvalidEvidence)
	}

	addExact := func(field string, directValue, attachedValue any) {
		report.StableComparisons = append(report.StableComparisons, Comparison{
			Field: field, Direct: directValue, Attached: attachedValue, Match: reflect.DeepEqual(directValue, attachedValue),
		})
	}
	addSet := func(field string, directValue, attachedValue, normalizedDirect, normalizedAttached any, valid bool) {
		report.StableComparisons = append(report.StableComparisons, Comparison{
			Field: field, Direct: directValue, Attached: attachedValue, Match: valid && reflect.DeepEqual(normalizedDirect, normalizedAttached),
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

	addExact(comparisonFieldNavigatorWebdriver, direct.Browser.Navigator.Webdriver, attached.Browser.Navigator.Webdriver)
	addExact(comparisonFieldNavigatorUserAgent, direct.Browser.Navigator.UserAgent, attached.Browser.Navigator.UserAgent)
	addSet(
		comparisonFieldNavigatorBrands,
		direct.Browser.Navigator.UserAgentData.Brands,
		attached.Browser.Navigator.UserAgentData.Brands,
		normalizedBrandSet(direct.Browser.Navigator.UserAgentData.Brands),
		normalizedBrandSet(attached.Browser.Navigator.UserAgentData.Brands),
		true,
	)
	addExact(comparisonFieldNavigatorUAPlatform, direct.Browser.Navigator.UserAgentData.Platform, attached.Browser.Navigator.UserAgentData.Platform)
	addExact(comparisonFieldNavigatorLanguage, direct.Browser.Navigator.Language, attached.Browser.Navigator.Language)
	addSet(
		comparisonFieldNavigatorLanguages,
		direct.Browser.Navigator.Languages,
		attached.Browser.Navigator.Languages,
		normalizedStringSet(direct.Browser.Navigator.Languages),
		normalizedStringSet(attached.Browser.Navigator.Languages),
		true,
	)
	addExact(comparisonFieldNavigatorPlatform, direct.Browser.Navigator.Platform, attached.Browser.Navigator.Platform)
	addExact(comparisonFieldPlaywrightBinding, direct.Browser.Automation.PlaywrightBindingPresent, attached.Browser.Automation.PlaywrightBindingPresent)
	addExact(comparisonFieldPlaywrightInitScripts, direct.Browser.Automation.PlaywrightInitScriptsPresent, attached.Browser.Automation.PlaywrightInitScriptsPresent)
	addExact(comparisonFieldRequestUserAgent, direct.Request.UserAgent, attached.Request.UserAgent)
	directClientHintBrands, directClientHintBrandsValid := parseClientHintBrandSet(direct.Request.ClientHints.Brands)
	attachedClientHintBrands, attachedClientHintBrandsValid := parseClientHintBrandSet(attached.Request.ClientHints.Brands)
	addSet(
		comparisonFieldRequestClientHintBrands,
		direct.Request.ClientHints.Brands,
		attached.Request.ClientHints.Brands,
		directClientHintBrands,
		attachedClientHintBrands,
		directClientHintBrandsValid && attachedClientHintBrandsValid,
	)
	addExact(comparisonFieldRequestClientHintMobile, direct.Request.ClientHints.Mobile, attached.Request.ClientHints.Mobile)
	addExact(comparisonFieldRequestClientHintPlatform, direct.Request.ClientHints.Platform, attached.Request.ClientHints.Platform)
	addExact(comparisonFieldRequestSecFetchDest, direct.Request.SecFetch.Dest, attached.Request.SecFetch.Dest)
	addExact(comparisonFieldRequestSecFetchMode, direct.Request.SecFetch.Mode, attached.Request.SecFetch.Mode)
	addExact(comparisonFieldRequestSecFetchSite, direct.Request.SecFetch.Site, attached.Request.SecFetch.Site)
	addExact(comparisonFieldRequestSecFetchUser, direct.Request.SecFetch.User, attached.Request.SecFetch.User)
	originMatch := direct.FirstNavigationOrigin == attached.FirstNavigationOrigin
	report.StableComparisons = append(report.StableComparisons, Comparison{
		Field: comparisonFieldFirstNavigationOrigin, Direct: "<redacted-origin>", Attached: "<redacted-origin>", Match: originMatch,
	})
	addWindow(comparisonFieldWindowOuterWidth, direct.Browser.Window.OuterWidth, attached.Browser.Window.OuterWidth)
	addWindow(comparisonFieldWindowOuterHeight, direct.Browser.Window.OuterHeight, attached.Browser.Window.OuterHeight)
	addWindow(comparisonFieldWindowInnerWidth, direct.Browser.Window.InnerWidth, attached.Browser.Window.InnerWidth)
	addWindow(comparisonFieldScreenWidth, direct.Browser.Screen.Width, attached.Browser.Screen.Width)
	addWindow(comparisonFieldScreenHeight, direct.Browser.Screen.Height, attached.Browser.Screen.Height)

	report.Informational["request.header_names"] = InformationalPair{Direct: direct.Request.HeaderNames, Attached: attached.Request.HeaderNames}
	report.Informational["timing"] = InformationalPair{Direct: direct.Timing, Attached: attached.Timing}
	report.Informational["browser.webgl"] = InformationalPair{Direct: direct.Browser.WebGL, Attached: attached.Browser.WebGL}
	report.Informational["browser.navigator.hardware_concurrency"] = InformationalPair{Direct: direct.Browser.Navigator.HardwareConcurrency, Attached: attached.Browser.Navigator.HardwareConcurrency}
	report.Informational["browser.navigator.device_memory"] = InformationalPair{Direct: direct.Browser.Navigator.DeviceMemory, Attached: attached.Browser.Navigator.DeviceMemory}
	report.Informational["browser.document.cookie_present"] = InformationalPair{Direct: direct.Browser.Document.CookiePresent, Attached: attached.Browser.Document.CookiePresent}
	report.Informational["browser.document.cookie_length"] = InformationalPair{Direct: direct.Browser.Document.CookieLength, Attached: attached.Browser.Document.CookieLength}
	report.Informational["browser.window.inner_height"] = InformationalPair{Direct: direct.Browser.Window.InnerHeight, Attached: attached.Browser.Window.InnerHeight}

	for _, comparison := range report.StableComparisons {
		if !comparison.Match {
			report.Verdict = VerdictFail
		}
	}
	if direct.Browser.Automation.PlaywrightBindingPresent || direct.Browser.Automation.PlaywrightInitScriptsPresent ||
		attached.Browser.Automation.PlaywrightBindingPresent || attached.Browser.Automation.PlaywrightInitScriptsPresent {
		report.Errors = append(report.Errors, "checked Playwright automation globals must be absent")
		addFailureClass(failureClassAutomationGlobalsPresent)
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
		payload, err := decodeDiagnosticPayload(body)
		if err != nil || payload.Source != "product_capture_browser_diagnostic" {
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

func decodeDiagnosticPayload(body []byte) (diagnosticPayload, error) {
	var required struct {
		BrowserSignals struct {
			Navigator struct {
				Webdriver *bool `json:"webdriver"`
			} `json:"navigator"`
			Automation struct {
				PlaywrightBindingPresent     *bool `json:"playwright_binding_present"`
				PlaywrightInitScriptsPresent *bool `json:"playwright_init_scripts_present"`
			} `json:"automation"`
		} `json:"browser_signals"`
	}
	if len(body) == 0 {
		return diagnosticPayload{}, errors.New("observation is empty")
	}
	if err := json.Unmarshal(body, &required); err != nil {
		return diagnosticPayload{}, err
	}
	if required.BrowserSignals.Navigator.Webdriver == nil ||
		required.BrowserSignals.Automation.PlaywrightBindingPresent == nil ||
		required.BrowserSignals.Automation.PlaywrightInitScriptsPresent == nil {
		return diagnosticPayload{}, errors.New("stable automation signals must be explicit booleans")
	}
	var payload diagnosticPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return diagnosticPayload{}, err
	}
	return payload, nil
}

func (c *Collector) recordNavigation(kind string, r *http.Request) {
	headerNames := make([]string, 0, len(r.Header))
	for key := range r.Header {
		headerNames = append(headerNames, strings.ToLower(key))
	}
	slices.Sort(headerNames)
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
			HeaderNames: headerNames,
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
		screen: { width: safe(() => screen.width, 0), height: safe(() => screen.height, 0) },
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
	TunnelHTTPClient    *http.Client
	TunnelHealthTimeout time.Duration
	HealthRetryWait     diagnosticHealthRetryWait
	Listen              func(string, string) (net.Listener, error)
	LaunchDirect        func(context.Context, string, string, bool) error
	LaunchAttached      func(context.Context, string, string, bool) error
	ValidateLifecycle   func(context.Context, string, string, bool) error
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

type redactedManagedTunnelError struct {
	cause   error
	message string
}

func (e redactedManagedTunnelError) Error() string { return e.message }
func (e redactedManagedTunnelError) Unwrap() error { return e.cause }

type redactedTunnelCleanupError struct{ cause error }

func (e redactedTunnelCleanupError) Error() string { return errDiagnosticTunnelCleanupFailed.Error() }
func (e redactedTunnelCleanupError) Unwrap() []error {
	return []error{errDiagnosticTunnelCleanupFailed, e.cause}
}

type redactedDiagnosticHealthError struct {
	classification error
	cause          error
}

func (e redactedDiagnosticHealthError) Error() string { return e.classification.Error() }
func (e redactedDiagnosticHealthError) Unwrap() []error {
	return []error{e.classification, e.cause}
}

var (
	errTunnelActivationTimeout             = errors.New("cloudflared timed out before publishing an origin")
	errTunnelExitedBeforeOrigin            = errors.New("cloudflared exited before publishing an origin")
	errDiagnosticDNSResolverUnavailable    = errors.New("diagnostic DNS resolver unavailable")
	errDiagnosticDNSPublicationNotReady    = errors.New("diagnostic DNS publication not ready")
	errDiagnosticNonPublicIPv4             = errors.New("diagnostic DNS resolved to non-public IPv4 address")
	errDiagnosticHealthEndpointRejected    = errors.New("run-correlated diagnostic health endpoint rejected")
	errDiagnosticHealthEndpointUnreachable = errors.New("fetch run-correlated diagnostic health endpoint failed")
	errDiagnosticHealthResponseTooLarge    = errors.New("diagnostic health response exceeds 4096 bytes")
	errDiagnosticTunnelCleanupFailed       = errors.New("diagnostic tunnel cleanup failed")
)

const maxDiagnosticHealthResponseBytes = 4096

func retryableTunnelStartError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) ||
		errors.Is(err, errTunnelActivationTimeout) ||
		errors.Is(err, errTunnelExitedBeforeOrigin)
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
	listen := r.Dependencies.Listen
	if listen == nil {
		listen = net.Listen
	}
	listener, err := listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen for diagnostic endpoint: %w", err)
	}
	server := &http.Server{Handler: collector.Handler(), ReadHeaderTimeout: 5 * time.Second}
	runCtx, cancelRun := context.WithCancel(ctx)
	ctx = runCtx
	serveResult := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		if err != nil {
			cancelRun()
		}
		serveResult <- err
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		var serveErr error
		select {
		case serveErr = <-serveResult:
			if serveErr != nil {
				serveErr = fmt.Errorf("serve diagnostic endpoint: %w", serveErr)
			}
		case <-time.After(2 * time.Second):
			serveErr = errors.New("diagnostic server did not stop after shutdown")
		}
		cancelRun()
		runErr = errors.Join(runErr, shutdownErr, serveErr)
	}()

	client := r.Dependencies.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	healthRetryWait := r.Dependencies.HealthRetryWait
	if healthRetryWait == nil {
		healthRetryWait = waitForDiagnosticHealthRetry
	}
	origin := strings.TrimRight(strings.TrimSpace(options.Origin), "/")
	managedTunnel := origin == ""
	if managedTunnel {
		if r.Dependencies.Tunnel == nil {
			return errors.New("diagnostic tunnel dependency is unavailable")
		}
		_, port, splitErr := net.SplitHostPort(listener.Addr().String())
		if splitErr != nil {
			return fmt.Errorf("resolve diagnostic listen port: %w", splitErr)
		}
		healthTimeout := tunnelHealthTimeout(r.Dependencies.TunnelHealthTimeout)
		tunnelHealthClient := r.Dependencies.TunnelHTTPClient
		if tunnelHealthClient == nil {
			return errors.New("managed diagnostic tunnel health client is unavailable")
		}
		localURL := "http://127.0.0.1:" + port
		for attempt := 0; attempt < 3; attempt++ {
			candidate, startErr := r.Dependencies.Tunnel.Start(ctx, localURL)
			if startErr != nil {
				cleanupErr := stopTunnel(r.Dependencies.Tunnel)
				if cleanupErr != nil {
					return errors.Join(fmt.Errorf("start diagnostic tunnel: %w", startErr), cleanupErr)
				}
				if ctx.Err() != nil || attempt == 2 || !retryableTunnelStartError(startErr) {
					return fmt.Errorf("start diagnostic tunnel: %w", startErr)
				}
				continue
			}
			if err := validateDiagnosticOrigin(candidate); err != nil {
				return errors.Join(err, stopTunnel(r.Dependencies.Tunnel))
			}
			healthCtx, healthCancel := context.WithTimeout(ctx, healthTimeout)
			healthErr := fetchRunHealth(
				healthCtx,
				tunnelHealthClient,
				candidate+"/runs/"+runID+"/healthz",
				runID,
				500*time.Millisecond,
				true,
				healthRetryWait,
			)
			healthCancel()
			if healthErr == nil {
				origin = candidate
				break
			}
			if stopErr := stopTunnel(r.Dependencies.Tunnel); stopErr != nil {
				return errors.Join(healthErr, stopErr)
			}
			if errors.Is(healthErr, errDiagnosticDNSResolverUnavailable) ||
				!errors.Is(healthErr, context.DeadlineExceeded) || ctx.Err() != nil || attempt == 2 {
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
		if err := fetchRunHealth(
			healthCtx,
			client,
			origin+"/runs/"+runID+"/healthz",
			runID,
			500*time.Millisecond,
			false,
			healthRetryWait,
		); err != nil {
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
	if err := r.Dependencies.ValidateLifecycle(ctx, options.Image, origin, managedTunnel); err != nil {
		return fmt.Errorf("validate candidate lifecycle: %w", redactManagedTunnelError(err, managedTunnel, origin))
	}
	directTarget := origin + "/runs/" + runID + "/direct"
	direct, err := launchAndCollect(ctx, collector, "direct", options.Image, directTarget, managedTunnel, r.Dependencies.LaunchDirect)
	if err != nil {
		return redactManagedTunnelError(err, managedTunnel, directTarget, origin)
	}
	attachedTarget := origin + "/runs/" + runID + "/attached"
	attached, err := launchAndCollect(ctx, collector, "attached", options.Image, attachedTarget, managedTunnel, r.Dependencies.LaunchAttached)
	if err != nil {
		return redactManagedTunnelError(err, managedTunnel, attachedTarget, origin)
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
		return conformanceFailureError(report)
	}
	return nil
}

func tunnelHealthTimeout(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return 2 * time.Minute
}

func redactManagedTunnelError(err error, managedTunnel bool, values ...string) error {
	if err == nil || !managedTunnel {
		return err
	}
	message := err.Error()
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		secrets := diagnosticRedactionSecrets(value)
		for _, secret := range secrets {
			if secret != "" {
				message = replaceDiagnosticValue(message, secret)
			}
		}
	}
	return redactedManagedTunnelError{cause: err, message: message}
}

func diagnosticRedactionSecrets(value string) []string {
	secrets := []string{value}
	parsed, err := url.Parse(value)
	if err != nil {
		return secrets
	}
	secrets = append(secrets, parsed.Host, parsed.Hostname())
	if parsed.Path != "" && parsed.Path != "/" {
		secrets = append(secrets, parsed.Path)
	}
	if escapedPath := parsed.EscapedPath(); escapedPath != "" && escapedPath != "/" && escapedPath != parsed.Path {
		secrets = append(secrets, escapedPath)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index := 0; index+1 < len(segments); index++ {
		if strings.EqualFold(segments[index], "runs") && segments[index+1] != "" {
			secrets = append(secrets, segments[index+1])
			break
		}
	}
	return secrets
}

func replaceDiagnosticValue(message, secret string) string {
	pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(secret))
	if err != nil {
		return "<managed-diagnostic-origin>"
	}
	return pattern.ReplaceAllLiteralString(message, "<managed-diagnostic-origin>")
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
	if err := tunnel.Stop(stopCtx); err != nil {
		return redactedTunnelCleanupError{cause: err}
	}
	return nil
}

type diagnosticHealthRetryWait func(context.Context, time.Duration) error

func fetchRunHealth(
	ctx context.Context,
	client *http.Client,
	healthURL, runID string,
	retryInterval time.Duration,
	managedTunnel bool,
	retryWait diagnosticHealthRetryWait,
) error {
	if retryInterval <= 0 {
		retryInterval = 500 * time.Millisecond
	}
	if retryWait == nil {
		return errors.New("diagnostic health retry wait is unavailable")
	}
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(lastErr, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody)
		if err != nil {
			return fmt.Errorf("create diagnostic health request: %w", err)
		}
		resp, err := client.Do(req)
		ctxErr := ctx.Err()
		if err == nil {
			if ctxErr != nil {
				var closeErr error
				if resp != nil && resp.Body != nil {
					closeErr = resp.Body.Close()
				}
				return errors.Join(diagnosticHealthResponseCause(managedTunnel, closeErr), ctxErr)
			}
			retryable, responseErr := classifyRunHealthResponse(resp, runID, managedTunnel)
			if ctxErr = ctx.Err(); ctxErr != nil {
				return errors.Join(responseErr, ctxErr)
			}
			if responseErr == nil {
				return nil
			}
			if !retryable {
				return responseErr
			}
			lastErr = responseErr
		} else {
			if !managedTunnel {
				lastErr = errors.Join(errDiagnosticHealthEndpointUnreachable, err)
			} else {
				var dnsErr *net.DNSError
				switch {
				case errors.Is(err, errDiagnosticNonPublicIPv4):
					return errors.Join(redactedDiagnosticHealthError{classification: errDiagnosticNonPublicIPv4, cause: err}, ctxErr)
				case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
					lastErr = redactedDiagnosticHealthError{classification: errDiagnosticDNSPublicationNotReady, cause: err}
				case errors.As(err, &dnsErr) && retryableDiagnosticIPv4LookupError(err):
					lastErr = redactedDiagnosticHealthError{classification: errDiagnosticDNSResolverUnavailable, cause: err}
				case errors.As(err, &dnsErr):
					return errors.Join(redactedDiagnosticHealthError{classification: errDiagnosticDNSResolverUnavailable, cause: err}, ctxErr)
				default:
					lastErr = redactedDiagnosticHealthError{classification: errDiagnosticHealthEndpointUnreachable, cause: err}
				}
			}
			if ctxErr != nil {
				return errors.Join(lastErr, ctxErr)
			}
		}
		if err := retryWait(ctx, retryInterval); err != nil {
			if managedTunnel && errors.Is(lastErr, errDiagnosticDNSPublicationNotReady) {
				return redactedDiagnosticHealthError{
					classification: errDiagnosticDNSResolverUnavailable,
					cause:          errors.Join(lastErr, err),
				}
			}
			return errors.Join(lastErr, err)
		}
	}
}

func classifyRunHealthResponse(resp *http.Response, runID string, managedTunnel bool) (bool, error) {
	body, bodyErr := readAndCloseDiagnosticHealthResponse(resp.Body)
	if resp.StatusCode == http.StatusOK {
		var health struct {
			Schema string `json:"schema"`
			RunID  string `json:"run_id"`
		}
		decodeErr := json.Unmarshal(body, &health)
		if bodyErr != nil || decodeErr != nil || health.Schema != SchemaV1 || health.RunID != runID {
			return false, classifiedDiagnosticHealthError(managedTunnel, errDiagnosticHealthEndpointRejected, bodyErr, decodeErr)
		}
		return false, nil
	}

	if retryableHealthStatus(resp.StatusCode) {
		classification := fmt.Errorf("diagnostic health endpoint returned transient status %d", resp.StatusCode)
		return true, classifiedDiagnosticHealthError(managedTunnel, classification, bodyErr)
	}
	classification := fmt.Errorf("run-correlated diagnostic health endpoint returned status %d", resp.StatusCode)
	return false, classifiedDiagnosticHealthError(managedTunnel, classification, bodyErr)
}

func readAndCloseDiagnosticHealthResponse(body io.ReadCloser) ([]byte, error) {
	if body == nil {
		return nil, errors.New("diagnostic health response body is unavailable")
	}
	data, readErr := io.ReadAll(io.LimitReader(body, maxDiagnosticHealthResponseBytes+1))
	closeErr := body.Close()
	var sizeErr error
	if len(data) > maxDiagnosticHealthResponseBytes {
		sizeErr = errDiagnosticHealthResponseTooLarge
	}
	return data, errors.Join(readErr, sizeErr, closeErr)
}

func classifiedDiagnosticHealthError(managedTunnel bool, classification error, causes ...error) error {
	cause := errors.Join(causes...)
	if cause == nil {
		return classification
	}
	if managedTunnel {
		return redactedDiagnosticHealthError{classification: classification, cause: cause}
	}
	return errors.Join(classification, cause)
}

func diagnosticHealthResponseCause(managedTunnel bool, cause error) error {
	if cause == nil {
		return nil
	}
	return classifiedDiagnosticHealthError(managedTunnel, errDiagnosticHealthEndpointRejected, cause)
}

func waitForDiagnosticHealthRetry(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
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
	managedTunnel bool,
	launch func(context.Context, string, string, bool) error,
) (Observation, error) {
	launchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	launchResult := make(chan error, 1)
	go func() { launchResult <- launch(launchCtx, image, target, managedTunnel) }()
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
	return waitForLaunchCleanupWithin(kind, cancel, launchResult, launchCleanupWaitTimeout)
}

func waitForLaunchCleanupWithin(kind string, cancel context.CancelFunc, launchResult <-chan error, timeout time.Duration) error {
	cancel()
	if timeout <= 0 {
		return fmt.Errorf("%s browser cleanup timeout must be positive", kind)
	}
	select {
	case err := <-launchResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("%s browser cleanup: %w", kind, err)
		}
		return nil
	case <-time.After(timeout):
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
	return defaultDependencies(stderr, newDiagnosticHealthClient)
}

type diagnosticHealthClientFactory func(string, diagnosticHealthDialer) *http.Client

func defaultDependencies(stderr io.Writer, newHealthClient diagnosticHealthClientFactory) Dependencies {
	client := &http.Client{Timeout: 45 * time.Second}
	return Dependencies{
		Tunnel:            &pinnedCloudflaredTunnel{client: client, stderr: stderr},
		HTTPClient:        client,
		TunnelHTTPClient:  newHealthClient(diagnosticDNSResolverAddress, nil),
		LaunchDirect:      launchDirectChrome,
		LaunchAttached:    launchAttachedProvider,
		ValidateLifecycle: validateCandidateLifecycle,
		InspectVersions:   inspectCandidateVersions,
	}
}

type diagnosticHealthDialer func(context.Context, string, string) (net.Conn, error)
type diagnosticIPv4Lookup func(context.Context, string) ([]netip.Addr, error)

func newDiagnosticHealthClient(resolverAddress string, dial diagnosticHealthDialer) *http.Client {
	lookup := newDiagnosticIPv4Lookup(resolverAddress)
	if dial == nil {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		dial = dialer.DialContext
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _ string, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			selected, err := resolvePublicDiagnosticIPv4(ctx, host, lookup)
			if err != nil {
				return nil, err
			}
			return dial(ctx, "tcp4", net.JoinHostPort(selected.String(), port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newDiagnosticIPv4Lookup(resolverAddress string) diagnosticIPv4Lookup {
	if strings.TrimSpace(resolverAddress) == "" {
		resolverAddress = diagnosticDNSResolverAddress
	}
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		return lookupDiagnosticIPv4(ctx, resolverAddress, host)
	}
}

func lookupDiagnosticIPv4(ctx context.Context, resolverAddress, host string) ([]netip.Addr, error) {
	queryCtx, cancel := context.WithTimeout(ctx, diagnosticDNSQueryTimeout)
	defer cancel()

	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return nil, fmt.Errorf("build diagnostic DNS name: %w", err)
	}
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, fmt.Errorf("create diagnostic DNS request ID: %w", err)
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	question := dnsmessage.Question{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}
	query, err := (&dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{question},
	}).Pack()
	if err != nil {
		return nil, fmt.Errorf("pack diagnostic DNS query: %w", err)
	}

	response, err := exchangeDiagnosticDNS(queryCtx, "udp", resolverAddress, query)
	if err != nil {
		return nil, diagnosticDNSExchangeError(host, err)
	}
	header, err := parseDiagnosticDNSHeader(response, id)
	if err != nil {
		return nil, diagnosticDNSProtocolError(host, err)
	}
	if header.Truncated {
		response, err = exchangeDiagnosticDNS(queryCtx, "tcp", resolverAddress, query)
		if err != nil {
			return nil, diagnosticDNSExchangeError(host, err)
		}
		header, err = parseDiagnosticDNSHeader(response, id)
		if err != nil {
			return nil, diagnosticDNSProtocolError(host, err)
		}
		if header.Truncated {
			return nil, diagnosticDNSProtocolError(host, errors.New("diagnostic DNS TCP response is truncated"))
		}
	}
	if err := validateDiagnosticDNSWireLength(response); err != nil {
		return nil, diagnosticDNSProtocolError(host, err)
	}
	var message dnsmessage.Message
	if err := message.Unpack(response); err != nil {
		return nil, diagnosticDNSProtocolError(host, fmt.Errorf("unpack diagnostic DNS response: %w", err))
	}
	if len(message.Questions) != 1 || message.Questions[0].Type != question.Type || message.Questions[0].Class != question.Class ||
		!strings.EqualFold(message.Questions[0].Name.String(), question.Name.String()) {
		return nil, diagnosticDNSProtocolError(host, errors.New("diagnostic DNS response question does not match request"))
	}
	if message.RCode != dnsmessage.RCodeSuccess {
		return nil, diagnosticDNSRCodeError(host, message.RCode)
	}

	addresses, err := diagnosticDNSIPv4Answers(message.Answers, question.Name)
	if err != nil {
		return nil, diagnosticDNSProtocolError(host, err)
	}
	if len(addresses) == 0 {
		return nil, &net.DNSError{Err: "IPv4 address is not published", Name: host, IsNotFound: true}
	}
	return addresses, nil
}

func validateDiagnosticDNSWireLength(message []byte) error {
	const headerBytes = 12
	if len(message) < headerBytes {
		return errors.New("diagnostic DNS response is shorter than its header")
	}
	offset := headerBytes
	questions := int(binary.BigEndian.Uint16(message[4:6]))
	for range questions {
		var err error
		offset, err = skipDiagnosticDNSWireName(message, offset)
		if err != nil {
			return err
		}
		if len(message)-offset < 4 {
			return errors.New("diagnostic DNS question is truncated")
		}
		offset += 4
	}
	resources := int(binary.BigEndian.Uint16(message[6:8])) +
		int(binary.BigEndian.Uint16(message[8:10])) +
		int(binary.BigEndian.Uint16(message[10:12]))
	for range resources {
		var err error
		offset, err = skipDiagnosticDNSWireName(message, offset)
		if err != nil {
			return err
		}
		if len(message)-offset < 10 {
			return errors.New("diagnostic DNS resource header is truncated")
		}
		resourceType := dnsmessage.Type(binary.BigEndian.Uint16(message[offset : offset+2]))
		dataLength := int(binary.BigEndian.Uint16(message[offset+8 : offset+10]))
		offset += 10
		if len(message)-offset < dataLength {
			return errors.New("diagnostic DNS resource data is truncated")
		}
		dataEnd := offset + dataLength
		switch resourceType {
		case dnsmessage.TypeA:
			if dataLength != net.IPv4len {
				return fmt.Errorf("diagnostic DNS A resource data length is %d, want %d", dataLength, net.IPv4len)
			}
		case dnsmessage.TypeCNAME:
			nameEnd, err := skipDiagnosticDNSWireName(message, offset)
			if err != nil {
				return err
			}
			if nameEnd != dataEnd {
				return errors.New("diagnostic DNS CNAME resource contains trailing data")
			}
		}
		offset += dataLength
	}
	if offset != len(message) {
		return fmt.Errorf("diagnostic DNS response contains %d trailing bytes", len(message)-offset)
	}
	return nil
}

func skipDiagnosticDNSWireName(message []byte, offset int) (int, error) {
	for {
		if offset >= len(message) {
			return 0, errors.New("diagnostic DNS name is truncated")
		}
		length := int(message[offset])
		switch length & 0xc0 {
		case 0xc0:
			if len(message)-offset < 2 {
				return 0, errors.New("diagnostic DNS name pointer is truncated")
			}
			return offset + 2, nil
		case 0:
			if length == 0 {
				return offset + 1, nil
			}
			if len(message)-offset-1 < length {
				return 0, errors.New("diagnostic DNS label is truncated")
			}
			offset += 1 + length
		default:
			return 0, errors.New("diagnostic DNS name uses a reserved label encoding")
		}
	}
}

func diagnosticDNSIPv4Answers(answers []dnsmessage.Resource, queryName dnsmessage.Name) ([]netip.Addr, error) {
	aRecords := make(map[string][]netip.Addr)
	cnames := make(map[string]string)
	for _, answer := range answers {
		if answer.Header.Class != dnsmessage.ClassINET {
			continue
		}
		owner := strings.ToLower(answer.Header.Name.String())
		switch resource := answer.Body.(type) {
		case *dnsmessage.AResource:
			address := netip.AddrFrom4(resource.A)
			if !slices.Contains(aRecords[owner], address) {
				aRecords[owner] = append(aRecords[owner], address)
			}
		case *dnsmessage.CNAMEResource:
			target := strings.ToLower(resource.CNAME.String())
			if existing, ok := cnames[owner]; ok && existing != target {
				return nil, errors.New("diagnostic DNS response contains conflicting CNAME answers")
			}
			cnames[owner] = target
		}
	}

	current := strings.ToLower(queryName.String())
	reachable := make(map[string]struct{})
	var selected []netip.Addr
	for hops := 0; ; hops++ {
		if _, seen := reachable[current]; seen {
			return nil, errors.New("diagnostic DNS response contains a CNAME loop")
		}
		reachable[current] = struct{}{}
		addresses := aRecords[current]
		target, hasCNAME := cnames[current]
		if len(addresses) > 0 {
			if hasCNAME {
				return nil, errors.New("diagnostic DNS response owner has both A and CNAME answers")
			}
			selected = addresses
			break
		}
		if !hasCNAME {
			break
		}
		if hops == maxDiagnosticDNSCNAMEHops {
			return nil, errors.New("diagnostic DNS response exceeds the CNAME hop limit")
		}
		current = target
	}
	for owner := range aRecords {
		if _, ok := reachable[owner]; !ok {
			return nil, errors.New("diagnostic DNS response contains an unrelated A answer")
		}
	}
	for owner := range cnames {
		if _, ok := reachable[owner]; !ok {
			return nil, errors.New("diagnostic DNS response contains an unrelated CNAME answer")
		}
	}
	return selected, nil
}

func exchangeDiagnosticDNS(ctx context.Context, network, resolverAddress string, query []byte) ([]byte, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, network, resolverAddress)
	if err != nil {
		return nil, err
	}
	defer func() { _ = connection.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = connection.SetDeadline(time.Now())
	})
	defer stopCancellation()

	if network == "udp" {
		count, err := connection.Write(query)
		if err != nil {
			return nil, err
		}
		if count != len(query) {
			return nil, io.ErrShortWrite
		}
		response := make([]byte, maxDiagnosticDNSResponseBytes)
		count, err = connection.Read(response)
		if err != nil {
			return nil, err
		}
		return response[:count], nil
	}
	if network != "tcp" {
		return nil, fmt.Errorf("unsupported diagnostic DNS network %q", network)
	}
	if len(query) > maxDiagnosticDNSResponseBytes {
		return nil, errors.New("diagnostic DNS query exceeds TCP framing limit")
	}
	frame := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(query)))
	copy(frame[2:], query)
	if _, err := io.Copy(connection, bytes.NewReader(frame)); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(connection, size[:]); err != nil {
		return nil, err
	}
	response := make([]byte, int(binary.BigEndian.Uint16(size[:])))
	if len(response) == 0 {
		return nil, errors.New("diagnostic DNS TCP response is empty")
	}
	if _, err := io.ReadFull(connection, response); err != nil {
		return nil, err
	}
	return response, nil
}

func parseDiagnosticDNSHeader(response []byte, id uint16) (dnsmessage.Header, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(response)
	if err != nil {
		return dnsmessage.Header{}, fmt.Errorf("parse diagnostic DNS response header: %w", err)
	}
	if !header.Response || header.ID != id || header.OpCode != 0 {
		return dnsmessage.Header{}, errors.New("diagnostic DNS response header does not match request")
	}
	return header, nil
}

func diagnosticDNSExchangeError(host string, cause error) error {
	if cause == nil {
		return nil
	}
	var networkErr net.Error
	timedOut := errors.Is(cause, context.DeadlineExceeded) || (errors.As(cause, &networkErr) && networkErr.Timeout())
	dnsErr := &net.DNSError{
		Err:         "diagnostic DNS exchange failed",
		Name:        host,
		IsTimeout:   timedOut,
		IsTemporary: true,
	}
	return errors.Join(dnsErr, cause)
}

func diagnosticDNSProtocolError(host string, cause error) error {
	if cause == nil {
		return nil
	}
	return errors.Join(&net.DNSError{
		Err:         "invalid diagnostic DNS response",
		Name:        host,
		IsTemporary: true,
	}, cause)
}

func diagnosticDNSRCodeError(host string, rcode dnsmessage.RCode) error {
	return &net.DNSError{
		Err:         "diagnostic DNS response code " + rcode.String(),
		Name:        host,
		IsNotFound:  rcode == dnsmessage.RCodeNameError,
		IsTemporary: rcode == dnsmessage.RCodeServerFailure || rcode == dnsmessage.RCodeRefused,
	}
}

func resolvePublicDiagnosticIPv4(ctx context.Context, host string, lookup diagnosticIPv4Lookup) (netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return netip.Addr{}, err
	}
	if literal, err := netip.ParseAddr(host); err == nil {
		literal = literal.Unmap()
		if !IsPublicDiagnosticIPv4(literal) {
			return netip.Addr{}, errDiagnosticNonPublicIPv4
		}
		return literal, nil
	}
	addresses, err := lookup(ctx, host)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return netip.Addr{}, errors.Join(err, ctxErr)
	}
	if err != nil {
		return netip.Addr{}, err
	}
	if len(addresses) == 0 {
		return netip.Addr{}, &net.DNSError{Err: "IPv4 address is not published", Name: host, IsNotFound: true}
	}
	var selected netip.Addr
	for _, address := range addresses {
		address = address.Unmap()
		if !IsPublicDiagnosticIPv4(address) {
			return netip.Addr{}, errDiagnosticNonPublicIPv4
		}
		if !selected.IsValid() {
			selected = address
		}
	}
	return selected, nil
}

// IsPublicDiagnosticIPv4 reports whether address is a routable IPv4 diagnostic target.
func IsPublicDiagnosticIPv4(address netip.Addr) bool {
	address = address.Unmap()
	if !address.Is4() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
	} {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func resolveManagedDirectChromeResolverRule(
	ctx context.Context,
	target string,
	lookup diagnosticIPv4Lookup,
	retryWindow, retryInterval time.Duration,
) (string, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("managed direct Chrome target is invalid")
	}
	if lookup == nil {
		return "", errors.New("managed direct Chrome DNS resolver is unavailable")
	}
	if retryWindow <= 0 || retryInterval <= 0 {
		return "", errors.New("managed direct Chrome DNS retry bounds must be positive")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	retryCtx, cancel := context.WithTimeout(ctx, retryWindow)
	defer cancel()
	var lastErr error
	for {
		selected, lookupErr := resolvePublicDiagnosticIPv4(retryCtx, host, lookup)
		if ctxErr := retryCtx.Err(); ctxErr != nil {
			return "", errors.Join(lookupErr, ctxErr)
		}
		if lookupErr == nil {
			return "MAP " + host + " " + selected.String(), nil
		}
		lastErr = lookupErr
		if errors.Is(lookupErr, errDiagnosticNonPublicIPv4) || !retryableDiagnosticIPv4LookupError(lookupErr) {
			return "", lookupErr
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return "", errors.Join(lastErr, retryCtx.Err())
		case <-timer.C:
		}
	}
}

func retryableDiagnosticIPv4LookupError(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.IsTimeout || dnsErr.IsTemporary)
}

const headedContainerScript = `set -eu
xvfb_pid=
child_pid=
child_process_state() {
  pid=$1
  retries=3
  while [ "$retries" -gt 0 ]; do
    stat=$(cat "/proc/$pid/stat" 2>/dev/null) || return 1
    case "$stat" in
      *") "*)
        rest=${stat##*) }
        set -- $rest
        if [ "$#" -ge 1 ]; then
          state=$1
          case "$state" in
            [A-Za-z]) printf '%s\n' "$state"; return 0 ;;
          esac
        fi
        ;;
    esac
    retries=$((retries - 1))
  done
  return 1
}
cleanup() {
  if [ -n "$child_pid" ]; then
    kill -TERM "$child_pid" 2>/dev/null || true
    timeout=${PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT:-10}
    attempts=$((timeout * 20))
    while [ "$attempts" -gt 0 ]; do
      state=$(child_process_state "$child_pid") || state=
      if [ "$state" = Z ] || { [ -z "$state" ] && ! kill -0 "$child_pid" 2>/dev/null; }; then
        break
      fi
      attempts=$((attempts - 1))
      sleep 0.05
    done
    state=$(child_process_state "$child_pid") || state=
    if [ "$state" != Z ] && kill -0 "$child_pid" 2>/dev/null; then
      kill -KILL "$child_pid" 2>/dev/null || true
    fi
    wait "$child_pid" 2>/dev/null || true
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

func directChromeContainerArgs(image, target, hostProfile string, managedTunnel bool, resolverRule string) []string {
	name := "product-capture-direct-" + mustRandomSuffix()
	args := []string{
		"run", "--rm", "--platform", "linux/amd64", "--name", name,
	}
	args = append(args, quickTunnelDNSArgs(target, managedTunnel)...)
	args = append(args,
		"-e", "PRODUCT_CAPTURE_XVFB_READY_TIMEOUT=10",
		"-e", "PRODUCT_CAPTURE_CHILD_STOP_TIMEOUT=10",
		"-v", hostProfile+":/tmp/conformance-profile",
		"--entrypoint", "/bin/sh", image,
		"-c", headedContainerScript, "--", "google-chrome",
		"--user-data-dir=/tmp/conformance-profile",
		"--window-size=1920,1080",
		"--no-first-run", "--no-default-browser-check",
		"--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage",
	)
	if resolverRule != "" {
		args = append(args, "--host-resolver-rules="+resolverRule)
	}
	return append(args, target)
}

func attachedProviderContainerArgs(image, target string, managedTunnel bool) []string {
	parsed, _ := url.Parse(target)
	origin := parsed.Scheme + "://" + parsed.Host
	args := []string{
		"run", "--rm", "--platform", "linux/amd64", "--name", "product-capture-attached-" + mustRandomSuffix(),
	}
	dnsArgs := quickTunnelDNSArgs(target, managedTunnel)
	args = append(args, dnsArgs...)
	args = append(args,
		"-e", "PRODUCT_CAPTURE_BROWSER_HEADLESS=false",
		"-e", "PRODUCT_CAPTURE_BROWSER_DIAGNOSTIC_ALLOWED_ORIGINS="+origin,
		"--entrypoint", "/usr/local/bin/product-capture-provider", image,
		"--browser-diagnostic-url", target,
	)
	if len(dnsArgs) > 0 {
		args = append(args, "--browser-diagnostic-require-ipv4")
	}
	return args
}

func quickTunnelDNSArgs(target string, managedTunnel bool) []string {
	if !managedTunnel {
		return nil
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "trycloudflare.com" && !strings.HasSuffix(host, ".trycloudflare.com") {
		return nil
	}
	return []string{"--dns", diagnosticDNSResolverIP}
}

func launchDirectChrome(ctx context.Context, image, target string, managedTunnel bool) error {
	resolverRule := ""
	if len(quickTunnelDNSArgs(target, managedTunnel)) > 0 {
		var err error
		resolverRule, err = resolveManagedDirectChromeResolverRule(
			ctx,
			target,
			newDiagnosticIPv4Lookup(diagnosticDNSResolverAddress),
			managedDirectDNSRetryWindow,
			managedDirectDNSRetryInterval,
		)
		if err != nil {
			return fmt.Errorf("resolve managed direct Chrome target: %w", err)
		}
	}
	profile, err := os.MkdirTemp("", "product-capture-conformance-profile-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	if err := os.Chmod(profile, 0o777); err != nil {
		return err
	}
	args := directChromeContainerArgs(image, target, profile, managedTunnel, resolverRule)
	name := containerName(args)
	runErr := runManagedContainer(ctx, name, args, nil)
	return errors.Join(runErr, cleanupEphemeralProfile(profile))
}

func launchAttachedProvider(ctx context.Context, image, target string, managedTunnel bool) error {
	args := attachedProviderContainerArgs(image, target, managedTunnel)
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
	case waitErr := <-wait:
		var runErr error
		if waitErr != nil {
			runErr = fmt.Errorf("candidate container %s: %w: %s", name, waitErr, output.String())
		}
		runErr = errors.Join(runErr, ctx.Err())
		return errors.Join(runErr, cleanupLifecycleContainer(name, wait, true, "stop"))
	case <-ctx.Done():
		return errors.Join(ctx.Err(), cleanupLifecycleContainer(name, wait, false, "stop"))
	}
}

type boundedWriter struct {
	buffer bytes.Buffer
	limit  int
	over   bool
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	original := len(data)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(data[:min(remaining, len(data))])
	}
	if original > remaining {
		w.over = true
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
	ctx, cancel := context.WithTimeout(context.Background(), candidateInspectTimeout)
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

func assertContainerGoneWith(docker func(context.Context, ...string) error, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), candidateInspectTimeout)
	defer cancel()
	err := docker(ctx, "container", "inspect", name)
	if err == nil {
		return fmt.Errorf("container %s remains after cleanup", name)
	}
	if ignoreMissingContainer(err) != nil {
		return fmt.Errorf("inspect container cleanup %s: %w", name, err)
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

func validateCandidateLifecycle(ctx context.Context, image, origin string, managedTunnel bool) error {
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
		if err := runLifecycleScenario(ctx, image, scenario.target, scenario.delay, scenario.signal, managedTunnel); err != nil {
			return fmt.Errorf("%s lifecycle: %w", scenario.name, err)
		}
	}
	return nil
}

func runLifecycleScenario(ctx context.Context, image, target string, delay time.Duration, signal string, managedTunnel bool) error {
	args := attachedProviderContainerArgs(image, target, managedTunnel)
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
		return errors.Join(err, cleanupLifecycleContainer(name, wait, false, "stop"))
	}
	timer := time.NewTimer(delay)
	var triggerErr error
	select {
	case <-ctx.Done():
		timer.Stop()
		triggerErr = ctx.Err()
	case <-timer.C:
	case waitErr := <-wait:
		earlyExitErr := fmt.Errorf("container exited before %s termination: %s", signal, output.String())
		if waitErr != nil {
			earlyExitErr = fmt.Errorf("container exited before %s termination: %w: %s", signal, waitErr, output.String())
		}
		return errors.Join(earlyExitErr, cleanupLifecycleContainer(name, wait, true, "stop"))
	}
	cleanupSignal := signal
	if triggerErr != nil {
		cleanupSignal = "stop"
	}
	return errors.Join(triggerErr, cleanupLifecycleContainer(name, wait, false, cleanupSignal))
}

func cleanupLifecycleContainer(name string, wait <-chan error, processReaped bool, signal string) error {
	commandCtx, cancel := context.WithTimeout(context.Background(), candidateStopCommandTimeout)
	defer cancel()
	var terminateErr error
	if signal == "stop" {
		terminateErr = ignoreMissingContainer(dockerCommand(commandCtx, "stop", "--time", fmt.Sprintf("%d", candidateStopSeconds), name))
	} else {
		terminateErr = ignoreMissingContainer(dockerCommand(commandCtx, "kill", "--signal", signal, name))
	}
	var reapErr error
	if !processReaped {
		reapErr = forceContainerAndWait(wait, candidateReapGrace, func() error {
			forceCtx, forceCancel := context.WithTimeout(context.Background(), candidateForceRemoveTimeout)
			defer forceCancel()
			return ignoreMissingContainer(dockerCommand(forceCtx, "rm", "-f", name))
		})
	}
	removeCtx, removeCancel := context.WithTimeout(context.Background(), candidateFinalRemoveTimeout)
	defer removeCancel()
	removeErr := ignoreMissingContainer(dockerCommand(removeCtx, "rm", "-f", name))
	return errors.Join(terminateErr, reapErr, removeErr, assertContainerGone(name))
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
	if len(args) > 0 && args[0] == "run" {
		name := "product-capture-version-" + mustRandomSuffix()
		namedArgs := make([]string, 0, len(args)+2)
		namedArgs = append(namedArgs, "run", "--name", name)
		namedArgs = append(namedArgs, args[1:]...)
		return managedDockerRunOutput(ctx, name, namedArgs)
	}
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

func managedDockerRunOutput(ctx context.Context, name string, args []string) (string, error) {
	cmd := exec.Command("docker", args...)
	var output boundedWriter
	output.limit = 4096
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start Docker version probe %s: %w", name, err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var waitErr error
	processReaped := false
	select {
	case waitErr = <-wait:
		processReaped = true
	case <-ctx.Done():
		waitErr = ctx.Err()
	}
	cleanupErr := cleanupLifecycleContainer(name, wait, processReaped, "stop")
	if waitErr != nil {
		return "", errors.Join(fmt.Errorf("docker version probe %s: %w: %s", name, waitErr, output.String()), cleanupErr)
	}
	if cleanupErr != nil {
		return "", cleanupErr
	}
	value := output.String()
	if value == "" || output.over {
		return "", fmt.Errorf("docker version probe %s returned invalid bounded output", name)
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
	killProcess   func(*os.Process) error
	reapTimeout   time.Duration
	mu            sync.Mutex
	cmd           *exec.Cmd
	done          chan struct{}
	waitErr       error
	containerName string
	tempDir       string
}

var quickTunnelOrigin = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

const tunnelRetainedLogLimit = 128 << 10

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

func scanTunnelOutput(reader io.Reader, stderr io.Writer, origins chan<- string) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	retainedBytes := 0
	for scanner.Scan() {
		line := scanner.Text()
		if stderr != nil && retainedBytes < tunnelRetainedLogLimit {
			redacted := redactTunnelLog(line)
			lineBytes := len(redacted) + 1
			if retainedBytes+lineBytes <= tunnelRetainedLogLimit {
				_, _ = fmt.Fprintln(stderr, redacted)
				retainedBytes += lineBytes
			}
		}
		if origin := parseQuickTunnelOrigin(line); origin != "" {
			select {
			case origins <- origin:
			default:
			}
		}
	}
	return scanner.Err()
}

func (t *pinnedCloudflaredTunnel) Start(ctx context.Context, localURL string) (origin string, startErr error) {
	tempDir, err := os.MkdirTemp("", "product-capture-cloudflared-*")
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	t.tempDir = tempDir
	t.mu.Unlock()
	defer func() {
		if startErr == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		startErr = errors.Join(startErr, t.Stop(cleanupCtx))
	}()
	path := filepath.Join(tempDir, "cloudflared-linux-amd64")
	if err := t.download(ctx, path); err != nil {
		return "", err
	}
	versionOutput, err := t.cloudflaredVersion(ctx, path)
	if err != nil {
		return "", err
	}
	if err := VerifyCloudflaredArtifact(path, CloudflaredSHA256, versionOutput); err != nil {
		return "", err
	}
	cmd, containerName := cloudflaredCommand(path, localURL)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", errors.Join(err, stdout.Close())
	}
	if err := cmd.Start(); err != nil {
		return "", errors.Join(err, stdout.Close(), stderr.Close())
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
	scanErrors := make(chan error, 2)
	scan := func(reader io.Reader) {
		if err := scanTunnelOutput(reader, t.stderr, origins); err != nil {
			scanErrors <- err
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
		return "", errors.Join(errTunnelExitedBeforeOrigin, err)
	case <-timer.C:
		return "", errTunnelActivationTimeout
	case <-ctx.Done():
		return "", ctx.Err()
	case scanErr := <-scanErrors:
		return "", fmt.Errorf("scan cloudflared output: %w", scanErr)
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
		var removeErr, absenceErr error
		if name != "" {
			forceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			removeErr = ignoreMissingContainer(docker(forceCtx, "rm", "-f", name))
			cancel()
			absenceErr = assertContainerGoneWith(docker, name)
		}
		if tempDir != "" {
			removeErr = errors.Join(removeErr, os.RemoveAll(tempDir))
		}
		return errors.Join(removeErr, absenceErr)
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
		killErr := t.forceKill(cmd.Process)
		stopErr = errors.Join(stopErr, forceErr, killErr, ctx.Err())
	case <-timer.C:
		var forceErr error
		if name != "" {
			forceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			forceErr = ignoreMissingContainer(docker(forceCtx, "rm", "-f", name))
			cancel()
		}
		killErr := t.forceKill(cmd.Process)
		if name != "" && forceErr == nil && killErr == nil {
			stopErr = nil
		} else {
			stopErr = errors.Join(stopErr, forceErr, killErr, errors.New("cloudflared did not reap after stop"))
		}
	}
	postKillTimer := time.NewTimer(reapTimeout)
	defer postKillTimer.Stop()
	select {
	case <-done:
	case <-postKillTimer.C:
		stopErr = errors.Join(stopErr, errors.New("cloudflared process did not reap after force removal"))
	}
	var removeErr, absenceErr error
	if name != "" {
		removeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		removeErr = ignoreMissingContainer(docker(removeCtx, "rm", "-f", name))
		cancel()
		absenceErr = assertContainerGoneWith(docker, name)
	}
	return errors.Join(stopErr, removeErr, absenceErr, os.RemoveAll(tempDir))
}

func (t *pinnedCloudflaredTunnel) forceKill(process *os.Process) error {
	if process == nil {
		return nil
	}
	kill := t.killProcess
	if kill == nil {
		kill = func(process *os.Process) error { return process.Kill() }
	}
	err := kill(process)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
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
