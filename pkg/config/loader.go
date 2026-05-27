package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadOptions provides additional loading parameters.
type LoadOptions struct {
	// Path is the config file to load (required).
	Path string

	// Strict enables DisallowUnknownFields for YAML/JSON parsing.
	// When true, unrecognized keys cause an error rather than being silently ignored.
	Strict bool
}

// Load reads configuration from a single source file.
// The format is detected from the file extension:
//   - .yaml / .yml → YAML
//   - .json → JSON
//
// Other extensions return an error. Legacy shell vars files are handled
// separately via the CAPRF client's ParseVars path.
// The loaded config is validated before returning.
func Load(path string) (*Config, error) {
	return LoadWithOptions(LoadOptions{Path: path})
}

// LoadWithOptions loads config with additional options.
func LoadWithOptions(opts LoadOptions) (*Config, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("config path is required")
	}

	f, err := os.Open(opts.Path) //nolint:gosec // trusted path from deployment
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file, close error is harmless

	ext := strings.ToLower(filepath.Ext(opts.Path))
	var cfg *Config

	switch ext {
	case ".yaml", ".yml":
		cfg, err = loadYAML(f, opts.Strict)
	case ".json":
		cfg, err = loadJSON(f, opts.Strict)
	default:
		return nil, fmt.Errorf("unsupported config format %q (use .yaml, .yml, or .json)", ext)
	}
	if err != nil {
		return nil, fmt.Errorf("load config from %s: %w", opts.Path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("load config from %s: %w", opts.Path, err)
	}
	return cfg, nil
}

func loadYAML(r io.Reader, strict bool) (*Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(r)
	if strict {
		decoder.KnownFields(true)
	}
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}
	// Reject multi-document YAML files to match JSON behavior and avoid
	// silently ignoring extra configuration in trailing documents.
	var extra any
	if decErr := decoder.Decode(&extra); !errors.Is(decErr, io.EOF) {
		if decErr != nil {
			return nil, fmt.Errorf("parsing YAML: unexpected content after first document: %w", decErr)
		}
		return nil, fmt.Errorf("parsing YAML: unexpected trailing document")
	}
	return &cfg, nil
}

func loadJSON(r io.Reader, strict bool) (*Config, error) {
	var cfg Config
	decoder := json.NewDecoder(r)
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	// Any successful Token() read (err == nil) means there is trailing content.
	// Only io.EOF (no more tokens) is the expected termination.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing JSON: unexpected trailing content")
	}
	return &cfg, nil
}
