package plugin

import (
	"fmt"
	"strings"
)

func resolveRuntimeRef(ref string, metadata, runtimeConfig map[string]any) (string, error) {
	var namespace, key string
	switch {
	case strings.HasPrefix(ref, "secret:"):
		namespace, key = "secrets", strings.TrimPrefix(ref, "secret:")
	case strings.HasPrefix(ref, "config:"):
		namespace, key = "config", strings.TrimPrefix(ref, "config:")
	default:
		return "", fmt.Errorf("ref must use secret: or config:")
	}
	for _, source := range []map[string]any{runtimeConfig, metadata} {
		if source == nil {
			continue
		}
		if value, ok := nestedString(source, namespace, key); ok {
			return value, nil
		}
		if value, ok := source[key].(string); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s ref %q not resolved", namespace, key)
}

func nestedString(source map[string]any, namespace, key string) (string, bool) {
	raw, ok := source[namespace]
	if !ok {
		return "", false
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := values[key].(string)
	return value, ok
}

func isRef(value string) bool {
	return strings.HasPrefix(value, "secret:") || strings.HasPrefix(value, "config:")
}
