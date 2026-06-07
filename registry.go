package oapi

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// Registry collects Routes and turns them into an OpenAPI 3 document. Because it
// reads the RouteSchema reflection types captured at NewRoute time, the docs are
// generated from the exact same Go types the handler binds — they cannot drift.
//
// Registry is framework-agnostic: it produces bytes (JSON/YAML) and an
// *openapi3.T. Serve those with any adapter (e.g. adapter/gin's SpecHandler).
type Registry struct {
	routes      []Route
	title       string
	version     string
	description string
	servers     []*openapi3.Server
	schemes     map[string]*openapi3.SecurityScheme

	// Document-level metadata, all optional, rendered by Swagger UI / Redoc.
	termsOfService string
	contact        *openapi3.Contact
	license        *openapi3.License
	logo           map[string]any // Redoc x-logo extension, set on Info
	externalDocs   *openapi3.ExternalDocs
	tags           []*openapi3.Tag // top-level tag descriptions, in declared order
	tagGroups      []tagGroup      // Redoc x-tagGroups navigation sections
	base           *openapi3.T     // optional base/common document overlaid onto
	useComponents  bool            // emit named response types as $ref components
}

// tagGroup is one Redoc x-tagGroups entry: a named navigation section grouping a
// set of tag names.
type tagGroup struct {
	name string
	tags []string
}

// NewRegistry creates a documentation registry with the given API title/version.
func NewRegistry(title, version string) *Registry {
	return &Registry{title: title, version: version} //nolint:exhaustruct
}

// Add appends routes to the registry. Returns the registry for chaining.
func (rg *Registry) Add(routes ...Route) *Registry {
	rg.routes = append(rg.routes, routes...)
	return rg
}

// Describe sets the API description shown in the docs.
func (rg *Registry) Describe(description string) *Registry {
	rg.description = description
	return rg
}

// UseComponents makes the generator emit each named response/data/meta/error
// struct type once under components/schemas and reference it by $ref wherever it
// appears, instead of inlining a copy at every use. This produces smaller, more
// idiomatic specs for APIs that share types across endpoints. It is opt-in: the
// default inlines every schema (so output is unchanged unless you call this).
// Request bodies stay inline, since they carry per-field binding constraints a
// shared response component must not.
func (rg *Registry) UseComponents() *Registry {
	rg.useComponents = true
	return rg
}

// AddServer adds a server entry (base URL + description) to the document.
func (rg *Registry) AddServer(url, description string) *Registry {
	rg.servers = append(rg.servers, &openapi3.Server{URL: url, Description: description}) //nolint:exhaustruct
	return rg
}

// AddSecurityScheme registers a named security scheme (referenced by routes via
// WithSecurity). Use BearerAuth / APIKeyAuth for the common cases.
func (rg *Registry) AddSecurityScheme(name string, scheme *openapi3.SecurityScheme) *Registry {
	if rg.schemes == nil {
		rg.schemes = map[string]*openapi3.SecurityScheme{}
	}
	rg.schemes[name] = scheme
	return rg
}

// Contact sets the API contact (name/url/email) shown in the docs. Empty
// arguments are omitted from the spec.
func (rg *Registry) Contact(name, url, email string) *Registry {
	rg.contact = &openapi3.Contact{Name: name, URL: url, Email: email} //nolint:exhaustruct
	return rg
}

// License sets the API license (name + optional URL) shown in the docs.
func (rg *Registry) License(name, url string) *Registry {
	rg.license = &openapi3.License{Name: name, URL: url} //nolint:exhaustruct
	return rg
}

// TermsOfService sets the URL of the API's terms of service.
func (rg *Registry) TermsOfService(url string) *Registry {
	rg.termsOfService = url
	return rg
}

// Logo sets a logo image URL shown in Redoc's sidebar via the x-logo extension
// (Swagger UI ignores it). Use LogoWith to also set background colour, alt text
// or a click-through href.
func (rg *Registry) Logo(url string) *Registry {
	return rg.LogoWith(map[string]any{"url": url})
}

// LogoWith sets the full Redoc x-logo object (keys: url, backgroundColor,
// altText, href), replacing any value set by Logo.
func (rg *Registry) LogoWith(logo map[string]any) *Registry {
	rg.logo = logo
	return rg
}

