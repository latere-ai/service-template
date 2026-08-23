package main

import "encoding/json"

// marshalEvidence renders an evidence block the way the pipeline assembles it
// from its job outputs.
func marshalEvidence(e Evidence) (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
