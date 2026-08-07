package diag

// Check describes a registered diagnostic check: its stable identifier,
// default severity, and a one-line description. The Registry is the
// authority for the diagnostic catalogue (FR-045): every emitted ID must be
// registered here, and the documentation coverage test enumerates it.
type Check struct {
	ID              string
	DefaultSeverity Severity
	Description     string
}

// Stable diagnostic identifiers.
//
// Ranges:
//
//	GHTMX-E01xx  route binding errors
//	GHTMX-E02xx  attribute errors
//	GHTMX-E03xx  declaration errors
//	GHTMX-E04xx  route discovery errors
//	GHTMX-E05xx  version errors
//	GHTMX-E06xx  templ carve-out errors (FR-004)
//	GHTMX-W01xx  reachability warnings
//	GHTMX-W02xx  target warnings
//	GHTMX-W03xx  build hygiene warnings
const (
	// Route binding errors.
	UnknownHandler       = "GHTMX-E0101"
	VerbMismatch         = "GHTMX-E0102"
	ParameterisedBinding = "GHTMX-E0103"
	ConstructorArity     = "GHTMX-E0104"

	// Attribute errors.
	UnknownAttribute      = "GHTMX-E0201"
	InvalidAttributeValue = "GHTMX-E0202"
	AttributeConflict     = "GHTMX-E0203"

	// Declaration errors.
	DuplicateFragment      = "GHTMX-E0301"
	FragmentChildren       = "GHTMX-E0302"
	UnresolvableFragment   = "GHTMX-E0303"
	UndeclaredEvent        = "GHTMX-E0304"
	DuplicateEvent         = "GHTMX-E0305"
	CircularReference      = "GHTMX-E0306"
	DuplicateComponentName = "GHTMX-E0307"
	ReservedImport         = "GHTMX-E0308"

	// Route discovery errors.
	DuplicateRoute       = "GHTMX-E0401"
	UnresolvableRoute    = "GHTMX-E0402"
	MalformedAnnotation  = "GHTMX-E0403"
	ConstructorCollision = "GHTMX-E0404"

	// Version errors.
	VersionMismatch    = "GHTMX-E0501"
	UnsupportedVersion = "GHTMX-E0502"

	// templ carve-out errors (FR-004).
	CarveOutTypedBinding   = "GHTMX-E0601"
	CarveOutStringURL      = "GHTMX-E0602"
	CarveOutAuthorEscaping = "GHTMX-E0603"

	// Reachability warnings.
	UnusedFragment    = "GHTMX-W0101"
	UnemittedEvent    = "GHTMX-W0102"
	FragmentScopeHint = "GHTMX-W0103"
	UnboundRoute      = "GHTMX-W0104"
	MultiPathHandler  = "GHTMX-W0105"

	// Target warnings.
	DanglingTarget = "GHTMX-W0201"

	// Build hygiene warnings.
	StaleOutput = "GHTMX-W0301"
)

// Registry maps every stable diagnostic ID to its check definition.
var Registry = map[string]Check{
	UnknownHandler:       {UnknownHandler, Error, "hx-* binding references a symbol that is not a registered handler"},
	VerbMismatch:         {VerbMismatch, Error, "hx-* binding verb does not match the handler's registered verb"},
	ParameterisedBinding: {ParameterisedBinding, Error, "direct symbol binding to a route with path parameters; use the generated route constructor"},
	ConstructorArity:     {ConstructorArity, Error, "route constructor called with the wrong number of arguments"},

	UnknownAttribute:      {UnknownAttribute, Error, "unknown hx-* attribute for the configured htmx version"},
	InvalidAttributeValue: {InvalidAttributeValue, Error, "invalid value for a constrained hx-* attribute"},
	AttributeConflict:     {AttributeConflict, Error, "mutually incompatible hx-* attribute combination"},

	DuplicateFragment:      {DuplicateFragment, Error, "duplicate fragment name within a package"},
	FragmentChildren:       {FragmentChildren, Error, "fragment bodies cannot use { children... }"},
	UnresolvableFragment:   {UnresolvableFragment, Error, "fragment reference cannot be resolved"},
	UndeclaredEvent:        {UndeclaredEvent, Error, "reference to an event name with no event declaration"},
	DuplicateEvent:         {DuplicateEvent, Error, "duplicate event name across the compiled set"},
	CircularReference:      {CircularReference, Error, "circular component or fragment reference"},
	DuplicateComponentName: {DuplicateComponentName, Error, "duplicate component name within a package"},
	ReservedImport:         {ReservedImport, Error, "template import collides with an import every generated file declares"},

	DuplicateRoute:       {DuplicateRoute, Error, "two registrations of the same verb and path"},
	UnresolvableRoute:    {UnresolvableRoute, Error, "route registration cannot be resolved by syntax-only analysis; declare it with a //ghtmx:route annotation"},
	MalformedAnnotation:  {MalformedAnnotation, Error, "malformed //ghtmx:route annotation or //ghtmx:routeprefix directive"},
	ConstructorCollision: {ConstructorCollision, Error, "route constructor name collision after disambiguation"},

	VersionMismatch:    {VersionMismatch, Error, "construct is unsupported by the configured htmx version"},
	UnsupportedVersion: {UnsupportedVersion, Error, "configured htmx version is outside the supported range"},

	CarveOutTypedBinding:   {CarveOutTypedBinding, Error, "hx-* verb attributes take a handler symbol or route constructor, not an arbitrary expression (templ carve-out 1)"},
	CarveOutStringURL:      {CarveOutStringURL, Error, "hx-* verb attributes take a handler symbol or route constructor, not a string URL (templ carve-out 1)"},
	CarveOutAuthorEscaping: {CarveOutAuthorEscaping, Error, "URL escaping at a route-binding site is engine-determined, not author-selected (templ carve-out 2)"},

	UnusedFragment:    {UnusedFragment, Warning, "fragment is never rendered or bound"},
	UnemittedEvent:    {UnemittedEvent, Warning, "event is declared but never emitted or referenced"},
	FragmentScopeHint: {FragmentScopeHint, Warning, "fragment body references an enclosing template identifier that is not a fragment parameter"},
	UnboundRoute:      {UnboundRoute, Warning, "discovered route is never bound from any template"},
	MultiPathHandler:  {MultiPathHandler, Warning, "handler is registered for the same verb at more than one path; template bindings resolve to one of them"},

	DanglingTarget: {DanglingTarget, Warning, "hx-target or hx-select literal ID selector matches no literal ID in the compiled template set"},

	StaleOutput: {StaleOutput, Warning, "generated output is stale relative to its source"},
}
