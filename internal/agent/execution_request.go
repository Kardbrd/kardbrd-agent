package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type optionalSelection struct {
	Present bool
	Value   string
}

type executionRequest struct {
	Version            int
	TargetBotID        string
	CapabilityRevision string
	Model              optionalSelection
	Effort             optionalSelection
}

type acceptedDefaults struct {
	Model  *string
	Effort *string
}

func parseExecutionRequest(raw json.RawMessage) (executionRequest, error) {
	var request executionRequest
	fields, err := strictJSONObject(raw, "execution_request")
	if err != nil {
		return request, err
	}
	for name := range fields {
		switch name {
		case "version", "target_bot_id", "capability_revision", "model", "effort":
		default:
			return request, fmt.Errorf("execution_request.%s: unknown field", name)
		}
	}
	if err := json.Unmarshal(fields["version"], &request.Version); err != nil || request.Version != 1 {
		return request, fmt.Errorf("execution_request.version: expected 1")
	}
	request.TargetBotID, err = requiredJSONString(fields, "target_bot_id", "execution_request")
	if err != nil {
		return request, err
	}
	request.CapabilityRevision, err = requiredJSONString(fields, "capability_revision", "execution_request")
	if err != nil {
		return request, err
	}
	if raw, present := fields["model"]; present {
		request.Model.Present = true
		request.Model.Value, err = decodeNonemptyJSONString(raw, "execution_request.model")
		if err != nil {
			return request, err
		}
	}
	if raw, present := fields["effort"]; present {
		request.Effort.Present = true
		request.Effort.Value, err = decodeNonemptyJSONString(raw, "execution_request.effort")
		if err != nil {
			return request, err
		}
	}
	return request, nil
}

func parseAcceptedDefaults(raw json.RawMessage) (acceptedDefaults, error) {
	var defaults acceptedDefaults
	fields, err := strictJSONObject(raw, "accepted_defaults")
	if err != nil {
		return defaults, err
	}
	for name := range fields {
		if name != "model" && name != "effort" {
			return defaults, fmt.Errorf("accepted_defaults.%s: unknown field", name)
		}
	}
	for _, name := range []string{"model", "effort"} {
		value, present := fields[name]
		if !present {
			return defaults, fmt.Errorf("accepted_defaults.%s: required", name)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		decoded, err := decodeNonemptyJSONString(value, "accepted_defaults."+name)
		if err != nil {
			return defaults, err
		}
		if name == "model" {
			defaults.Model = &decoded
		} else {
			defaults.Effort = &decoded
		}
	}
	return defaults, nil
}

func strictJSONObject(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%s: expected object", path)
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("%s: invalid key", path)
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("%s.%s: duplicate field", path, key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", path, key, err)
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s: trailing data", path)
	}
	return fields, nil
}

func requiredJSONString(fields map[string]json.RawMessage, key, path string) (string, error) {
	raw, present := fields[key]
	if !present {
		return "", fmt.Errorf("%s.%s: required", path, key)
	}
	return decodeNonemptyJSONString(raw, path+"."+key)
}

func decodeNonemptyJSONString(raw json.RawMessage, path string) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("%s: expected nonempty string", path)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%s: expected nonempty string", path)
	}
	return value, nil
}
