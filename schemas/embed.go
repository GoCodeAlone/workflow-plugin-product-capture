package schemas

import _ "embed"

//go:embed browser-diagnostic-result.schema.json
var browserDiagnosticResult string

func BrowserDiagnosticResult() []byte {
	return []byte(browserDiagnosticResult)
}
