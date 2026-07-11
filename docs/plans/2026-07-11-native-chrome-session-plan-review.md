# Adversarial Plan Review

**Artifact:** `docs/plans/2026-07-11-native-chrome-session.md`
**Phase:** plan

## Cycle 1

**Status:** FAIL

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| P1 | Important | ownership | Manual runner workflow/tests retained product package orchestration | Inventory/genericize workflow, shell, PowerShell, and all tests/docs in Task 5 |
| P2 | Important | release order | GitHub release published before runtime conformance | Keep release draft; publish/notify only after candidate runtime succeeds |
| P3 | Important | runtime validity | compute-core lacked agent/lease/artifact client APIs | Add/release bounded read-only APIs as new PR 1/Task 1 |
| P4 | Important | deploy validity | BMW deploy has no `workflow_dispatch`; command/race invalid | Set staging variable before merge; use existing CI `workflow_run` deploy for exact main SHA |
| P5 | Important | artifact policy | Lexical grep did not bound/reject renamed runtime archives | Use declared `artifact_specs`; propagate in lease; enforce agent/server name/type/size/content |
| P6 | Important | supply chain | `cloudflared` version/digest/lifecycle unspecified | Pin 2026.7.1 official amd64 SHA; version/health check; teardown tests |
| P7 | Important | congestion | Online-worker check omitted capacity/lease/queue policy | Poll matching idle worker, zero active leases/queued tasks, bounded 30-minute wait |
| P8 | Minor | config validity | BMW validation named nonexistent `workflow.yaml` | Use established `wfctl validate --strict --skip-unknown-types app.yaml` |

**Design-class scan:**

| class | result | note |
|---|---|---|
| Project guidance | Finding P1/P5 | Ownership and bounded artifacts incomplete |
| Assumptions | Finding P3/P4/P7 | APIs, trigger, capacity invalid |
| Repo precedent | Finding P1 | Manual runner remained product-specific |
| Artifact precedent | Finding P5 | Generic package channel confused with task results |
| YAGNI | Clean | No spoofing/fork/profile expansion |
| Failure modes | Finding P2/P4/P6/P7 | Release/deploy/tunnel/capacity gaps |
| Security/privacy | Finding P5/P6 | Artifact/tunnel trust gaps |
| Infrastructure | Finding P2/P4/P6 | Invalid gate sequencing |
| Multi-component | Finding P3/P4 | Consumer boundaries not executable |
| Integration proof | Finding P3 | Client lacked required methods |
| UI rendering | Clean | No UI contribution |
| Rollback | Clean | Rollback intent present |
| Simpler alternative | Clean | Digest image preferred over OCI upload |
| User intent | Finding P1/P5/P7 | Move/bounds/congestion incomplete |
| Runtime validity | Finding P3/P4/P8 | APIs/trigger/file invalid |

**Plan-class scan:**

| class | result | note |
|---|---|---|
| Decomposition | Finding P1/P6 | Hidden inventory/tunnel work |
| Verification match | Finding P5/P8 | Grep/file target insufficient |
| Auth chain | Clean | Server-side fulfillment auth specified |
| Serial dependencies | Finding P2/P4/P7 | Release/deploy/capacity ordering |
| Rollback wiring | Clean | Each runtime task has rollback |
| Integration proof | Finding P3/P4 | Commands could not execute |
| Integration matrix | Clean | Matrix complete |
| UI route proof | Clean | No UI route change |
| Infrastructure verification | Finding P2/P4/P6/P7 | Gate checks incomplete |
| Plugin layout | Clean | Lock/install path established |
| Config schema | Finding P8 | Wrong BMW filename |
| Naming match | Finding P1/P4 | Product identifiers/invalid trigger |
| Compile validity | Finding P3/P8 | Missing methods/file |

**Verdict:** Eight findings require plan revision and a second review cycle.
