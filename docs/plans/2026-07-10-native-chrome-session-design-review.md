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
