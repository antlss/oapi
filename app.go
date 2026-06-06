package oapi

// App is an immutable bundle of the configuration the library otherwise reads
// from process-wide globals — the [Validator], the success [ResponseEnvelope] and
// the request body cap. Build one with [New], attach it to routes with [WithApp],
// and every request those routes serve reads its configuration from the App rather
// than the globals.
//
// An App makes configuration explicit, thread-safe and composable: two Apps with
// different validators or envelopes can serve in the same process without racing
// on shared globals, and a test can run with its own App without disturbing the
// rest of the program. The process-wide [SetValidator]/[SetResponseEnvelope] API
// keeps working unchanged for routes built without an App (they read the globals
// at request time, exactly as before).
//
// An App is safe for concurrent use: its configuration is snapshotted at [New]
// time and never mutated afterwards.
//
// Scope note: the [ErrorParser] and the [RuleTag] remain process-wide (the doc
// generator reads them from package globals); see [WithRuleTag] and
// [SetErrorParser]. App-scoping those is a planned follow-up.
type App struct {
	cfg *appConfig
}

// appConfig is the immutable configuration an App (or, when nil, the process-wide
// globals) supplies to the request path. A nil *appConfig means "read the
// globals", which is how routes built without an App preserve the original
// behaviour — see the *OrGlobal accessors.
type appConfig struct {
	validator    Validator
	validatorSet bool
	envelope     ResponseEnvelope
	maxBodyBytes int64
	hasMaxBody   bool
}

// Option configures an [App] at construction time.
type Option func(*appConfig)

// New builds an immutable [App]. It snapshots the current process-wide defaults
// (whatever [SetValidator]/[SetResponseEnvelope] last installed) as the baseline,
// then applies opts, so an App is fully self-contained: later global Set* calls do
// not change an App that already exists. Call it once at startup, before serving.
func New(opts ...Option) *App {
	cfg := &appConfig{
		validator:    validatorImpl,
		validatorSet: validatorConfigured,
		envelope:     responseEnvelope,
		maxBodyBytes: 0,
		hasMaxBody:   false,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return &App{cfg: cfg}
}

// WithValidator sets the [Validator] this App uses to check every bound request
// part. Passing nil disables validation for the App explicitly (and silences the
// "no validator configured" warning for its routes).
func WithValidator(v Validator) Option {
	return func(c *appConfig) {
		c.validator = v
		c.validatorSet = true
	}
}

// WithResponseEnvelope sets the default success [ResponseEnvelope] for this App's
// routes (a route may still override it with [WithEnvelope]/[WithRawResponse]).
// Passing nil restores the standard [DataEnvelope].
func WithResponseEnvelope(e ResponseEnvelope) Option {
	return func(c *appConfig) {
		if e == nil {
			e = DataEnvelope
		}
		c.envelope = e
	}
}

// WithRuleTag sets the struct tag the OpenAPI generator and the validator read for
// validation rules (see [RuleTag]). NOTE: the rule tag is process-wide — it is read
// by the doc generator from a package global — so this option sets that global and
// affects the whole process, not just this App. It exists on the App for a single,
// declarative configuration site; call it once at startup.
func WithRuleTag(tag string) Option {
	return func(*appConfig) {
		if tag != "" {
			RuleTag = tag
		}
	}
}

// WithMaxRequestBytes sets the request body cap (in bytes) an adapter applies for
// this App's routes; 0 disables the cap. Adapters read it via
// [Route.MaxRequestBytes]; until an adapter is App-aware it falls back to its own
// DefaultMaxRequestBytes.
func WithMaxRequestBytes(n int64) Option {
	return func(c *appConfig) {
		c.maxBodyBytes = n
		c.hasMaxBody = true
	}
}

// WithApp attaches an [App]'s configuration to a route. It is the bridge between
// the App and the generic route constructors (Go does not allow generic methods,
// so the App cannot expose NewRoute itself): pass it as a [RouteOption].
//
//	app := oapi.New(oapi.WithValidator(v), oapi.WithResponseEnvelope(env))
//	r := oapi.NewRoute(method, path, handler, oapi.WithApp(app), oapi.WithSummary("..."))
//
// It sets the route's success envelope from the App only when the route has not
// already chosen one with [WithEnvelope]/[WithRawResponse], so an explicit
// per-route envelope always wins regardless of option order.
func WithApp(a *App) RouteOption {
	return func(route *Route) {
		if a == nil {
			return
		}
		route.cfg = a.cfg
		if route.envelope == nil {
			route.envelope = a.cfg.envelope
		}
	}
}

// validatorOrGlobal returns the App's validator (and whether one was configured),
// or the process-wide globals when the config is nil (a route built without an App).
func (c *appConfig) validatorOrGlobal() (Validator, bool) {
	if c == nil {
		return validatorImpl, validatorConfigured
	}
	return c.validator, c.validatorSet
}
