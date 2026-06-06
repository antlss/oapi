package oapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// GenConfig configures Registry.Write. The zero value is valid: an empty Dir,
// JSONFile or YAMLFile falls back to its default, so GenConfig{} writes
// openapi.json and openapi.yaml into the current directory after validating.
type GenConfig struct {
	Dir        string // output directory; default "."
	JSONFile   string // JSON filename; default "openapi.json"; "-" disables JSON
	YAMLFile   string // YAML filename; default "openapi.yaml"; "-" disables YAML
	NoValidate bool   // skip the pre-write OpenAPI 3 validation (validation is on by default)
	BaseFile   string // optional base/common document (JSON or YAML) to overlay; see Base
}

const (
	defaultJSONFile = "openapi.json"
	defaultYAMLFile = "openapi.yaml"
	disableFile     = "-"
)

// Write renders the OpenAPI document and writes it to disk as JSON and/or YAML,
// creating the output directory as needed. Unless NoValidate is set it validates
// the document first and returns an error without touching the filesystem when
// the spec is invalid, so a successful call guarantees well-formed artifacts. It
// returns the paths actually written, in a stable order (JSON then YAML).
//
// When BaseFile is set and no base has been configured yet, it is loaded and
// applied via Base before generation.
func (rg *Registry) Write(ctx context.Context, cfg GenConfig) ([]string, error) {
	if cfg.BaseFile != "" && rg.base == nil {
		base, err := LoadBaseFile(cfg.BaseFile)
		if err != nil {
			return nil, fmt.Errorf("load base %q: %w", cfg.BaseFile, err)
		}
		rg.Base(base)
	}

	if !cfg.NoValidate {
		if err := rg.Validate(ctx); err != nil {
			return nil, fmt.Errorf("openapi document is invalid: %w", err)
		}
	}

	dir := cfg.Dir
	if dir == "" {
		dir = "."
	}

	plan := []struct {
		name   string
		render func() ([]byte, error)
	}{
		{name: orDefault(cfg.JSONFile, defaultJSONFile), render: rg.JSON},
		{name: orDefault(cfg.YAMLFile, defaultYAMLFile), render: rg.YAML},
	}

	// Render everything before writing anything, so a render error leaves the
	// output directory untouched (all-or-nothing).
	type artifact struct {
		path string
		data []byte
	}
	out := make([]artifact, 0, len(plan))
	for _, p := range plan {
		if p.name == disableFile {
			continue
		}
		data, err := p.render()
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", p.name, err)
		}
		out = append(out, artifact{path: filepath.Join(dir, p.name), data: data})
	}
	if len(out) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create out dir %q: %w", dir, err)
	}

	written := make([]string, 0, len(out))
	for _, a := range out {
		if err := os.WriteFile(a.path, a.data, 0o644); err != nil {
			return nil, fmt.Errorf("write %q: %w", a.path, err)
		}
		written = append(written, a.path)
	}
	return written, nil
}

// orDefault returns val unless it is empty, in which case it returns def.
func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
