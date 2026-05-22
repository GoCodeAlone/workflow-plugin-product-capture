# workflow-plugin-product-capture

This repo owns the Workflow product-capture plugin and workflow-compute provider
package for user-submitted product URLs.

- Keep generic provider/runtime logic here, not in the application using this plugin.
- Keep workflow-compute scheduling, leases, capabilities, and proof semantics in
  `GoCodeAlone/workflow-compute`.
- Do not add credentialed shopping sessions.
- Use Go for scripts and validation where practical.
- Run `go test ./...` before pushing.
