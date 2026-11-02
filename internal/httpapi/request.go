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

func decodeToolingRequest(data []byte, compile bool) (playground.Source, playground.CompileOptions, error) {
	src := playground.Source{Name: "anonymous"}
	options := playground.CompileOptions{Optimization: playground.DefaultOptimization}
	fields := []string{"source"}
	if compile {
		fields = append(fields, "options", "include")
	}

	request, err := decodeObject(data, fields...)
	if err != nil {
		return src, options, err
	}

	source, err := decodeObject(request["source"], "name", "text")
	if err != nil {
		return src, options, err
	}

	if err := decodeNonNull(source["text"], &src.Text); err != nil {
		return src, options, err
	}

	if name, ok := source["name"]; ok {
		if err := decodeNonNull(name, &src.Name); err != nil {
			return src, options, err
		}
	}

	if raw, ok := request["options"]; ok {
		values, err := decodeObject(raw, "optimization")
		if err != nil {
			return src, options, err
		}

		if value, ok := values["optimization"]; ok {
			if err := decodeNonNull(value, &options.Optimization); err != nil {
				return src, options, err
			}
		}
	}

	if raw, ok := request["include"]; ok {
		if err := decodeNonNull(raw, &options.Include); err != nil {
			return src, options, err
		}
	}

	if !playground.ValidOptimization(options.Optimization) || !playground.ValidArtifacts(options.Include) {
		return src, options, errInvalidRequest
	}

	return src, options, nil
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
		if !ok || !slices.Contains(fields, key) {
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
