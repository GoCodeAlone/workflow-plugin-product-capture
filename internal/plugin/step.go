package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

type connectionConfig struct {
	ServerURL      string `json:"server_url"`
	AuthTokenRef   string `json:"auth_token_ref"`
	RequestTimeout string `json:"request_timeout,omitempty"`
	ConfigDir      string `json:"_config_dir,omitempty"`
}

func (c connectionConfig) validate() error {
	var errs []error
	if c.ServerURL == "" {
		errs = append(errs, errors.New("server_url is required"))
	}
	if c.AuthTokenRef == "" {
		errs = append(errs, errors.New("auth_token_ref is required"))
	} else if !isRef(c.AuthTokenRef) {
		errs = append(errs, errors.New("auth_token_ref must be a secret: or config: ref"))
	}
	if c.RequestTimeout != "" {
		if _, err := time.ParseDuration(c.RequestTimeout); err != nil {
			errs = append(errs, fmt.Errorf("request_timeout must be duration: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (c connectionConfig) client(ctx context.Context, metadata, runtimeConfig map[string]any) (*protocol.Client, error) {
	_ = ctx
	token, err := resolveRuntimeRef(c.AuthTokenRef, metadata, runtimeConfig)
	if err != nil {
		return nil, err
	}
	timeout := 30 * time.Second
	if c.RequestTimeout != "" {
		timeout, err = time.ParseDuration(c.RequestTimeout)
		if err != nil {
			return nil, err
		}
	}
	return protocol.NewClient(protocol.ClientConfig{
		ServerURL: c.ServerURL,
		Token:     token,
		Timeout:   timeout,
	})
}

type taskConfig struct {
	ID             string            `json:"id,omitempty"`
	ProductID      string            `json:"product_id"`
	OrgID          string            `json:"org_id"`
	PoolID         string            `json:"pool_id"`
	PolicyID       string            `json:"policy_id"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Labels         map[string]string `json:"labels,omitempty"`
}

func (c taskConfig) validate() error {
	var errs []error
	if c.ProductID == "" {
		errs = append(errs, errors.New("product_id is required"))
	}
	if c.OrgID == "" {
		errs = append(errs, errors.New("org_id is required"))
	}
	if c.PoolID == "" {
		errs = append(errs, errors.New("pool_id is required"))
	}
	if c.PolicyID == "" {
		errs = append(errs, errors.New("policy_id is required"))
	}
	if c.TimeoutSeconds <= 0 {
		errs = append(errs, errors.New("timeout_seconds must be positive"))
	}
	return errors.Join(errs...)
}

type productCaptureStepConfig struct {
	connectionConfig
	taskConfig
	ProviderID              string   `json:"provider_id,omitempty"`
	ProviderVersion         string   `json:"provider_version,omitempty"`
	ProviderConfigRef       string   `json:"provider_config_ref,omitempty"`
	ProviderConfigDigest    string   `json:"provider_config_digest,omitempty"`
	ProviderImageRef        string   `json:"provider_image_ref,omitempty"`
	ProviderComponentRef    string   `json:"provider_component_ref,omitempty"`
	ProviderComponentDigest string   `json:"provider_component_digest,omitempty"`
	URL                     string   `json:"url,omitempty"`
	URLField                string   `json:"url_field,omitempty"`
	AllowedHosts            []string `json:"allowed_hosts"`
	CaptureMode             string   `json:"capture_mode,omitempty"`
	CaptureTimeoutSeconds   int      `json:"capture_timeout_seconds,omitempty"`
	MaxHTMLBytes            int64    `json:"max_html_bytes,omitempty"`
	MaxImageCount           int      `json:"max_image_count,omitempty"`
	MetadataOnly            bool     `json:"metadata_only,omitempty"`
	PollInterval            string   `json:"poll_interval,omitempty"`
	WaitTimeout             string   `json:"wait_timeout,omitempty"`
	RequireProof            *bool    `json:"require_proof,omitempty"`
}

type productCaptureStep struct {
	name   string
	config productCaptureStepConfig
}

func newProductCaptureStep(name string, raw map[string]any) (*productCaptureStep, error) {
	var cfg productCaptureStepConfig
	if err := decodeStrictMap(raw, &cfg); err != nil {
		return nil, fmt.Errorf("step.product_capture %q: %w", name, err)
	}
	if err := errors.Join(cfg.connectionConfig.validate(), cfg.taskConfig.validate()); err != nil {
		return nil, fmt.Errorf("step.product_capture %q: %w", name, err)
	}
	if cfg.URL == "" && cfg.URLField == "" {
		return nil, fmt.Errorf("step.product_capture %q: url or url_field is required", name)
	}
	if len(cfg.AllowedHosts) == 0 {
		return nil, fmt.Errorf("step.product_capture %q: allowed_hosts is required", name)
	}
	if err := validateProviderRuntimeRef(cfg.ProviderImageRef, cfg.ProviderComponentRef, cfg.ProviderComponentDigest); err != nil {
		return nil, fmt.Errorf("step.product_capture %q: %w", name, err)
	}
	if cfg.PollInterval != "" {
		if d, err := time.ParseDuration(cfg.PollInterval); err != nil {
			return nil, fmt.Errorf("step.product_capture %q: poll_interval must be duration: %w", name, err)
		} else if d <= 0 {
			return nil, fmt.Errorf("step.product_capture %q: poll_interval must be positive", name)
		}
	}
	if cfg.WaitTimeout != "" {
		if d, err := time.ParseDuration(cfg.WaitTimeout); err != nil {
			return nil, fmt.Errorf("step.product_capture %q: wait_timeout must be duration: %w", name, err)
		} else if d <= 0 {
			return nil, fmt.Errorf("step.product_capture %q: wait_timeout must be positive", name)
		}
	}
	return &productCaptureStep{name: name, config: cfg}, nil
}

func (s *productCaptureStep) Execute(ctx context.Context, _ map[string]any, _ map[string]map[string]any, current map[string]any, metadata map[string]any, runtimeConfig map[string]any) (*sdk.StepResult, error) {
	url := s.config.URL
	if url == "" {
		url = stateString(current, s.config.URLField)
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return errorResult("product capture url is required"), nil
	}
	client, err := s.config.connectionConfig.client(ctx, metadata, runtimeConfig)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	input := productCaptureProviderInput{
		URL:            url,
		AllowedHosts:   append([]string(nil), s.config.AllowedHosts...),
		CaptureMode:    s.config.CaptureMode,
		TimeoutSeconds: s.config.CaptureTimeoutSeconds,
		MaxHTMLBytes:   s.config.MaxHTMLBytes,
		MaxImageCount:  s.config.MaxImageCount,
		MetadataOnly:   s.config.MetadataOnly,
	}
	if input.CaptureMode == "" {
		input.CaptureMode = string(protocol.ProductCaptureModeBrowser)
	}
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	workload := protocol.WorkloadSpec{
		Kind: protocol.WorkloadProvider,
		Provider: &protocol.ProviderWorkload{
			ProviderConfig:  s.productCaptureProviderConfig(),
			Operation:       "capture_product",
			ImageRef:        s.config.ProviderImageRef,
			ComponentRef:    s.config.ProviderComponentRef,
			ComponentDigest: s.config.ProviderComponentDigest,
			Input:           inputBytes,
		},
	}
	if err := workload.Validate(); err != nil {
		return errorResult(err.Error()), nil
	}
	task, err := client.SubmitTask(ctx, buildTask(s.config.taskConfig, workload))
	if err != nil {
		return errorResult(err.Error()), nil
	}
	output, err := s.waitForProductCapture(ctx, client, task.ID)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	if output["error"] != nil {
		return &sdk.StepResult{StopPipeline: true, Output: output}, nil
	}
	return &sdk.StepResult{Output: output}, nil
}

type productCaptureProviderInput struct {
	URL            string   `json:"url"`
	AllowedHosts   []string `json:"allowed_hosts"`
	CaptureMode    string   `json:"capture_mode,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MaxHTMLBytes   int64    `json:"max_html_bytes,omitempty"`
	MaxImageCount  int      `json:"max_image_count,omitempty"`
	MetadataOnly   bool     `json:"metadata_only,omitempty"`
}

func (s *productCaptureStep) productCaptureProviderConfig() protocol.ProviderConfig {
	cfg := protocol.ProviderConfig{
		PluginID:     "workflow-plugin-product-capture",
		ProviderID:   defaultString(s.config.ProviderID, "browser"),
		ContractID:   "product-capture.browser.v1",
		Version:      defaultString(s.config.ProviderVersion, "v1.0.0"),
		ConfigRef:    s.config.ProviderConfigRef,
		ConfigDigest: s.config.ProviderConfigDigest,
	}
	if cfg.ConfigRef == "" {
		cfg.ConfigRef = "config://network-products/" + s.config.ProductID + "/browser"
	}
	return cfg
}

func (s *productCaptureStep) waitForProductCapture(ctx context.Context, client *protocol.Client, taskID string) (map[string]any, error) {
	pollInterval := durationOrDefault(s.config.PollInterval, time.Second)
	timeout := durationOrDefault(s.config.WaitTimeout, 5*time.Minute)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		task, found, stalls, err := client.TaskSnapshot(waitCtx, taskID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("task %q not found", taskID)
		}
		actionableStalls := actionableStalls(stalls, s.requireProof())
		if task.Status == protocol.TaskFailed || task.Status == protocol.TaskStalled || len(actionableStalls) > 0 {
			output := taskOutput(task)
			if len(actionableStalls) > 0 {
				addStallOutput(output, actionableStalls[0])
			}
			output["error"] = taskWaitError(task, actionableStalls)
			return output, nil
		}
		if isTerminalTaskStatus(task.Status) {
			proof, hasProof, err := client.FindProof(waitCtx, task.ID)
			if err != nil {
				return nil, err
			}
			output := taskOutput(task)
			if hasProof {
				addProofOutput(output, proof)
			}
			if hasProof && proof.Verifier.Status != protocol.VerificationAccepted {
				output["error"] = fmt.Sprintf("task %q proof %q is %s", task.ID, proof.ID, proof.Verifier.Status)
				return output, nil
			}
			if hasProof || !s.requireProof() {
				return output, nil
			}
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("timed out waiting for task %q", taskID)
		case <-timer.C:
		}
	}
}

func (s *productCaptureStep) requireProof() bool {
	return s.config.RequireProof == nil || *s.config.RequireProof
}

func validateProviderRuntimeRef(imageRef, componentRef, componentDigest string) error {
	hasImage := strings.TrimSpace(imageRef) != ""
	hasComponent := strings.TrimSpace(componentRef) != "" || strings.TrimSpace(componentDigest) != ""
	switch {
	case hasImage && hasComponent:
		return errors.New("provider_image_ref and provider_component_ref/provider_component_digest are mutually exclusive")
	case hasImage:
		if err := validateProviderImageRef(imageRef); err != nil {
			return fmt.Errorf("provider_image_ref: %w", err)
		}
	case hasComponent:
		if err := validateProviderComponentRef(componentRef, componentDigest); err != nil {
			return err
		}
	default:
		return errors.New("provider_image_ref or provider_component_ref/provider_component_digest is required")
	}
	return nil
}

func validateProviderImageRef(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("is required")
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\t\r\n \x00") {
		return errors.New("must not contain whitespace or NUL")
	}
	_, digest, ok := strings.Cut(value, "@")
	if !ok || !validSHA256Digest(digest) {
		return errors.New("must be digest-pinned with @sha256:<64 hex>")
	}
	return nil
}

func validateProviderComponentRef(componentRef, componentDigest string) error {
	if strings.TrimSpace(componentRef) == "" {
		return errors.New("provider_component_ref is required when provider_component_digest is set")
	}
	if strings.TrimSpace(componentDigest) == "" {
		return errors.New("provider_component_digest is required when provider_component_ref is set")
	}
	if strings.TrimSpace(componentRef) != componentRef || strings.ContainsAny(componentRef, "\t\r\n \x00") {
		return errors.New("provider_component_ref must not contain whitespace or NUL")
	}
	if strings.TrimSpace(componentDigest) != componentDigest || strings.ContainsAny(componentDigest, "\t\r\n \x00") {
		return errors.New("provider_component_digest must not contain whitespace or NUL")
	}
	const prefix = "provider://workflow-plugin-product-capture/"
	if !strings.HasPrefix(componentRef, prefix) || strings.TrimPrefix(componentRef, prefix) == "" {
		return errors.New("provider_component_ref must target workflow-plugin-product-capture")
	}
	if !validSHA256Digest(componentDigest) {
		return errors.New("provider_component_digest must be sha256:<64 hex>")
	}
	return nil
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func taskOutput(task protocol.Task) map[string]any {
	return map[string]any{
		"task_id": task.ID,
		"org_id":  task.OrgID,
		"pool_id": task.PoolID,
		"status":  string(task.Status),
	}
}

func addProofOutput(output map[string]any, proof protocol.ProofReceipt) {
	output["proof_id"] = proof.ID
	output["proof_status"] = string(proof.Verifier.Status)
	output["proof_provider"] = proof.Verifier.Provider
	output["worker_id"] = proof.WorkerID
	output["artifact_hash"] = proof.ArtifactHash
	if len(proof.ResultPreview) > 0 {
		output["result_preview"] = proof.ResultPreview
		for key, value := range proof.ResultPreview {
			if _, exists := output[key]; !exists && flattenedPreviewField(key) {
				output[key] = value
			}
		}
	}
}

func flattenedPreviewField(key string) bool {
	switch key {
	case "affiliate_url",
		"asin",
		"availability",
		"canonical_url",
		"captured_at",
		"confidence",
		"currency",
		"description",
		"external_id",
		"image_url",
		"images",
		"marketplace",
		"merchant",
		"price",
		"prime_eligible",
		"product_url",
		"provider",
		"provider_version",
		"rating",
		"requested_url",
		"requires_user_confirmation",
		"review_count",
		"seller",
		"shipping_summary",
		"ships_from",
		"source",
		"title",
		"url",
		"variant",
		"variant_dimensions",
		"variant_key":
		return true
	default:
		return false
	}
}

func addStallOutput(output map[string]any, stall protocol.TaskStall) {
	output["stall_reason"] = stall.Reason
	if stall.LeaseID != "" {
		output["lease_id"] = stall.LeaseID
	}
	if stall.AgentID != "" {
		output["agent_id"] = stall.AgentID
	}
	if stall.AgeMS != 0 {
		output["stall_age_ms"] = stall.AgeMS
	}
}

func taskWaitError(task protocol.Task, stalls []protocol.TaskStall) string {
	if len(stalls) > 0 {
		return fmt.Sprintf("task %q stalled: %s", task.ID, stalls[0].Reason)
	}
	return fmt.Sprintf("task %q %s", task.ID, task.Status)
}

func actionableStalls(stalls []protocol.TaskStall, requireProof bool) []protocol.TaskStall {
	if requireProof {
		return stalls
	}
	actionable := make([]protocol.TaskStall, 0, len(stalls))
	for _, stall := range stalls {
		if stall.Reason == "proof_missing" {
			continue
		}
		actionable = append(actionable, stall)
	}
	return actionable
}

func isTerminalTaskStatus(status protocol.TaskStatus) bool {
	switch status {
	case protocol.TaskSucceeded, protocol.TaskFailed, protocol.TaskStalled:
		return true
	default:
		return false
	}
}

func durationOrDefault(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func errorResult(msg string) *sdk.StepResult {
	return &sdk.StepResult{
		StopPipeline: true,
		Output: map[string]any{
			"error": msg,
		},
	}
}

func stateString(current map[string]any, field string) string {
	if current == nil || field == "" {
		return ""
	}
	value, ok := current[field]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}
