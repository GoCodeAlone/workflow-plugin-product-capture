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
