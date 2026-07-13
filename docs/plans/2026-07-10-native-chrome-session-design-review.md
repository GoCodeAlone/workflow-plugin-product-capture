### Adversarial Review Report

**Phase:** design
**Artifact:** `docs/plans/2026-07-10-native-chrome-session-design.md`
**Status:** FAIL

## Cycle 1

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| D1 | Critical | security/privacy | Dynamic diagnostic accepted arbitrary HTTP(S) targets and same-origin POST | Require exact HTTPS origin allowlist, public DNS, same-origin redirects/requests, bounded redacted output |
| D2 | Important | user intent | Validation stopped before two-user Stripe funding and Issuing card | Name existing BMW commerce workflow/test and all required assertions |
| D3 | Important | assumptions | Checked globals alone could not establish native equivalence | Add versioned direct-vs-CDP comparison matrix; make no non-detection claim |
| D4 | Important | failure modes | Child cleanup omitted Xvfb/Chrome process tree | Add process-group TERM/KILL/reap and container/cgroup parent-death tests |
| D5 | Important | integration proof | Components lacked class, owner, and scenario evidence | Add integration matrix and application-owned BMW scenario |
| D6 | Important | infrastructure | Mutable Chrome image was pushed before runtime proof | Test the exact candidate before push; promote by immutable digest |
| D7 | Important | rollback | Chrome profile downgrade compatibility was assumed | Drain/archive/reset anonymous profile on incompatible rollback |
| D8 | Minor | rollout | Headed default could break standalone deployments | Preserve binary default; set and preflight headed mode in image |

**Bug-class scan transcript:**

| class | result | note |
|---|---|---|
| Project-guidance conflicts | Finding D2/D5 | Real application path and owner were incomplete |
| Assumptions under attack | Finding D3 | A3 overstated checked-global evidence |
| Repo-precedent conflicts | Clean | Generic runtime remains here; compute semantics remain elsewhere |
| Artifact-class precedent | Finding D5 | Scenario belongs to existing BMW workflow/test |
| YAGNI violations | Clean | Fork/renaming and identity knobs remain rejected |
| Missing failure modes | Finding D4 | Process tree/profile lock cleanup absent |
| Security/privacy | Finding D1 | Diagnostic destination was unconstrained |
| Infrastructure impact | Finding D6/D8 | Candidate gating and headed rollout absent |
| Multi-component validation | Finding D2 | Funding and Issuing assertions absent |
| Declared integration proof | Finding D5 | No integration matrix |
| Contributed UI rendering proof | Clean | No UI contribution |
| Rollback story | Finding D7 | Profile downgrade/reset absent |
| Simpler alternative | Clean | Plain Playwright launch considered and rejected |
| User-intent drift | Finding D2 | Ultimate BMW/Stripe outcome omitted |
| Existence/runtime validity | Finding D6 | Built-image CDP launch test absent |

**Options considered:** trusted fixed diagnostic endpoint; pre-promotion candidate
digest; application-owned BMW proof. All are incorporated in the revision.

**Verdict reasoning:** Critical/Important findings require design revision and a
second adversarial review cycle.

## Cycle 2

**Status:** FAIL

**D1-D8:** Resolved by cycle 1 revision; implementation unverified.

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| D9 | Important | security/privacy | Initial DNS validation allowed later rebinding | Pin allowlisted host to validated public IP for diagnostic Chrome process |
| D10 | Important | user intent/failure | Existing BMW proof submits fake order and lacks card cleanup | Add admin abort-purchase path; cancel card in test `finally`; never submit fake order |
| D11 | Important | integration proof | Final rows did not prove distinct users/contributions/PaymentIntents | Assert all three identity linkages in funded state |
| D12 | Important | runtime validity | Candidate preservation between smoke and push unspecified | Build once into Docker content store, smoke image ID/tag, push without rebuild |
| D13 | Minor | profile trust | Operator profile path could reference an authenticated mount | Accepted: unprivileged dedicated container; path is trusted operator config, not workload input |

**Bug-class scan transcript:**

