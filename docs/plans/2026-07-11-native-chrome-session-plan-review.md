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

## Cycle 3

**Status:** FAIL

P9-P12 are resolved.

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| P13 | Important | artifact/runtime validity | Task 1 omitted canonical `/artifacts/` in returned refs | Correct grammar; safely parse name segments; add list-to-download server-fixture round trip |
| P14 | Important | deploy ordering | Webhook `ensure` after deploy can persist a signing secret absent from the active revision | Require both ensures before PR 5 deploy; watch success; verify later active revision bindings |
| P15 | Important | integration reachability | BMW readiness cron waits seven days, so the claim/card proof cannot execute | Add environment-backed delay, seven-day default/production and staging zero; exercise real cron/dispatcher |

**Design-class scan:**

| class | result | note |
|---|---|---|
| Project guidance | Clean | Ownership split is correct |
| Assumptions | Finding P13/P15 | Ref shape and immediate fulfillment conflict with code |
| Repo precedent | Clean | Generic provider/runtime retention matches compute |
| Artifact precedent | Finding P13 | Proposed ref grammar differs from stored refs |
| YAGNI | Clean | No spoofing, profile credentials, fork, or OCI upload |
| Failure modes | Finding P14/P15 | Secret drift and seven-day gate unhandled |
| Security/privacy | Finding P14 | Active app may verify with stale signing secret |
| Infrastructure | Finding P14 | Provision/deploy order invalid |
| Multi-component | Finding P13/P14/P15 | Download, webhook, claim boundaries cannot complete |
| Integration proof | Finding P15 | Funded-to-claim path blocked by runtime policy |
| UI rendering | Clean | No contributed UI |
| Rollback | Clean | Existing rollback paths remain defined |
| Simpler alternative | Clean | Digest runtime and bounded JSON remain appropriate |
| User intent | Finding P15 | Required fulfillment/card proof is unreachable |
| Runtime validity | Finding P13/P14/P15 | Real refs, secrets, and timing invalidate run |

**Plan-class scan:**

| class | result | note |
|---|---|---|
| Decomposition | Finding P14/P15 | Secret activation and delay need explicit steps |
| Verification match | Finding P13/P15 | Static checks cannot prove ref/claim runtime |
| Auth chain | Clean | Protected projection/abort auth specified |
| Serial dependencies | Finding P14 | Provision must precede consuming deploy |
| Rollback wiring | Clean | Revert/redeploy/reconciliation present |
| Integration proof | Finding P13/P15 | Download and claim paths cannot run as written |
| Integration matrix | Clean | All declared integrations represented |
| UI route proof | Clean | No new UI route |
| Infrastructure verification | Finding P14 | No watch or post-secret deployment proof |
| Plugin layout | Clean | Generic compute-wasm imports retained |
| Config schema | Clean | Existing app/schema targets are valid |
| Naming match | Finding P13 | Canonical artifact ref identifier wrong |
| Compile validity | Finding P13 | Parser would target nonexistent route |

**Verdict:** P13-P15 require executable ref, deploy, and fulfillment revisions.

## Cycle 4

**Status:** FAIL

P13-P15 are resolved.

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| P16 | Important | Actions authorization | Run-ID metadata gate lacks `actions: read` and a token in the target workflow | Grant `actions: read`; use step-scoped `${{ github.token }}`; test exact workflow/event/conclusion/SHA/order checks |

**Design-class scan:**

| class | result | note |
|---|---|---|
| Project guidance | Clean | Ownership/no-OCI boundary correct |
| Assumptions | Finding P16 | Assumes Actions metadata readable without permission |
| Repo precedent | Clean | Existing deploy/webhook workflows used |
| Artifact precedent | Clean | Canonical bounded route exact |
| YAGNI | Clean | No spoofing, credentials, or runtime upload |
| Failure modes | Finding P16 | Metadata preflight lacks auth setup |
| Security/privacy | Clean | Secret bindings checked without values |
| Infrastructure | Finding P16 | Actions API authorization omitted |
| Multi-component | Finding P16 | Run-ID proof cannot execute |
| Integration proof | Finding P16 | Deploy provenance gate not runnable |
| UI rendering | Clean | No UI route |
| Rollback | Clean | Image/redeploy/card cleanup defined |
| Simpler alternative | Clean | Run-ID gate appropriate once authorized |
| User intent | Clean | Full requested E2E remains covered |
| Runtime validity | Finding P16 | Workflow token contract invalid |

**Plan-class scan:**

| class | result | note |
|---|---|---|
| Decomposition | Clean | Prerequisites explicit |
| Verification match | Finding P16 | actionlint cannot prove API auth |
| Auth chain | Finding P16 | GitHub token permission incomplete |
| Serial dependencies | Clean | Ensure/deploy/proof ordered |
| Rollback wiring | Clean | Task rollback sufficient |
| Integration proof | Finding P16 | Metadata gate not runnable |
| Integration matrix | Clean | All integrations represented |
| UI route proof | Clean | No UI route |
| Infrastructure verification | Finding P16 | API access not granted |
| Plugin layout | Clean | Generic compute-wasm retained |
| Config schema | Clean | Refs/config/delay coherent |
| Naming match | Clean | Canonical ref exact |
| Compile validity | Clean | Planned code shapes plausible |

**Verdict:** P16 requires explicit Actions read authorization and contract proof.

## Cycle 5

**Status:** FAIL

P16 is resolved.

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| P17 | Important | source provenance | Final dispatch omits `--ref` and does not bind proof run `headSha` to deployed SHA | Require main/deploy/expected/workflow/proof-run SHA equality; dispatch `--ref main`; redeploy on advance |

**Design-class scan:**

| class | result | note |
|---|---|---|
| Project guidance | Clean | Ownership/no-OCI boundaries align |
| Assumptions | Finding P17 | Dispatch revision assumed, not enforced |
| Repo precedent | Clean | Existing runner/deploy pattern used |
| Artifact precedent | Clean | Canonical bounded refs exact |
| YAGNI | Clean | No spoofing, profiles, fork, or image upload |
| Failure modes | Finding P17 | Main advance unhandled |
| Security/privacy | Clean | Metadata token is least privilege |
| Infrastructure | Finding P17 | Dispatch revision unpinned |
| Multi-component | Finding P17 | E2E code can differ from deployment |
| Integration proof | Finding P17 | Exact source provenance incomplete |
| UI rendering | Clean | No UI route |
| Rollback | Clean | Restore/redeploy/card cleanup defined |
| Simpler alternative | Clean | SHA equality simpler than temporary refs |
| User intent | Finding P17 | Exact staging proof may use another revision |
| Runtime validity | Finding P17 | Dispatch defaults to current default branch |

**Plan-class scan:**

| class | result | note |
|---|---|---|
| Decomposition | Clean | P17 fits Task 9 |
| Verification match | Finding P17 | Prerequisite runs checked, proof SHA is not |
| Auth chain | Clean | P16 authorization complete |
| Serial dependencies | Finding P17 | Dispatch must match deployed main |
| Rollback wiring | Clean | Existing paths sufficient |
| Integration proof | Finding P17 | Source-to-deployment binding absent |
| Integration matrix | Clean | All integrations represented |
| UI route proof | Clean | No UI route |
| Infrastructure verification | Finding P17 | Dispatch ref behavior unenforced |
| Plugin layout | Clean | Generic compute-wasm retained |
| Config schema | Clean | Delay/environment checks coherent |
| Naming match | Clean | Identifiers consistent |
| Compile validity | Clean | Planned shapes plausible |

**Verdict:** P17 requires immutable proof-source/deployment provenance.
