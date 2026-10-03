package schemas

import _ "embed"

//go:embed browser-diagnostic-result.schema.json
var browserDiagnosticResult string

//go:embed product-capture-operation-output.schema.json
var productCaptureOperationOutput string

//go:embed product-capture-operation-input.schema.json
var productCaptureOperationInput string

func BrowserDiagnosticResult() []byte {
	return []byte(browserDiagnosticResult)
}

func ProductCaptureOperationOutput() []byte {
	return []byte(productCaptureOperationOutput)
}

func ProductCaptureOperationInput() []byte {
	return []byte(productCaptureOperationInput)
}
