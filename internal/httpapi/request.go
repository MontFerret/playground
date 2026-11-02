package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/MontFerret/playground/internal/playground"
)

var errInvalidRequest = errors.New("invalid request")

type postRequest struct {
	source       playground.Source
	optimization playground.Optimization
	include      []playground.InspectionArtifact
	params       map[string]json.RawMessage
	timeoutMs    int64
}

func decodeRequest(data []byte, path string) (postRequest, error) {
	req := postRequest{source: playground.Source{Name: "anonymous"}, optimization: playground.DefaultOptimization}
	fields := []string{"source"}
	optionFields := []string{"optimization"}
	switch path {
	case "/api/v1/compile":
		fields = append(fields, "options", "include")
	case "/api/v1/execute":
		fields = append(fields, "options", "params")
		optionFields = append(optionFields, "timeoutMs")
	}

	request, err := decodeObject(data, fields...)
	if err != nil {
		return req, err
	}

	source, err := decodeObject(request["source"], "name", "text")
	if err != nil {
		return req, err
	}

	if err := decodeNonNull(source["text"], &req.source.Text); err != nil {
		return req, err
	}

	if name, ok := source["name"]; ok {
		if err := decodeNonNull(name, &req.source.Name); err != nil {
			return req, err
		}
	}

	if raw, ok := request["options"]; ok {
		values, err := decodeObject(raw, optionFields...)
		if err != nil {
			return req, err
		}

		if value, ok := values["optimization"]; ok {
			if err := decodeNonNull(value, &req.optimization); err != nil {
				return req, err
			}
		}

		if value, ok := values["timeoutMs"]; ok {
			if err := decodeNonNull(value, &req.timeoutMs); err != nil || req.timeoutMs <= 0 {
				return req, errInvalidRequest
			}
		}
	}

	if raw, ok := request["params"]; ok {
		req.params, err = decodeObject(raw)
		if err != nil {
			return req, err
		}

		for _, value := range req.params {
			if value[0] != '{' && value[0] != '[' {
				continue
			}

			decoder := json.NewDecoder(bytes.NewReader(value))
			decoder.UseNumber()
			if err := checkParameterKeys(decoder); err != nil {
				return req, err
			}
		}
	}

	if raw, ok := request["include"]; ok {
		if err := decodeNonNull(raw, &req.include); err != nil {
			return req, err
		}
	}

	if !playground.ValidOptimization(req.optimization) || !playground.ValidArtifacts(req.include) {
		return req, errInvalidRequest
	}

	return req, nil
}

// JSON syntax stays owned by encoding/json. Explicit object keys enforce the
// schema's case-sensitive names, reject duplicates, and distinguish null from
// omission; Go's default struct decoder does not enforce these distinctions.
func decodeObject(data []byte, fields ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errInvalidRequest
	}

	result := make(map[string]json.RawMessage, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errInvalidRequest
		}

		key, ok := token.(string)
		if !ok || (fields != nil && !slices.Contains(fields, key)) {
			return nil, errInvalidRequest
		}

		if _, exists := result[key]; exists {
			return nil, errInvalidRequest
		}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errInvalidRequest
		}

		result[key] = value
	}

	if _, err := decoder.Token(); err != nil {
		return nil, errInvalidRequest
	}

	if _, err := decoder.Token(); err != io.EOF {
		return nil, errInvalidRequest
	}

	return result, nil
}

func decodeNonNull(data []byte, target any) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errInvalidRequest
	}

	return json.Unmarshal(data, target)
}

// Parameter objects have arbitrary keys, but follow the same uniqueness rule
// at every depth. Keep original value bytes for Ferret's numeric conversion.
func checkParameterKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, container := token.(json.Delim)
	if !container {
		return nil
	}

	var keys map[string]struct{}
	if delim == '{' {
		keys = make(map[string]struct{})
	}

	for decoder.More() {
		if keys != nil {
			key, err := decoder.Token()
			if err != nil {
				return err
			}

			name := key.(string)
			if _, exists := keys[name]; exists {
				return errInvalidRequest
			}

			keys[name] = struct{}{}
		}

		if err := checkParameterKeys(decoder); err != nil {
			return err
		}
	}

	_, err = decoder.Token()

	return err
}