// ExternalDocs adds a link to external documentation shown near the top of the docs.
func (rg *Registry) ExternalDocs(description, url string) *Registry {
	rg.externalDocs = &openapi3.ExternalDocs{Description: description, URL: url} //nolint:exhaustruct
	return rg
}

// AddTag declares a top-level tag with a description. Declaration order controls
// the order tags appear in Redoc's navigation; calling it again for an existing
// tag name updates that tag's description.
func (rg *Registry) AddTag(name, description string) *Registry {
	for _, t := range rg.tags {
		if t.Name == name {
			t.Description = description
			return rg
		}
	}
	rg.tags = append(rg.tags, &openapi3.Tag{Name: name, Description: description}) //nolint:exhaustruct
	return rg
}

// TagGroup defines a Redoc x-tagGroups navigation section grouping the named
// tags under a heading (Swagger UI ignores it).
func (rg *Registry) TagGroup(name string, tags ...string) *Registry {
	rg.tagGroups = append(rg.tagGroups, tagGroup{name: name, tags: tags})
	return rg
}

// Base sets a base OpenAPI document the generator overlays onto: the base
// supplies defaults (info, externalDocs, vendor extensions such as x-tagGroups,
// …) while the generated paths/components and any value set through the
// Registry's own setters take precedence. Use LoadBaseFile to read one from disk
// (e.g. openapi/common.json). Pass nil to clear.
func (rg *Registry) Base(doc *openapi3.T) *Registry {
	rg.base = doc
	return rg
}

// LoadBaseFile reads an OpenAPI document (JSON or YAML) from disk for use as a
// Registry base via Base, returning the parsed document or an error so a
// missing/invalid file is handled explicitly:
//
//	base, err := oapi.LoadBaseFile("openapi/common.json")
//	if err != nil { ... }
//	reg.Base(base)
func LoadBaseFile(path string) (*openapi3.T, error) {
	return openapi3.NewLoader().LoadFromFile(path)
}

// Routes returns the registered routes in registration order.
func (rg *Registry) Routes() []Route { return rg.routes }

// BearerAuth is an HTTP bearer-token security scheme (e.g. JWT).
func BearerAuth() *openapi3.SecurityScheme {
	return openapi3.NewSecurityScheme().WithType("http").WithScheme("bearer")
}

// APIKeyAuth is an API-key security scheme carried in the given location
// ("header", "query" or "cookie") under the given parameter name.
func APIKeyAuth(in, name string) *openapi3.SecurityScheme {
	return openapi3.NewSecurityScheme().WithType("apiKey").WithIn(in).WithName(name)
}

// OpenAPI builds the OpenAPI 3 document for every registered route.
//
// When a base document is configured (see Base/LoadBaseFile) the generator
// starts from a copy of it and overlays the rest. Precedence is deterministic:
// the base supplies defaults; values set through the Registry override them when
// non-empty; and the generated paths/components are always merged in last.
func (rg *Registry) OpenAPI() *openapi3.T {
	doc := rg.baseDocument()

	rg.applyInfo(doc)

	if len(rg.servers) > 0 {
		doc.Servers = append(doc.Servers, rg.servers...)
	}

	rg.applyTags(doc)

	if rg.externalDocs != nil {
		doc.ExternalDocs = rg.externalDocs
	}

	if len(rg.tagGroups) > 0 {
		groups := make([]map[string]any, 0, len(rg.tagGroups))
		for _, g := range rg.tagGroups {
			groups = append(groups, map[string]any{"name": g.name, "tags": g.tags})
		}
		doc.Extensions = setExtension(doc.Extensions, "x-tagGroups", groups)
	}

	rg.applySecuritySchemes(doc)

	// When components are enabled, named response/data/meta/error types are
	// collected into this set as $ref components; a nil set inlines everything (the
	// default), keeping the output byte-identical.
	var set *schemaSet
	if rg.useComponents {
		set = newSchemaSet()
	}

	for _, route := range rg.routes {
		rg.addRoute(doc, route, set)
	}

	rg.applyComponentSchemas(doc, set)

	return doc
}

