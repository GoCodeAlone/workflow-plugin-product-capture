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

## Cycle 2

**Status:** FAIL

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| P9 | Important | version/runtime validity | Plan proposed core `v0.5.1`, but mainline latest is `v0.8.3`; consumers already use `v0.5.0`/`v0.7.0` | Release additive APIs as `v0.8.4`; upgrade product-capture and workflow-compute to that exact version |
| P10 | Important | serial dependency | Replacement product proof preceded generic artifact enforcement, leaving acceptance semantics unproven at cutover | Split workflow-compute into enforcement PR 3 and removal PR 4; deploy enforcement before proof, remove old path only after accepted replacement |
| P11 | Important | user-intent drift | Plan removed proof scripts but retained product-specific production runtime (`WorkloadProductCapture`, `ProductCaptureBrowserProvider`, compute-wasm adapter branches) | Remove legacy workload/executor/capability surface and migrate retained callers to generic `WorkloadProvider`/`DynamicProvider` |
| P12 | Minor | artifact policy | Generic enforcement vaguely required OCI-shaped JSON rejection, mixing product schema with transport policy | Generic layer enforces declared name/type/max and JSON syntax; product-capture validates product schema |

**Prior-finding check:** P2/P4/P6/P8 resolved; P1/P3/P5/P7 partial and replaced
by P9-P11.

**Design-class scan:**

| class | result | note |
|---|---|---|
| Project guidance | Finding P11 | Product-specific runtime remains outside its owner |
| Assumptions | Finding P9/P10 | Obsolete release line and undeployed artifact semantics |
| Repo precedent | Finding P11 | Legacy executor path omitted from removal |
| Artifact precedent | Finding P10/P12 | Existing specs need ordered rollout and precise semantics |
| YAGNI | Clean | No spoofing, credentialed profile, or browser fork |
| Failure modes | Finding P10 | Proof can precede enforcement |
| Security/privacy | Finding P12 | Ambiguous archive-shaped JSON is not defensible generic policy |
| Infrastructure | Finding P9/P10 | Dependency and staging order are not executable |
| Multi-component | Finding P10 | Real proof precedes required server capability |
| Integration proof | Finding P9 | Core/server/product versions are not aligned |
| UI rendering | Clean | No contributed UI |
| Rollback | Clean | Image, profile, staging, and card rollback remain defined |
| Simpler alternative | Clean | Digest image plus bounded result artifacts is appropriate |
| User intent | Finding P11 | Product-specific compute move is incomplete |
| Runtime validity | Finding P9/P10 | Version and deployed-consumer prerequisites invalid |

**Plan-class scan:**

| class | result | note |
|---|---|---|
| Decomposition | Finding P10/P11 | Enforcement and removal need separate prerequisite work |
| Verification match | Finding P10/P12 | Proof order and archive classification are invalid |
| Auth chain | Clean | Server-side fulfillment auth and denial proof specified |
| Serial dependencies | Finding P9/P10 | Core release, compute deploy, contract, proof are serial |
| Rollback wiring | Clean | Task-level rollback present |
| Integration proof | Finding P10 | No enforcement proof before product cutover |
| Integration matrix | Clean | Core, compute, product, BMW, Stripe, Chrome represented |
| UI route proof | Clean | No new UI route |
| Infrastructure verification | Finding P9/P10 | Mainline dependency/deploy gate missing |
| Plugin layout | Clean | Existing lock/install host layout applies |
| Config schema | Clean | Corrected artifact specs and wfctl validation |
| Naming match | Finding P11 | Legacy ProductCapture identifiers omitted |
| Compile validity | Clean | Embedded interfaces compile-plausible |

**Verdict:** Three Important findings require version/order/ownership revision;
P12 is a concrete responsibility-boundary cleanup included in the same revision.