| class | result | note |
|---|---|---|
| Project-guidance conflicts | Clean | Ownership boundaries now match repo guidance |
| Assumptions under attack | Finding D9/D13 | DNS and operator profile trust challenged |
| Repo-precedent conflicts | Clean | Provider/compute/BMW boundaries preserved |
| Artifact-class precedent | Clean | Existing BMW scenario owns commerce proof |
| YAGNI violations | Clean | No identity-tuning/fork additions |
| Missing failure modes | Finding D10 | Active-card cleanup absent |
| Security/privacy | Finding D9/D13 | DNS rebinding and operator mount trust |
| Infrastructure impact | Finding D12 | Same candidate bytes not guaranteed |
| Multi-component validation | Finding D11 | Contributor attribution incomplete |
| Declared integration proof | Finding D11 | Stripe row linkage incomplete |
| Contributed UI rendering proof | Clean | No UI contribution |
| Rollback story | Clean | Drain/reset/repromote proof defined |
| Simpler alternative | Clean | Plain Playwright launch already rejected |
| User-intent drift | Finding D10 | Fake fulfillment exceeded requested proof |
| Existence/runtime validity | Finding D12 | One-build publish path unspecified |

**Options incorporated:** pinned diagnostic host; card-generation-only proof with
abort cleanup; single-build local candidate publication.

**Verdict reasoning:** Four Important findings require a second revision and
third review cycle. D13 is accepted because only a trusted runtime operator can
set the profile env/mount and the dedicated unprivileged image has no user login
profile to protect.

## Cycle 3

**Status:** FAIL

**D1-D13:** Resolved or explicitly accepted at design level.

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| D14 | Important | security/privacy | Diagnostic did not firewall WS/WSS/WebTransport egress | Accepted: exact trusted origin, ephemeral no-secret profile, staging-only; all-protocol network policy belongs runtime |
| D15 | Important | failure modes | CI `finally` cannot cancel card after runner loss | Mark proof card/deadline in fulfillment evidence; server scheduler reaps overdue cards |
| D16 | Important | Stripe safety | Proof did not require test mode or `livemode=false` | Gate test key prefixes and assert false on PaymentIntent/card objects |

**Bug-class scan transcript:**

| class | result | note |
|---|---|---|
| Project-guidance conflicts | Clean | Ownership remains aligned |
| Assumptions under attack | Finding D15/D16 | Cleanup and Stripe mode assumptions removed |
| Repo-precedent conflicts | Clean | Provider/BMW responsibilities preserved |
| Artifact-class precedent | Clean | BMW workflow remains proof owner |
| YAGNI violations | Clean | No spoofing or browser fork |
| Missing failure modes | Finding D15 | Runner-loss cleanup absent |
| Security/privacy | Finding D14/D16 | Protocol egress accepted; live-mode guard added |
| Infrastructure impact | Finding D16 | Staging secret mode needed fail-closed validation |
| Multi-component validation | Finding D16 | Stripe mode not proven |
| Declared integration proof | Finding D16 | Stripe rows lacked mode invariant |
| Contributed UI rendering proof | Clean | No UI contribution |
| Rollback story | Clean | Existing rollback complete |
| Simpler alternative | Clean | Prior alternatives sufficient |
| User-intent drift | Clean | Full requested outcome covered |
| Existence/runtime validity | Clean | Existing/new artifacts accurately identified |

**Options:** runtime egress namespace; server-side proof-card reaper; direct
Stripe `livemode` checks. Reaper and mode checks are incorporated. D14 is
accepted because expanding provider scope into worker network policy violates
the repository ownership boundary and the endpoint itself is trusted.

**Verdict reasoning:** D15-D16 require revision. D14 is an explicit residual
risk with bounded staging-only impact and a named runtime owner.

## Cycle 4

**Status:** FAIL

**D1-D16:** Resolved or explicitly accepted at design level.

