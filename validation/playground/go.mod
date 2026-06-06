module github.com/antlss/oapi/validation/playground

go 1.25.5

require (
	github.com/antlss/oapi v0.0.0
	github.com/go-playground/validator/v10 v10.30.3
)

require (
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/getkin/kin-openapi v0.139.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-playground/form/v4 v4.3.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/oasdiff/yaml v0.1.0 // indirect
	github.com/oasdiff/yaml3 v0.0.13 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/woodsbury/decimal128 v1.3.0 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// Dev-only local link to the core module (unpublished). Remove and bump the
// require above to a tagged version when releasing this module.
replace github.com/antlss/oapi => ../..
