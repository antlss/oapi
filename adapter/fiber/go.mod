module github.com/antlss/oapi/adapter/fiber

go 1.25.5

require (
	github.com/antlss/oapi v0.0.0
	github.com/gofiber/fiber/v2 v2.52.13
)

require (
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/getkin/kin-openapi v0.139.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-playground/form/v4 v4.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/oasdiff/yaml v0.1.0 // indirect
	github.com/oasdiff/yaml3 v0.0.13 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.51.0 // indirect
	github.com/valyala/tcplisten v1.0.0 // indirect
	github.com/woodsbury/decimal128 v1.3.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// Dev-only local link to the core module (unpublished). Remove and bump the
// require above to a tagged version when releasing this module.
replace github.com/antlss/oapi => ../..