| id | sev | class | finding | resolution |
|---|---|---|---|---|
| D17 | Important | Stripe failure | Card could orphan between Stripe create and DB persistence | Reserve cleanup first; deterministic idempotency + Stripe metadata; metadata reconciler cancels orphans |
| D18 | Important | provenance | BMW proof was not bound to promoted runtime digest | Update/deploy BMW staging env; propagate submitted runtime ref; persist and assert exact candidate |
| D19 | Important | comparison validity | Native baseline lacked schema/tolerances/rejection rules | Define no-Playwright self-reporting baseline, stable fields, tolerances, informational fields |
| D20 | Important | CDP ownership | Stale endpoint could attach to surviving foreign Chrome | Require fresh file, live child, procfs listener-to-process-tree ownership; test stale/survivor cases |
| D21 | Important | Stripe evidence | `livemode` source/redaction unspecified | Server-side SDK booleans; protected IDs/booleans only; prohibit secret/raw-body artifacts |
| D22 | Minor | artifact hygiene | Status named only cycle 1 | Update status through cycle 4 |

**Bug-class scan transcript:**

| class | result | note |
|---|---|---|
| Project-guidance conflicts | Clean | Ownership remains aligned |
| Assumptions under attack | Finding D19/D20 | Baseline and endpoint ownership underspecified |
| Repo-precedent conflicts | Clean | Existing provider boundary retained |
| Artifact-class precedent | Clean | BMW workflow remains proof owner |
| YAGNI violations | Clean | No spoofing/fork added |
| Missing failure modes | Finding D17/D20 | Orphan card and stale endpoint gaps |
| Security/privacy | Finding D17/D20/D21 | Card, endpoint, evidence constraints needed |
| Infrastructure impact | Finding D18 | Staging digest deploy transition absent |
| Multi-component validation | Finding D18/D21 | Runtime and Stripe evidence unbound |
| Declared integration proof | Finding D18 | Candidate provenance absent |
| Contributed UI rendering proof | Clean | No UI contribution |
| Rollback story | Clean | Existing rollback complete |
| Simpler alternative | Clean | Prior alternatives sufficient |
| User-intent drift | Finding D19 | Native comparison was ambiguous |
| Existence/runtime validity | Finding D18/D20 | Deploy binding and endpoint freshness absent |

**Options incorporated:** runtime provenance output, Stripe metadata reconciliation,
and a dedicated no-Playwright native baseline contract.

**Verdict reasoning:** D17-D21 require design revision; D22 is corrected with
the same revision.

## Cycle 5

**Revision:** `4e86a7c`
**Status:** PASS

**D1-D22:** Resolved or explicitly accepted. D13 remains a trusted-operator
profile boundary. D14 remains a bounded staging-only runtime-network residual
with its follow-up recorded in workspace `docs/FOLLOWUPS.md`.

**Findings:** No Critical, Important, or Minor findings.

**Bug-class scan transcript:**

| class | result | note |
|---|---|---|
| Project-guidance conflicts | Clean | Provider/compute/BMW ownership preserved |
| Assumptions under attack | Clean | A1-A9 have failure conditions and fallbacks |
| Repo-precedent conflicts | Clean | Existing Go/Node/application boundaries retained |
| Artifact-class precedent | Clean | Existing BMW workflow/test owns commerce proof |
| YAGNI violations | Clean | No fork, spoofing, credentialed profile, or provider network namespace |
| Missing failure modes | Clean | Process, endpoint, profile, runner, Stripe, and Amazon failures covered |
| Security/privacy | Clean | Loopback/process-owned CDP, constrained diagnostics, redacted Stripe proof |
| Infrastructure impact | Clean | Candidate push, staging deploy, provenance, rollback defined |
| Multi-component validation | Clean | Real image/compute/BMW/Stripe/Issuing boundaries exercised |
| Declared integration proof | Clean | Matrix and exact candidate provenance complete |
| Contributed UI rendering proof | Clean | No UI contribution |
| Rollback story | Clean | Drain/reset/re-promote/re-diagnose explicit |
| Simpler alternative | Clean | Plain Playwright launch and fork rejected |
| User-intent drift | Clean | Natural browser and full BMW/Stripe outcome covered |
| Existence/runtime validity | Clean | Existing consumers named; new fields/routes have hosts |

**Option deferred:** component-backed BMW staging can replace the compatibility
image-ref variable after BMW adopts provider component campaigns.

**Verdict reasoning:** Cycle 5 closes D17-D21 and finds no new tangible issue;
the design passes.
