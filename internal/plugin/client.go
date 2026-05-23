package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GoCodeAlone/workflow-compute/pkg/protocol"
)

type computeClient struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

type taskList struct {
	Tasks  []protocol.Task `json:"tasks"`
	Stalls []taskStall     `json:"stalls,omitempty"`
}

type taskStall struct {
	TaskID  string `json:"task_id,omitempty"`
	LeaseID string `json:"lease_id,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	Reason  string `json:"reason"`
	AgeMS   int64  `json:"age_ms"`
}

func newComputeClient(serverURL, token string, timeout time.Duration) (*computeClient, error) {
	parsed, err := url.ParseRequestURI(serverURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("server_url must be absolute http(s) URL")
	}
	if token != "" && parsed.Scheme != "https" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("server_url must use https when auth token is set")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &computeClient{
		baseURL: parsed,
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *computeClient) submitTask(ctx context.Context, task protocol.Task) (protocol.Task, error) {
	var out struct {
		Task protocol.Task `json:"task"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/tasks", task, http.StatusCreated, &out); err != nil {
		return protocol.Task{}, err
	}
	return out.Task, nil
}

func (c *computeClient) listTasks(ctx context.Context) (taskList, error) {
	var out taskList
	if err := c.doJSON(ctx, http.MethodGet, "/v1/tasks", nil, http.StatusOK, &out); err != nil {
		return taskList{}, err
	}
	return out, nil
}

func (c *computeClient) taskSnapshot(ctx context.Context, id string) (protocol.Task, bool, []taskStall, error) {
	list, err := c.listTasks(ctx)
	if err != nil {
		return protocol.Task{}, false, nil, err
	}
	matchingStalls := make([]taskStall, 0)
	for _, stall := range list.Stalls {
		if stall.TaskID == id {
			matchingStalls = append(matchingStalls, stall)
		}
	}
	for _, task := range list.Tasks {
		if task.ID == id {
			return task, true, matchingStalls, nil
		}
	}
	return protocol.Task{}, false, matchingStalls, nil
}

func (c *computeClient) listProofs(ctx context.Context) ([]protocol.ProofReceipt, error) {
	var out struct {
		Proofs []protocol.ProofReceipt `json:"proofs"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/proofs", nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return out.Proofs, nil
}

func (c *computeClient) findProof(ctx context.Context, taskID string) (protocol.ProofReceipt, bool, error) {
	proofs, err := c.listProofs(ctx)
	if err != nil {
		return protocol.ProofReceipt{}, false, err
	}
	for _, proof := range proofs {
		if proof.TaskID == taskID {
			return proof, true, nil
		}
	}
	return protocol.ProofReceipt{}, false, nil
}

func (c *computeClient) doJSON(ctx context.Context, method, path string, body any, want int, out any) error {
	var requestBody *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		requestBody = bytes.NewReader(data)
	} else {
		requestBody = bytes.NewReader(nil)
	}
	endpoint := c.baseURL.JoinPath(path)
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), requestBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: got status %d want %d", method, path, resp.StatusCode, want)
	}
	if out == nil {
		return nil
	}
	return protocol.DecodeStrict(resp.Body, out)
}