// applyComponentSchemas merges the collected component schemas into the document,
// creating the components container as needed. Generated schemas are merged last,
// so they win over a same-named schema from a configured base document (mirroring
// how generated paths are merged). A nil/empty set is a no-op.
func (rg *Registry) applyComponentSchemas(doc *openapi3.T, set *schemaSet) {
	if set.empty() {
		return
	}
	if doc.Components == nil {
		doc.Components = &openapi3.Components{} //nolint:exhaustruct
	}
	if doc.Components.Schemas == nil {
		doc.Components.Schemas = openapi3.Schemas{}
	}
	for name, ref := range set.schemas {
		doc.Components.Schemas[name] = ref
	}
}

// baseDocument returns the document to build onto: a copy of the configured base
// (never the caller's instance) or a fresh 3.0.3 skeleton. Info and Paths are
// guaranteed non-nil so the overlay steps can assume them.
func (rg *Registry) baseDocument() *openapi3.T {
	var doc *openapi3.T
	if rg.base != nil {
		doc = cloneDoc(rg.base)
	}
	if doc == nil {
		doc = &openapi3.T{OpenAPI: "3.0.3"} //nolint:exhaustruct
	}
	if doc.OpenAPI == "" {
		doc.OpenAPI = "3.0.3"
	}
	if doc.Info == nil {
		doc.Info = &openapi3.Info{} //nolint:exhaustruct
	}
	if doc.Paths == nil {
		doc.Paths = openapi3.NewPaths()
	}
	return doc
}

// applyInfo overlays the registry's Info-level metadata, overriding base values
// only when the registry value is set (so a base document can supply defaults
// the code leaves unset).
func (rg *Registry) applyInfo(doc *openapi3.T) {
	info := doc.Info
	if rg.title != "" {
		info.Title = rg.title
	}
	if rg.version != "" {
		info.Version = rg.version
	}
	if rg.description != "" {
		info.Description = rg.description
	}
	if rg.termsOfService != "" {
		info.TermsOfService = rg.termsOfService
	}
	if rg.contact != nil {
		info.Contact = rg.contact
	}
	if rg.license != nil {
		info.License = rg.license
	}
	if rg.logo != nil {
		info.Extensions = setExtension(info.Extensions, "x-logo", rg.logo)
	}
}

// applyTags merges the registry's top-level tag descriptions into the document,
// preserving declared order and overriding a base tag of the same name.
func (rg *Registry) applyTags(doc *openapi3.T) {
	for _, t := range rg.tags {
		if existing := doc.Tags.Get(t.Name); existing != nil {
			existing.Description = t.Description
			continue
		}
		doc.Tags = append(doc.Tags, t)
	}
}

