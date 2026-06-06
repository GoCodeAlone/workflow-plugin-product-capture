package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-compute-core/protocol"
)

func buildTask(cfg taskConfig, workload protocol.WorkloadSpec) protocol.Task {
	id := cfg.ID
	if id == "" {
		id = "task-" + shortHash(time.Now().UTC().Format(time.RFC3339Nano))
	}
	inputHash := workloadHash(workload)
	return protocol.Task{
		ProtocolVersion: protocol.Version,
		ID:              id,
		ProductID:       cfg.ProductID,
		OrgID:           cfg.OrgID,
		PoolID:          cfg.PoolID,
		PolicyID:        cfg.PolicyID,
		Status:          protocol.TaskQueued,
		Workload:        workload,
		InputHash:       inputHash,
		RequestedAt:     time.Now().UTC(),
		TimeoutSeconds:  cfg.TimeoutSeconds,
		Labels:          cfg.Labels,
		Signature: protocol.SignatureEnvelope{
			Algorithm: "dev-local-sha256",
			KeyID:     "local-dev",
			Value:     shortHash(id + ":" + inputHash),
		},
	}
}

func workloadHash(workload protocol.WorkloadSpec) string {
	data, _ := json.Marshal(workload)
	return "sha256:" + shortHash(string(data))
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
