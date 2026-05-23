package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

func decodeStrictMap(raw map[string]any, out any) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("multiple config values are not allowed")
	}
	return nil
}