// applySecuritySchemes merges the registered security schemes into the document
// components, creating the containers as needed.
func (rg *Registry) applySecuritySchemes(doc *openapi3.T) {
	if len(rg.schemes) == 0 {
		return
	}
	if doc.Components == nil {
		doc.Components = &openapi3.Components{} //nolint:exhaustruct
	}
	if doc.Components.SecuritySchemes == nil {
		doc.Components.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	for name, scheme := range rg.schemes {
		doc.Components.SecuritySchemes[name] = &openapi3.SecuritySchemeRef{Value: scheme} //nolint:exhaustruct
	}
}

// setExtension lazily initialises an Extensions map and sets one vendor
// extension key (e.g. x-logo, x-tagGroups).
func setExtension(ext map[string]any, key string, value any) map[string]any {
	if ext == nil {
		ext = map[string]any{}
	}
	ext[key] = value
	return ext
}

// cloneDoc returns a deep copy of doc by round-tripping it through JSON, so the
// generator can overlay onto a base document without mutating the caller's value.
// Returns nil on any error so the caller falls back to building from scratch.
func cloneDoc(doc *openapi3.T) *openapi3.T {
	clone := &openapi3.T{} //nolint:exhaustruct
	if err := jsonRoundTrip(doc, clone); err != nil {
		return nil
	}
	return clone
}

// jsonRoundTrip deep-copies src into dst by marshalling src and unmarshalling the
// bytes into dst, yielding an independent copy with no shared sub-pointers. Both
// cloneDoc and deAliasSchema rely on it: the generator overlays onto a base
// document and enriches per-field schemas without mutating the instances
// openapi3/openapi3gen share. Each caller picks its own fallback on error.
func jsonRoundTrip(src json.Marshaler, dst json.Unmarshaler) error {
	raw, err := src.MarshalJSON()
	if err != nil {
		return err
	}
	return dst.UnmarshalJSON(raw)
}

// JSON renders the OpenAPI document as indented JSON.
func (rg *Registry) JSON() ([]byte, error) {
	raw, err := rg.OpenAPI().MarshalJSON()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// SpecBytesOnce returns a closure that renders the document to indented JSON
// exactly once and caches the result (bytes or error) for every later call. It
// is the shared engine behind each adapter's SpecHandler: the adapter keeps only
// the few framework-specific lines that write the bytes, while the lazy
// render-once behaviour lives here next to JSON. Each call to SpecBytesOnce gets
// its own cache, so one per SpecHandler registration.
func (rg *Registry) SpecBytesOnce() func() ([]byte, error) {
	var (
		once sync.Once
		raw  []byte
		err  error
	)
	return func() ([]byte, error) {
		once.Do(func() { raw, err = rg.JSON() })
		return raw, err
	}
}

// YAML renders the OpenAPI document as block-style YAML. It round-trips the JSON
// encoding through a yaml.Node so key order is preserved and every openapi3
// custom JSON marshaller is honoured, without a YAML-specific dependency.
func (rg *Registry) YAML() ([]byte, error) {
	raw, err := rg.OpenAPI().MarshalJSON()
	if err != nil {
		return nil, err
	}

	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	clearYAMLStyle(&node)

	return yaml.Marshal(&node)
}

// clearYAMLStyle recursively resets node styles so a tree decoded from JSON
// re-encodes as block-style YAML instead of inline flow (JSON) style.
func clearYAMLStyle(node *yaml.Node) {
	node.Style = 0
	for _, child := range node.Content {
		clearYAMLStyle(child)
	}
}

// Validate builds the document and checks it against the OpenAPI 3 schema. A nil
// return means the spec is well-formed and loadable by any standard tool.
func (rg *Registry) Validate(ctx context.Context) error {
	return rg.OpenAPI().Validate(ctx)
}

func (rg *Registry) addRoute(doc *openapi3.T, route Route, set *schemaSet) {
	rd := route.doc
	op := &openapi3.Operation{ //nolint:exhaustruct
		Summary:     rd.summary,
		Description: rd.description,
		Tags:        rd.tags,
		Deprecated:  rd.deprecated,
		OperationID: operationID(route),
	}

	// Parameters: each request part maps to a different OpenAPI location.
	for _, p := range paramsFromStruct(rd.schema.Header, openapi3.ParameterInHeader, tagHeader) {
		op.AddParameter(p)
	}
	for _, p := range paramsFromStruct(rd.schema.Param, openapi3.ParameterInPath, tagURI) {
		op.AddParameter(p)
	}
	for _, p := range paramsFromStruct(rd.schema.Query, openapi3.ParameterInQuery, tagForm) {
		op.AddParameter(p)
	}

	// Request body (JSON / multipart / urlencoded).
	if rb := requestBody(rd.schema.Body, set); rb != nil {
		op.RequestBody = &openapi3.RequestBodyRef{Value: rb} //nolint:exhaustruct
	}

	op.Responses = responsesFor(route, set)

	// Security requirements (AND of the declared schemes).
	if len(rd.security) > 0 {
		req := openapi3.NewSecurityRequirement()
		for _, s := range rd.security {
			req[s.scheme] = append([]string{}, s.scopes...)
		}
		op.Security = openapi3.NewSecurityRequirements().With(req)
	}

	pathItem := doc.Paths.Value(toOpenAPIPath(route.path))
	if pathItem == nil {
		pathItem = &openapi3.PathItem{} //nolint:exhaustruct
		doc.Paths.Set(toOpenAPIPath(route.path), pathItem)
	}
	pathItem.SetOperation(route.method, op)
}
