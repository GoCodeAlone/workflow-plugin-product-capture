package stagingproof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/snapshot"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	defaultPollInterval                          = 30 * time.Second
	defaultCapacityTimeout                       = 30 * time.Minute
	defaultResultTimeout                         = 30 * time.Minute
	defaultArtifactTimeout                       = 5 * time.Minute
	defaultTaskTimeoutSeconds                    = 300
	maxTaskTimeoutSeconds                        = 300
	maxSummaryTitleBytes                         = 512
	maxSummaryImageURLBytes                      = 2048
	maxSummaryPriceBytes                         = 128
	maxSummaryCurrencyBytes                      = 16
	browserDiagnosticSchemaSHA256                = "sha256:c7cfb25ad2fe4842cdd1b5a078c495e16d729cc18f81e7054e7becba0d620d40"
	browserDiagnosticOperationInputSchemaRef     = "schema://providers/workflow-plugin-product-capture/browser/operations/browser_diagnostic/input/v1"
	browserDiagnosticOperationInputSchemaSHA256  = "sha256:732d520997602aea2e1f0bc751b5e05bf372608a373f078d9ac4c3eb1ae912f4"
	browserDiagnosticOperationOutputSchemaRef    = "schema://providers/workflow-plugin-product-capture/browser/operations/browser_diagnostic/output/v1"
	browserDiagnosticOperationOutputSchemaSHA256 = "sha256:94eb33379184a7f00f489c7bc018afff76e0abb4a675609b541ed3cf61ef155e"
)

var canonicalPricePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{2})?$`)

type Config struct {
	ServerURL            string
	Token                string
	OrgID                string
	PoolID               string
	ProductID            string
	PolicyID             string
	WorkerID             string
	ProductURL           string
	AllowedHost          string
	BrowserDiagnosticURL string
	ProviderImageRef     string
	Contract             protocol.ProviderContract
	ProductSchema        []byte
	DiagnosticSchema     []byte
	PollInterval         time.Duration
	CapacityTimeout      time.Duration
	ResultTimeout        time.Duration
	ArtifactTimeout      time.Duration
	TaskTimeoutSeconds   int
}

type Summary struct {
	SchemaVersion     string                      `json:"schema_version"`
	Capacity          CapacitySummary             `json:"capacity"`
	TaskID            string                      `json:"task_id"`
	TaskStatus        protocol.TaskStatus         `json:"task_status"`
	ProofID           string                      `json:"proof_id"`
	ProofStatus       protocol.VerificationStatus `json:"proof_status"`
	WorkerID          string                      `json:"worker_id"`
	ArtifactHash      string                      `json:"artifact_hash"`
	ProviderImageRef  string                      `json:"provider_image_ref"`
	Executor          protocol.ExecutorRef        `json:"executor"`
	Product           ProductSummary              `json:"product"`
	Artifacts         []ArtifactSummary           `json:"artifacts"`
	BrowserDiagnostic *DiagnosticSummary          `json:"browser_diagnostic,omitempty"`
}

type CapacitySummary struct {
	WorkerStatus           protocol.AgentStatus `json:"worker_status"`
	RetainedWorkerMatching bool                 `json:"retained_worker_matching"`
	MatchingOnlineAgents   int                  `json:"matching_online_agents"`
	ActiveMatchingLeases   int                  `json:"active_matching_leases"`
	QueuedMatchingTasks    int                  `json:"queued_matching_tasks"`
	Digest                 string               `json:"digest"`
}

type ProductSummary struct {
	Title    string `json:"title"`
	ImageURL string `json:"image_url"`
	Price    string `json:"price"`
	Currency string `json:"currency,omitempty"`
}

type ArtifactSummary struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
}

type DiagnosticSummary struct {
	TaskID       string                      `json:"task_id"`
	ProofID      string                      `json:"proof_id"`
	ProofStatus  protocol.VerificationStatus `json:"proof_status"`
	SchemaSHA256 string                      `json:"schema_sha256"`
	Artifact     ArtifactSummary             `json:"artifact"`
}

type productArtifact struct {
	Provider     string `json:"provider"`
	URL          string `json:"url"`
	RequestedURL string `json:"requested_url"`
	CanonicalURL string `json:"canonical_url"`
	ExternalID   string `json:"external_id"`
	Title        string `json:"title"`
	ImageURL     string `json:"image_url"`
	Price        string `json:"price"`
	Currency     string `json:"currency"`
}

type browserDiagnosticArtifact struct {
	TargetURL      string                     `json:"target_url"`
	FinalURL       string                     `json:"final_url"`
	PostedToOrigin bool                       `json:"posted_to_origin"`
	PostError      string                     `json:"post_error"`
	BrowserSignals map[string]json.RawMessage `json:"browser_signals"`
}

func Run(ctx context.Context, cfg Config) (Summary, error) {
	cfg = withDefaults(cfg)
	productOperation, productSchema, err := validateConfig(cfg)
	if err != nil {
		return Summary{}, err
	}
	var diagnosticSchema *jsonschema.Schema
	if cfg.BrowserDiagnosticURL != "" {
		diagnosticSchema, err = compileRootSchema(cfg.DiagnosticSchema, "https://proof.invalid/browser-diagnostic-result.schema.json")
		if err != nil {
			return Summary{}, fmt.Errorf("browser diagnostic schema: %w", err)
		}
	}
	client, err := protocol.NewClient(protocol.ClientConfig{
		ServerURL:  cfg.ServerURL,
		Token:      cfg.Token,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	})
	if err != nil {
		return Summary{}, fmt.Errorf("create compute client: %w", err)
	}

	capacity, executor, err := waitForCapacity(ctx, client, cfg)
	if err != nil {
		return Summary{}, err
	}
	task, err := submitProductTask(ctx, client, cfg)
	if err != nil {
		return Summary{}, fmt.Errorf("submit product task: %w", err)
	}
	task, proof, err := waitForAcceptedProof(ctx, client, cfg, task, executor)
	if err != nil {
		return Summary{}, err
	}
	artifacts, product, err := downloadProductArtifacts(ctx, client, cfg, task, proof, productOperation, productSchema)
	if err != nil {
		return Summary{}, err
	}
	var diagnostic *DiagnosticSummary
	if cfg.BrowserDiagnosticURL != "" {
		_, diagnosticExecutor, err := waitForCapacity(ctx, client, cfg)
		if err != nil {
			return Summary{}, fmt.Errorf("wait for diagnostic capacity: %w", err)
		}
		diagnosticTask, err := submitDiagnosticTask(ctx, client, cfg)
		if err != nil {
			return Summary{}, fmt.Errorf("submit browser diagnostic task: %w", err)
		}
		diagnosticTask, diagnosticProof, err := waitForAcceptedProof(ctx, client, cfg, diagnosticTask, diagnosticExecutor)
		if err != nil {
			return Summary{}, fmt.Errorf("browser diagnostic: %w", err)
		}
		diagnosticOperation, _ := providerOperation(cfg.Contract, "browser_diagnostic")
		diagnosticArtifact, err := downloadDiagnosticArtifact(ctx, client, cfg, diagnosticTask, diagnosticProof, diagnosticOperation, diagnosticSchema)
		if err != nil {
			return Summary{}, err
		}
		diagnostic = &DiagnosticSummary{
			TaskID: diagnosticTask.ID, ProofID: diagnosticProof.ID,
			ProofStatus: diagnosticProof.Verifier.Status, SchemaSHA256: browserDiagnosticSchemaSHA256,
			Artifact: diagnosticArtifact,
		}
	}

	return Summary{
		SchemaVersion:     "product-capture-staging-proof.v1",
		Capacity:          capacity,
		TaskID:            task.ID,
		TaskStatus:        task.Status,
		ProofID:           proof.ID,
		ProofStatus:       proof.Verifier.Status,
		WorkerID:          proof.WorkerID,
		ArtifactHash:      proof.ArtifactHash,
		ProviderImageRef:  cfg.ProviderImageRef,
		Executor:          executor,
		Product:           product,
		Artifacts:         artifacts,
		BrowserDiagnostic: diagnostic,
	}, nil
}

func withDefaults(cfg Config) Config {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.CapacityTimeout <= 0 {
		cfg.CapacityTimeout = defaultCapacityTimeout
	}
	if cfg.ResultTimeout <= 0 {
		cfg.ResultTimeout = defaultResultTimeout
	}
	if cfg.ArtifactTimeout <= 0 {
		cfg.ArtifactTimeout = defaultArtifactTimeout
	}
	if cfg.TaskTimeoutSeconds <= 0 {
		cfg.TaskTimeoutSeconds = defaultTaskTimeoutSeconds
	} else if cfg.TaskTimeoutSeconds > maxTaskTimeoutSeconds {
		cfg.TaskTimeoutSeconds = maxTaskTimeoutSeconds
	}
	return cfg
}

func validateConfig(cfg Config) (protocol.ProviderOperation, *jsonschema.Schema, error) {
	for name, value := range map[string]string{
		"server_url":   cfg.ServerURL,
		"token":        cfg.Token,
		"org_id":       cfg.OrgID,
		"pool_id":      cfg.PoolID,
		"product_id":   cfg.ProductID,
		"policy_id":    cfg.PolicyID,
		"worker_id":    cfg.WorkerID,
		"product_url":  cfg.ProductURL,
		"allowed_host": cfg.AllowedHost,
	} {
		if strings.TrimSpace(value) == "" {
			return protocol.ProviderOperation{}, nil, fmt.Errorf("%s is required", name)
		}
	}
	if err := validateImageRef(cfg.ProviderImageRef); err != nil {
		return protocol.ProviderOperation{}, nil, err
	}
	parsedProductURL, err := url.ParseRequestURI(cfg.ProductURL)
	if err != nil || (parsedProductURL.Scheme != "http" && parsedProductURL.Scheme != "https") || parsedProductURL.Hostname() == "" || parsedProductURL.User != nil {
		return protocol.ProviderOperation{}, nil, errors.New("product_url must be an absolute http(s) URL without user info")
	}
	if !strings.EqualFold(parsedProductURL.Hostname(), cfg.AllowedHost) {
		return protocol.ProviderOperation{}, nil, errors.New("allowed_host must exactly match product_url host")
	}
	if snapshot.AmazonASINFromURL(cfg.ProductURL) == "" {
		return protocol.ProviderOperation{}, nil, errors.New("product_url must contain a supported Amazon ASIN path")
	}
	if cfg.BrowserDiagnosticURL != "" {
		diagnosticURL, err := url.ParseRequestURI(cfg.BrowserDiagnosticURL)
		if err != nil || diagnosticURL.Scheme != "https" || diagnosticURL.Hostname() == "" || diagnosticURL.User != nil {
			return protocol.ProviderOperation{}, nil, errors.New("browser_diagnostic_url must be an absolute HTTPS URL without user info")
		}
		if sha256Ref(cfg.DiagnosticSchema) != browserDiagnosticSchemaSHA256 {
			return protocol.ProviderOperation{}, nil, errors.New("diagnostic schema digest does not match the pinned artifact schema")
		}
	}
	if err := cfg.Contract.Validate(); err != nil {
		return protocol.ProviderOperation{}, nil, fmt.Errorf("provider contract: %w", err)
	}
	operation, ok := providerOperation(cfg.Contract, "capture_product")
	if !ok {
		return protocol.ProviderOperation{}, nil, errors.New("provider contract does not declare capture_product")
	}
	if len(operation.ArtifactSpecs) == 0 {
		return protocol.ProviderOperation{}, nil, errors.New("capture_product must declare bounded artifact_specs")
	}
	if sha256Ref(cfg.ProductSchema) != operation.OutputSchemaDigest {
		return protocol.ProviderOperation{}, nil, errors.New("product schema does not match capture_product output_schema_digest")
	}
	if cfg.BrowserDiagnosticURL != "" {
		diagnostic, ok := providerOperation(cfg.Contract, "browser_diagnostic")
		if !ok || len(diagnostic.ArtifactSpecs) == 0 {
			return protocol.ProviderOperation{}, nil, errors.New("browser_diagnostic must declare bounded artifact_specs")
		}
		if diagnostic.InputSchemaRef != browserDiagnosticOperationInputSchemaRef {
			return protocol.ProviderOperation{}, nil, errors.New("browser_diagnostic input_schema_ref does not match the pinned operation input schema")
		}
		if diagnostic.InputSchemaDigest != browserDiagnosticOperationInputSchemaSHA256 {
			return protocol.ProviderOperation{}, nil, errors.New("browser_diagnostic input_schema_digest does not match the pinned operation input schema")
		}
		if diagnostic.OutputSchemaRef != browserDiagnosticOperationOutputSchemaRef {
			return protocol.ProviderOperation{}, nil, errors.New("browser_diagnostic output_schema_ref does not match the pinned operation output schema")
		}
		if diagnostic.OutputSchemaDigest != browserDiagnosticOperationOutputSchemaSHA256 {
			return protocol.ProviderOperation{}, nil, errors.New("browser_diagnostic output_schema_digest does not match the pinned operation output schema")
		}
	}
	schema, err := compileProductSchema(cfg.ProductSchema)
	if err != nil {
		return protocol.ProviderOperation{}, nil, fmt.Errorf("product schema: %w", err)
	}
	return operation, schema, nil
}

func validateImageRef(ref string) error {
	if strings.TrimSpace(ref) != ref || strings.ContainsAny(ref, " \t\r\n\x00") {
		return errors.New("provider_image_ref must be digest-pinned without whitespace")
	}
	name, digest, ok := strings.Cut(ref, "@")
	if !ok || name == "" || !validSHA256(digest) {
		return errors.New("provider_image_ref must be digest-pinned with @sha256:<64 lowercase hex>")
	}
	return nil
}

func compileProductSchema(data []byte) (*jsonschema.Schema, error) {
	const resource = "https://proof.invalid/product-output.schema.json"
	return compileSchema(data, resource, resource+"#/$defs/product_json")
}

func compileRootSchema(data []byte, resource string) (*jsonschema.Schema, error) {
	return compileSchema(data, resource, resource)
}

func compileSchema(data []byte, resource, target string) (*jsonschema.Schema, error) {
	var document any
	if len(data) == 0 {
		return nil, errors.New("schema is required")
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, err
	}
	return compiler.Compile(target)
}

func providerOperation(contract protocol.ProviderContract, id string) (protocol.ProviderOperation, bool) {
	for _, operation := range contract.Operations {
		if operation.ID == id {
			return operation, true
		}
	}
	return protocol.ProviderOperation{}, false
}

func waitForCapacity(ctx context.Context, client *protocol.Client, cfg Config) (CapacitySummary, protocol.ExecutorRef, error) {
	waitCtx, cancel := context.WithTimeout(ctx, cfg.CapacityTimeout)
	defer cancel()
	var last CapacitySummary
	for {
		agents, err := retryTransient(waitCtx, cfg.PollInterval, func() ([]protocol.Agent, error) {
			return client.ListAgents(waitCtx)
		})
		if err != nil {
			return CapacitySummary{}, protocol.ExecutorRef{}, fmt.Errorf("list capacity agents: %w", err)
		}
		leases, err := retryTransient(waitCtx, cfg.PollInterval, func() ([]protocol.Lease, error) {
			return client.ListLeases(waitCtx)
		})
		if err != nil {
			return CapacitySummary{}, protocol.ExecutorRef{}, fmt.Errorf("list capacity leases: %w", err)
		}
		tasks, err := retryTransient(waitCtx, cfg.PollInterval, func() (protocol.TaskList, error) {
			return client.ListTasks(waitCtx)
		})
		if err != nil {
			return CapacitySummary{}, protocol.ExecutorRef{}, fmt.Errorf("list capacity tasks: %w", err)
		}
		last = summarizeCapacity(cfg, agents, leases, tasks.Tasks, time.Now().UTC())
		if last.MatchingOnlineAgents == 1 && last.RetainedWorkerMatching && last.WorkerStatus == protocol.AgentOnline &&
			last.ActiveMatchingLeases == 0 && last.QueuedMatchingTasks == 0 {
			for _, agent := range agents {
				if agent.ID == cfg.WorkerID {
					if executor, ok := compatibleExecutor(agent, cfg); ok {
						return last, executor, nil
					}
				}
			}
			return CapacitySummary{}, protocol.ExecutorRef{}, errors.New("retained worker executor identity disappeared during capacity check")
		}
		if err := waitForPoll(waitCtx, cfg.PollInterval); err != nil {
			return CapacitySummary{}, protocol.ExecutorRef{}, fmt.Errorf("capacity unavailable before timeout: online=%d active_leases=%d queued_tasks=%d worker_status=%s",
				last.MatchingOnlineAgents, last.ActiveMatchingLeases, last.QueuedMatchingTasks, last.WorkerStatus)
		}
	}
}

func summarizeCapacity(cfg Config, agents []protocol.Agent, leases []protocol.Lease, tasks []protocol.Task, now time.Time) CapacitySummary {
	matchingIDs := map[string]struct{}{}
	status := protocol.AgentUnknown
	retainedWorkerMatching := false
	for _, agent := range agents {
		if agent.ID == cfg.WorkerID {
			status = agent.Status
		}
		if agentCompatible(agent, cfg) {
			matchingIDs[agent.ID] = struct{}{}
			if agent.ID == cfg.WorkerID {
				retainedWorkerMatching = true
			}
		}
	}
	activeLeases := 0
	for _, lease := range leases {
		if _, ok := matchingIDs[lease.WorkerID]; ok && lease.ExpiresAt.After(now) {
			activeLeases++
		}
	}
	queuedTasks := 0
	for _, task := range tasks {
		if task.Status == protocol.TaskQueued && task.OrgID == cfg.OrgID && task.PoolID == cfg.PoolID &&
			task.ProductID == cfg.ProductID && task.Workload.Kind == protocol.WorkloadProvider {
			queuedTasks++
		}
	}
	summary := CapacitySummary{
		WorkerStatus:           status,
		RetainedWorkerMatching: retainedWorkerMatching,
		MatchingOnlineAgents:   len(matchingIDs),
		ActiveMatchingLeases:   activeLeases,
		QueuedMatchingTasks:    queuedTasks,
	}
	digestInput, _ := json.Marshal(summary)
	summary.Digest = sha256Ref(digestInput)
	return summary
}

func agentCompatible(agent protocol.Agent, cfg Config) bool {
	_, ok := compatibleExecutor(agent, cfg)
	return ok
}

func compatibleExecutor(agent protocol.Agent, cfg Config) (protocol.ExecutorRef, bool) {
	if agent.Status != protocol.AgentOnline {
		return protocol.ExecutorRef{}, false
	}
	if _, ok := protocol.SelectIdleAgent([]protocol.Agent{agent}, nil, cfg.OrgID, cfg.PoolID, time.Now().UTC()); !ok {
		return protocol.ExecutorRef{}, false
	}
	caps := agent.Capabilities
	if !slices.Contains(caps.ExecutorProviders, "product-capture-browser") ||
		!slices.Contains(caps.WorkloadKinds, string(protocol.WorkloadProvider)) ||
		!slices.Contains(caps.ExecutionTiers, protocol.ExecutionSandboxedContainer) ||
		!slices.Contains(caps.ProofTiers, protocol.ProofArtifactHash) {
		return protocol.ExecutorRef{}, false
	}
	for _, executor := range caps.Executors {
		if executor.Provider == "product-capture-browser" &&
			executor.ExecutionSecurityTier == protocol.ExecutionSandboxedContainer && executor.ProofTier == protocol.ProofArtifactHash &&
			executor.Version != "" && strings.TrimSpace(executor.Version) == executor.Version && !strings.ContainsAny(executor.Version, " \t\r\n\x00") &&
			validSHA256(executor.ImageDigest) && validSHA256(executor.RootFSDigest) {
			if err := executor.ValidateForProof(); err == nil {
				return executor, true
			}
		}
	}
	return protocol.ExecutorRef{}, false
}

func submitProductTask(ctx context.Context, client *protocol.Client, cfg Config) (protocol.Task, error) {
	input, err := json.Marshal(map[string]any{
		"url":             cfg.ProductURL,
		"allowed_hosts":   []string{cfg.AllowedHost},
		"capture_mode":    string(protocol.ProductCaptureModeBrowser),
		"timeout_seconds": cfg.TaskTimeoutSeconds,
		"max_html_bytes":  1 << 20,
		"max_image_count": 8,
	})
	if err != nil {
		return protocol.Task{}, err
	}
	return submitProviderTask(ctx, client, cfg, "capture_product", input)
}

func submitDiagnosticTask(ctx context.Context, client *protocol.Client, cfg Config) (protocol.Task, error) {
	input, err := json.Marshal(map[string]string{"url": cfg.BrowserDiagnosticURL})
	if err != nil {
		return protocol.Task{}, err
	}
	return submitProviderTask(ctx, client, cfg, "browser_diagnostic", input)
}

func submitProviderTask(ctx context.Context, client *protocol.Client, cfg Config, operation string, input json.RawMessage) (protocol.Task, error) {
	workload := protocol.WorkloadSpec{
		Kind: protocol.WorkloadProvider,
		Provider: &protocol.ProviderWorkload{
			ProviderConfig: protocol.ProviderConfig{
				PluginID:   cfg.Contract.PluginID,
				ProviderID: cfg.Contract.ProviderID,
				ContractID: cfg.Contract.ContractID,
				Version:    cfg.Contract.Version,
				ConfigRef:  "config://network-products/" + cfg.ProductID + "/" + cfg.Contract.ProviderID,
			},
			Operation: operation,
			ImageRef:  cfg.ProviderImageRef,
			Input:     input,
		},
	}
	now := time.Now().UTC()
	inputHash := sha256Ref(mustJSON(workload))
	taskID := "task-product-capture-" + strings.ReplaceAll(operation, "_", "-") + "-" + strings.TrimPrefix(sha256Ref([]byte(now.Format(time.RFC3339Nano)+inputHash)), "sha256:")[:20]
	task := protocol.Task{
		ProtocolVersion: protocol.Version,
		ID:              taskID,
		ProductID:       cfg.ProductID,
		OrgID:           cfg.OrgID,
		PoolID:          cfg.PoolID,
		PolicyID:        cfg.PolicyID,
		Status:          protocol.TaskQueued,
		Workload:        workload,
		Requirements: protocol.PlacementRequirements{
			ExecutorProvider:      "product-capture-browser",
			ExecutionSecurityTier: protocol.ExecutionSandboxedContainer,
			ProofTier:             protocol.ProofArtifactHash,
		},
		InputHash:      inputHash,
		RequestedAt:    now,
		TimeoutSeconds: cfg.TaskTimeoutSeconds,
		Signature: protocol.SignatureEnvelope{
			Algorithm: "dev-local-sha256",
			KeyID:     "local-dev",
			Value:     strings.TrimPrefix(sha256Ref([]byte(taskID+":"+inputHash)), "sha256:"),
		},
	}
	if err := task.Validate(); err != nil {
		return protocol.Task{}, fmt.Errorf("build task: %w", err)
	}
	created, err := client.SubmitTask(ctx, task)
	if err != nil {
		return protocol.Task{}, err
	}
	if err := validateSubmittedTask(created, task); err != nil {
		return protocol.Task{}, err
	}
	return task, nil
}

func validateSubmittedTask(created, expected protocol.Task) error {
	if err := created.Validate(); err != nil {
		return fmt.Errorf("submitted task response: %w", err)
	}
	if taskBindingHash(created) != taskBindingHash(expected) {
		return errors.New("submitted task response does not match requested provider task")
	}
	return nil
}

func taskBindingHash(task protocol.Task) string {
	task.Status = ""
	task.Signature.Verified = false
	return protocol.CanonicalHash(task)
}

func waitForAcceptedProof(ctx context.Context, client *protocol.Client, cfg Config, expected protocol.Task, expectedExecutor protocol.ExecutorRef) (protocol.Task, protocol.ProofReceipt, error) {
	waitCtx, cancel := context.WithTimeout(ctx, cfg.ResultTimeout)
	defer cancel()
	taskID := expected.ID
	for {
		snapshot, err := retryTransient(waitCtx, cfg.PollInterval, func() (taskSnapshot, error) {
			task, found, stalls, err := client.TaskSnapshot(waitCtx, taskID)
			return taskSnapshot{Task: task, Found: found, Stalls: stalls}, err
		})
		if err != nil {
			return protocol.Task{}, protocol.ProofReceipt{}, fmt.Errorf("read task: %w", err)
		}
		task, found, stalls := snapshot.Task, snapshot.Found, snapshot.Stalls
		if len(stalls) > 0 {
			return protocol.Task{}, protocol.ProofReceipt{}, fmt.Errorf("task %s stalled: %s", taskID, stalls[0].Reason)
		}
		if found {
			if taskBindingHash(task) != taskBindingHash(expected) {
				return protocol.Task{}, protocol.ProofReceipt{}, errors.New("terminal task does not match submitted provider task")
			}
			switch task.Status {
			case protocol.TaskFailed, protocol.TaskStalled, protocol.TaskCanceled:
				return protocol.Task{}, protocol.ProofReceipt{}, fmt.Errorf("task %s ended with status %s", taskID, task.Status)
			case protocol.TaskSucceeded:
				foundProof, err := retryTransient(waitCtx, cfg.PollInterval, func() (proofSnapshot, error) {
					proof, ok, err := client.FindProof(waitCtx, taskID)
					return proofSnapshot{Proof: proof, Found: ok}, err
				})
				if err != nil {
					return protocol.Task{}, protocol.ProofReceipt{}, fmt.Errorf("read proof: %w", err)
				}
				proof, ok := foundProof.Proof, foundProof.Found
				if ok && proof.Verifier.Status != protocol.VerificationPending && proof.Verifier.Status != protocol.VerificationUnknown {
					if proof.Verifier.Status != protocol.VerificationAccepted {
						return protocol.Task{}, protocol.ProofReceipt{}, fmt.Errorf("proof %s is %s", proof.ID, proof.Verifier.Status)
					}
					if err := validateAcceptedProof(proof, task, expected, cfg, expectedExecutor); err != nil {
						return protocol.Task{}, protocol.ProofReceipt{}, err
					}
					return task, proof, nil
				}
			}
		}
		if err := waitForPoll(waitCtx, cfg.PollInterval); err != nil {
			return protocol.Task{}, protocol.ProofReceipt{}, fmt.Errorf("timed out waiting for task %s proof", taskID)
		}
	}
}

func validateAcceptedProof(proof protocol.ProofReceipt, terminal, expected protocol.Task, cfg Config, expectedExecutor protocol.ExecutorRef) error {
	if err := proof.Validate(); err != nil {
		return fmt.Errorf("accepted proof receipt: %w", err)
	}
	if proof.TaskID != expected.ID || proof.OrgID != expected.OrgID || proof.PoolID != expected.PoolID ||
		proof.PolicyID != expected.PolicyID || proof.InputHash != expected.InputHash || proof.WorkerID != cfg.WorkerID {
		return errors.New("accepted proof identity does not match submitted task and retained worker")
	}
	leased := terminal
	leased.Status = protocol.TaskLeased
	if proof.TaskHash != protocol.CanonicalHash(leased) {
		return errors.New("accepted proof task hash does not match leased task")
	}
	if proof.DependencyClosureHash != protocol.CanonicalHash(expected.Workload) {
		return errors.New("accepted proof dependency closure does not match submitted workload")
	}
	if protocol.CanonicalHash(proof.Executor) != protocol.CanonicalHash(expectedExecutor) {
		return errors.New("accepted proof executor does not match requested runtime")
	}
	if proof.ExitCode != 0 {
		return errors.New("accepted proof exit_code is nonzero")
	}
	return nil
}

type taskSnapshot struct {
	Task   protocol.Task
	Found  bool
	Stalls []protocol.TaskStall
}

type proofSnapshot struct {
	Proof protocol.ProofReceipt
	Found bool
}

func downloadProductArtifacts(ctx context.Context, client *protocol.Client, cfg Config, task protocol.Task, proof protocol.ProofReceipt, operation protocol.ProviderOperation, schema *jsonschema.Schema) ([]ArtifactSummary, ProductSummary, error) {
	artifactCtx, cancel := context.WithTimeout(ctx, cfg.ArtifactTimeout)
	defer cancel()
	metadata, err := retryTransient(artifactCtx, cfg.PollInterval, func() ([]protocol.TaskArtifact, error) {
		return client.ListTaskArtifacts(artifactCtx, task.ID)
	})
	if err != nil {
		return nil, ProductSummary{}, fmt.Errorf("list task artifacts: %w", err)
	}
	specs := operation.NormalizedArtifactSpecs()
	if len(metadata) != len(specs) {
		return nil, ProductSummary{}, fmt.Errorf("task returned %d artifacts; contract declares %d", len(metadata), len(specs))
	}
	byName := make(map[string]protocol.TaskArtifact, len(metadata))
	for _, artifact := range metadata {
		if _, exists := byName[artifact.Name]; exists {
			return nil, ProductSummary{}, fmt.Errorf("artifact %q is duplicated", artifact.Name)
		}
		byName[artifact.Name] = artifact
	}
	result := make([]ArtifactSummary, 0, len(specs))
	bodies := make(map[string][]byte, len(specs))
	var product ProductSummary
	for _, spec := range specs {
		artifact, ok := byName[spec.Name]
		if !ok {
			return nil, ProductSummary{}, fmt.Errorf("declared artifact %q is missing", spec.Name)
		}
		if err := validateArtifactMetadata(task, proof, artifact, spec); err != nil {
			return nil, ProductSummary{}, err
		}
		body, err := retryTransient(artifactCtx, cfg.PollInterval, func() ([]byte, error) {
			return client.DownloadTaskArtifact(artifactCtx, artifact.Ref, spec.MaxBytes)
		})
		if err != nil {
			return nil, ProductSummary{}, fmt.Errorf("download artifact %q: %w", spec.Name, err)
		}
		if int64(len(body)) != artifact.SizeBytes {
			return nil, ProductSummary{}, fmt.Errorf("artifact %q byte length does not match size_bytes", spec.Name)
		}
		if sha256Ref(body) != artifact.SHA256 {
			return nil, ProductSummary{}, fmt.Errorf("artifact %q digest mismatch", spec.Name)
		}
		if !json.Valid(body) {
			return nil, ProductSummary{}, fmt.Errorf("artifact %q is not valid JSON", spec.Name)
		}
		var document any
		if err := json.Unmarshal(body, &document); err != nil {
			return nil, ProductSummary{}, fmt.Errorf("decode artifact %q: %w", spec.Name, err)
		}
		if err := schema.Validate(document); err != nil {
			return nil, ProductSummary{}, fmt.Errorf("artifact %q violates product schema: %w", spec.Name, err)
		}
		var captured productArtifact
		if err := json.Unmarshal(body, &captured); err != nil {
			return nil, ProductSummary{}, fmt.Errorf("decode product summary: %w", err)
		}
		product, err = boundedProductSummary(captured, cfg)
		if err != nil {
			return nil, ProductSummary{}, err
		}
		bodies[artifact.Name] = body
		result = append(result, ArtifactSummary{
			Name: artifact.Name, ContentType: artifact.ContentType,
			SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
		})
	}
	if err := validateArtifactHash(proof, bodies); err != nil {
		return nil, ProductSummary{}, err
	}
	return result, product, nil
}

func downloadDiagnosticArtifact(ctx context.Context, client *protocol.Client, cfg Config, task protocol.Task, proof protocol.ProofReceipt, operation protocol.ProviderOperation, schema *jsonschema.Schema) (ArtifactSummary, error) {
	artifactCtx, cancel := context.WithTimeout(ctx, cfg.ArtifactTimeout)
	defer cancel()
	metadata, err := retryTransient(artifactCtx, cfg.PollInterval, func() ([]protocol.TaskArtifact, error) {
		return client.ListTaskArtifacts(artifactCtx, task.ID)
	})
	if err != nil {
		return ArtifactSummary{}, fmt.Errorf("list browser diagnostic artifacts: %w", err)
	}
	specs := operation.NormalizedArtifactSpecs()
	if len(specs) != 1 || len(metadata) != 1 || metadata[0].Name != specs[0].Name {
		return ArtifactSummary{}, errors.New("browser diagnostic must return its one declared artifact")
	}
	artifact := metadata[0]
	if err := validateArtifactMetadata(task, proof, artifact, specs[0]); err != nil {
		return ArtifactSummary{}, err
	}
	body, err := retryTransient(artifactCtx, cfg.PollInterval, func() ([]byte, error) {
		return client.DownloadTaskArtifact(artifactCtx, artifact.Ref, specs[0].MaxBytes)
	})
	if err != nil {
		return ArtifactSummary{}, fmt.Errorf("download browser diagnostic artifact: %w", err)
	}
	if int64(len(body)) != artifact.SizeBytes {
		return ArtifactSummary{}, errors.New("browser diagnostic artifact byte length does not match size_bytes")
	}
	if sha256Ref(body) != artifact.SHA256 {
		return ArtifactSummary{}, errors.New("browser diagnostic artifact digest mismatch")
	}
	if !json.Valid(body) {
		return ArtifactSummary{}, errors.New("browser diagnostic artifact is not valid JSON")
	}
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return ArtifactSummary{}, fmt.Errorf("decode browser diagnostic artifact: %w", err)
	}
	if schema == nil {
		return ArtifactSummary{}, errors.New("browser diagnostic schema is required")
	}
	if err := schema.Validate(document); err != nil {
		return ArtifactSummary{}, fmt.Errorf("browser diagnostic artifact violates schema: %w", err)
	}
	var diagnostic browserDiagnosticArtifact
	if err := json.Unmarshal(body, &diagnostic); err != nil {
		return ArtifactSummary{}, fmt.Errorf("decode browser diagnostic evidence: %w", err)
	}
	if err := validateBrowserDiagnostic(diagnostic, cfg); err != nil {
		return ArtifactSummary{}, err
	}
	if err := validateArtifactHash(proof, map[string][]byte{artifact.Name: body}); err != nil {
		return ArtifactSummary{}, err
	}
	return ArtifactSummary{
		Name: artifact.Name, ContentType: artifact.ContentType,
		SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
	}, nil
}

func validateArtifactHash(proof protocol.ProofReceipt, artifacts map[string][]byte) error {
	actual := providerArtifactHash(artifacts)
	if proof.ArtifactHash != actual {
		return errors.New("accepted proof artifact hash does not match downloaded artifacts")
	}
	return nil
}

func providerArtifactHash(artifacts map[string][]byte) string {
	names := make([]string, 0, len(artifacts))
	for name := range artifacts {
		names = append(names, name)
	}
	slices.Sort(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = fmt.Fprintf(hash, "%s\x00", name)
		_, _ = hash.Write(artifacts[name])
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validateBrowserDiagnostic(diagnostic browserDiagnosticArtifact, cfg Config) error {
	if diagnostic.TargetURL != cfg.BrowserDiagnosticURL {
		return errors.New("browser diagnostic target_url does not match submitted URL")
	}
	target, err := url.ParseRequestURI(diagnostic.TargetURL)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil {
		return errors.New("browser diagnostic target_url must be an absolute HTTPS URL without user info")
	}
	finalURL, err := url.ParseRequestURI(diagnostic.FinalURL)
	if err != nil || finalURL.Scheme != "https" || finalURL.Hostname() == "" || finalURL.User != nil {
		return errors.New("browser diagnostic final_url must be an absolute HTTPS URL without user info")
	}
	if !sameOrigin(target, finalURL) {
		return errors.New("browser diagnostic final_url is not same-origin with target_url")
	}
	if !diagnostic.PostedToOrigin {
		return errors.New("browser diagnostic endpoint did not accept the same-origin observation")
	}
	if diagnostic.PostError != "" {
		return errors.New("browser diagnostic reported a post error")
	}
	if len(diagnostic.BrowserSignals) == 0 {
		return errors.New("browser diagnostic did not return browser signals")
	}
	return nil
}

func sameOrigin(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) &&
		strings.EqualFold(first.Hostname(), second.Hostname()) &&
		effectivePort(first) == effectivePort(second)
}

func effectivePort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	if strings.EqualFold(value.Scheme, "http") {
		return "80"
	}
	return ""
}

func validateArtifactMetadata(task protocol.Task, proof protocol.ProofReceipt, artifact protocol.TaskArtifact, spec protocol.ProviderArtifactSpec) error {
	if spec.Name == "" || spec.ContentType != "application/json" || spec.MaxBytes <= 0 || spec.MaxBytes > 1<<20 {
		return fmt.Errorf("artifact contract for %q is not bounded JSON", spec.Name)
	}
	if artifact.Name != spec.Name {
		return fmt.Errorf("artifact name %q is undeclared", artifact.Name)
	}
	if artifact.TaskID != task.ID || artifact.ProofID != proof.ID || artifact.PoolID != task.PoolID {
		return fmt.Errorf("artifact %q scope does not match accepted task and proof", artifact.Name)
	}
	expectedRef := fmt.Sprintf("artifact://%s/tasks/%s/proofs/%s/artifacts/%s", task.PoolID, task.ID, proof.ID, artifact.Name)
	if artifact.Ref != expectedRef {
		return fmt.Errorf("artifact %q ref is not canonical", artifact.Name)
	}
	if artifact.ContentType != spec.ContentType {
		return fmt.Errorf("artifact %q content type %q does not match %q", artifact.Name, artifact.ContentType, spec.ContentType)
	}
	if artifact.SizeBytes < 0 || artifact.SizeBytes > spec.MaxBytes {
		return fmt.Errorf("artifact %q size %d exceeds contract limit %d", artifact.Name, artifact.SizeBytes, spec.MaxBytes)
	}
	if !validSHA256(artifact.SHA256) {
		return fmt.Errorf("artifact %q sha256 is invalid", artifact.Name)
	}
	return nil
}

func boundedProductSummary(product productArtifact, cfg Config) (ProductSummary, error) {
	if product.Provider != "browser_capture" {
		return ProductSummary{}, errors.New("product provider does not match browser capture output")
	}
	if product.RequestedURL != cfg.ProductURL {
		return ProductSummary{}, errors.New("product requested_url does not match submitted URL")
	}
	resultURL, err := url.ParseRequestURI(product.URL)
	if err != nil || (resultURL.Scheme != "http" && resultURL.Scheme != "https") || resultURL.Hostname() == "" || resultURL.User != nil ||
		!strings.EqualFold(resultURL.Hostname(), cfg.AllowedHost) {
		return ProductSummary{}, errors.New("product url host does not match allowed host")
	}
	requestedASIN := snapshot.AmazonASINFromURL(cfg.ProductURL)
	if snapshot.AmazonASINFromURL(product.URL) != requestedASIN {
		return ProductSummary{}, errors.New("product url ASIN does not match submitted product")
	}
	if product.ExternalID != requestedASIN {
		return ProductSummary{}, errors.New("product external_id does not match submitted product ASIN")
	}
	canonicalURL, err := url.ParseRequestURI(product.CanonicalURL)
	if err != nil || (canonicalURL.Scheme != "http" && canonicalURL.Scheme != "https") || canonicalURL.Hostname() == "" || canonicalURL.User != nil ||
		!strings.EqualFold(canonicalURL.Hostname(), cfg.AllowedHost) || snapshot.AmazonASINFromURL(product.CanonicalURL) != requestedASIN {
		return ProductSummary{}, errors.New("product canonical_url does not match submitted product ASIN")
	}
	for name, value := range map[string]string{
		"title":     product.Title,
		"image_url": product.ImageURL,
		"price":     product.Price,
	} {
		if strings.TrimSpace(value) == "" {
			return ProductSummary{}, fmt.Errorf("product %s is required for staging proof", name)
		}
	}
	if len(product.Title) > maxSummaryTitleBytes || len(product.ImageURL) > maxSummaryImageURLBytes ||
		len(product.Price) > maxSummaryPriceBytes || len(product.Currency) > maxSummaryCurrencyBytes {
		return ProductSummary{}, errors.New("product summary field exceeds bound")
	}
	if !canonicalPricePattern.MatchString(product.Price) || product.Currency != "USD" {
		return ProductSummary{}, errors.New("product price must use canonical USD decimal format")
	}
	imageURL, err := url.ParseRequestURI(product.ImageURL)
	if err != nil || (imageURL.Scheme != "http" && imageURL.Scheme != "https") || imageURL.Hostname() == "" || imageURL.User != nil {
		return ProductSummary{}, errors.New("product image_url must be absolute http(s)")
	}
	return ProductSummary{Title: product.Title, ImageURL: product.ImageURL, Price: product.Price, Currency: product.Currency}, nil
}

func waitForPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryTransient[T any](ctx context.Context, interval time.Duration, operation func() (T, error)) (T, error) {
	var zero T
	for {
		value, err := operation()
		if err == nil {
			return value, nil
		}
		if !isTransientControlError(err) {
			return zero, err
		}
		if waitErr := waitForPoll(ctx, interval); waitErr != nil {
			return zero, errors.Join(waitErr, err)
		}
	}
}

func isTransientControlError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var status protocol.StatusError
	if errors.As(err, &status) {
		return status.StatusCode == http.StatusTooManyRequests || status.StatusCode >= http.StatusInternalServerError
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func sha256Ref(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
