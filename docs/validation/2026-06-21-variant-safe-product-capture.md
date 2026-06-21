# Variant-Safe Product Capture Validation

Date: 2026-06-21

Scope: Task 3 of the BMW product-capture round trip plan.

Fixture URL:

- `https://www.amazon.com/Microsoft-Xbox-Gaming-Console-video-game/dp/B08H75RTZ8`

Behavior covered:

- `product_json` includes `requested_url`, selected ASIN in `external_id`,
  `canonical_url`, `variant`, `variant_dimensions`, `variant_key`,
  `requires_user_confirmation`, `image_url`, and bounded `images`.
- Selected Amazon variant dimensions produce a deterministic
  `asin-variant-sha256:` key independent of DOM order.
- Missing variant dimensions produce an `exact-url-sha256:` key and require user
  confirmation; consumers must not promote that key across submitted URLs.
- Requested/canonical ASIN mismatch continues to fail closed.
- Plugin preview flattening exposes variant fields and still does not promote
  diagnostic `error` preview fields into successful output.

Verification commands:

```sh
GOWORK=off go test ./internal/snapshot -run 'Test.*Variant|Test.*Amazon|Test.*Image' -count=1
GOWORK=off go test ./internal/provider -run 'Test.*Variant|Test.*ProductJSON|Test.*Amazon' -count=1
GOWORK=off go test ./internal/plugin -run 'Test.*ProductCapture|Test.*Variant' -count=1
GOWORK=off go test ./... -count=1
git diff --check
```
