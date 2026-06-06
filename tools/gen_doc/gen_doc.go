// Package gendoc provides a turnkey main for generating a Registry's OpenAPI
// document from the command line, so an application's generator command is a
// single line:
//
//	//go:generate go run ./cmd/openapi-gen -out ./openapi
//
//	package main
//
//	import (
//		"github.com/antlss/oapi/tools/gen_doc"
//		"example.com/app/api"
//	)
//
//	func main() { gendoc.Main(api.Registry()) }
//
// Flags: -out DIR, -format LIST (json,yaml), -base FILE, -no-validate.
//
// It lives under tools/ alongside any future code generators (e.g. an API mock
// generator), each a small package that wraps the core library.
package gendoc

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/antlss/oapi"
)

// Main parses generation flags, writes the registry's OpenAPI document to disk
// and exits non-zero on failure. It is the body of a generator command; call it
// from main with your application's Registry.
func Main(reg *oapi.Registry) {
	out := flag.String("out", ".", "directory to write the OpenAPI document into")
	format := flag.String("format", "json,yaml", "comma-separated formats to emit: json, yaml")
	base := flag.String("base", "", "optional base/common document (JSON or YAML) to overlay")
	noValidate := flag.Bool("no-validate", false, "skip OpenAPI 3 validation before writing")
	flag.Parse()

	cfg, err := configFor(*out, *format, *base, *noValidate)
	if err != nil {
		log.Fatalf("gen_doc: %v", err)
	}

	written, err := reg.Write(context.Background(), cfg)
	if err != nil {
		log.Fatalf("gen_doc: %v", err)
	}
	for _, path := range written {
		fmt.Printf("wrote %s\n", path)
	}
}

// configFor turns the flag values into a GenConfig, disabling whichever of JSON
// / YAML is not listed in the -format flag.
func configFor(out, format, base string, noValidate bool) (oapi.GenConfig, error) {
	cfg := oapi.GenConfig{Dir: out, BaseFile: base, NoValidate: noValidate, JSONFile: "-", YAMLFile: "-"}
	for f := range strings.SplitSeq(format, ",") {
		switch strings.TrimSpace(strings.ToLower(f)) {
		case "":
			// ignore empty entries (e.g. a trailing comma)
		case "json":
			cfg.JSONFile = "" // "" selects the default filename
		case "yaml", "yml":
			cfg.YAMLFile = ""
		default:
			return cfg, fmt.Errorf("unknown format %q (want json or yaml)", f)
		}
	}
	if cfg.JSONFile == "-" && cfg.YAMLFile == "-" {
		return cfg, fmt.Errorf("no output formats selected via -format")
	}
	return cfg, nil
}
