package ghtmx

import (
	"net/url"
	"strings"
)

// EscapePathSegment percent-encodes a value for use as a single URL path
// segment. Generated route constructors call it for every path parameter
// (S1.1): the escaping context is fixed by the engine and is not
// selectable at the binding site.
func EscapePathSegment(s string) string {
	return url.PathEscape(s)
}

// EscapeQueryValue percent-encodes a value for use in a URL query
// component.
func EscapeQueryValue(s string) string {
	return url.QueryEscape(s)
}

// EscapePathWildcard percent-encodes a rest-of-path value ({name...}
// parameters): each segment is escaped individually and separators are
// preserved.
func EscapePathWildcard(s string) string {
	segments := strings.Split(s, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return strings.Join(segments, "/")
}
