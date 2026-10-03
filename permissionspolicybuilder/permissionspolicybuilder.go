// Package permissionspolicybuilder provides a typed builder for the
// Permissions-Policy header. Unlike a raw header string it distinguishes
// between allowing a feature for every origin, blocking it everywhere,
// allowing it same-origin only, and allowing it for an explicit origin list.
package permissionspolicybuilder

import (
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// featureToken matches a Permissions-Policy feature token: one or more
// lowercase ASCII letters, digits, or hyphens.
var featureToken = regexp.MustCompile(`^[a-z0-9-]+$`)

// AllowList describes the origins that may use a feature.
//
// The zero value denies the feature everywhere. Otherwise:
//   - AllowAll allows the feature for every origin (the "*" wildcard);
//   - Self additionally allows the feature in the document's own origin;
//   - Origins explicitly lists additional origins serialized as quoted URLs.
//
// AllowAll cannot be combined with Self or Origins.
type AllowList struct {
	AllowAll bool
	Self     bool
	Origins  []string
}

// Builder collects feature allow lists and serializes them into a
// Permissions-Policy header value.
type Builder struct {
	Features map[string]AllowList
}

// AddFeature registers feature with allowList. Registering the same feature
// again with an identical allow list is a no-op; a conflicting allow list
// returns an error. Each origin is validated individually.
func (builder *Builder) AddFeature(feature string, allowList AllowList) error {
	if err := validateFeature(feature); err != nil {
		return err
	}

	if err := validateAllowList(allowList); err != nil {
		return fmt.Errorf("invalid allow list for feature %s: %w", feature, err)
	}

	if len(allowList.Origins) == 0 {
		allowList.Origins = nil
	}

	if builder.Features == nil {
		builder.Features = make(map[string]AllowList)
	}

	if existing, ok := builder.Features[feature]; ok {
		if !reflect.DeepEqual(existing, allowList) {
			return fmt.Errorf("conflicting allow list for feature %s: %v and %v", feature, existing, allowList)
		}

		return nil
	}

	builder.Features[feature] = allowList

	return nil
}

// AllowFeature allows feature for every origin: `feature=*`.
func (builder *Builder) AllowFeature(feature string) error {
	return builder.AddFeature(feature, AllowList{AllowAll: true})
}

// BlockFeature denies feature everywhere: `feature=()`.
func (builder *Builder) BlockFeature(feature string) error {
	return builder.AddFeature(feature, AllowList{})
}

// AllowSelf allows feature only in the document's own origin: `feature=(self)`.
func (builder *Builder) AllowSelf(feature string) error {
	return builder.AddFeature(feature, AllowList{Self: true})
}

// AllowOrigins allows feature for the document's own origin and the given
// origins, for example `feature=(self "https://example.com")`. Each origin is
// validated individually. An empty origin list allows same-origin only.
func (builder *Builder) AllowOrigins(feature string, origins ...string) error {
	return builder.AddFeature(feature, AllowList{Self: true, Origins: origins})
}

// MustBuild is like Build but panics if an error occurs.
func (builder *Builder) MustBuild() string {
	policy, err := builder.Build()
	if err != nil {
		panic(err)
	}

	return policy
}

// Build serializes the registered features into a Permissions-Policy header
// value. Features are emitted in lexicographic order for stable output. The
// value follows Permissions-Policy syntax (comma-separated features, quoted
// origins) rather than CSP syntax (semicolon-separated directives).
func (builder *Builder) Build() (string, error) {
	features := make([]string, 0, len(builder.Features))

	for feature, allowList := range builder.Features {
		if err := validateFeature(feature); err != nil {
			return "", err
		}

		if err := validateAllowList(allowList); err != nil {
			return "", fmt.Errorf("invalid allow list for feature %s: %w", feature, err)
		}

		features = append(features, feature)
	}

	sort.Strings(features)

	var sb strings.Builder

	for _, feature := range features {
		if sb.Len() > 0 {
			sb.WriteString(", ")
		}

		sb.WriteString(feature)

		allowList := builder.Features[feature]
		if allowList.AllowAll {
			sb.WriteString("=*")

			continue
		}

		sb.WriteString("=(")

		first := true

		if allowList.Self {
			sb.WriteString("self")
			first = false
		}

		for _, origin := range allowList.Origins {
			if !first {
				sb.WriteString(" ")
			}

			sb.WriteString(`"` + origin + `"`)

			first = false
		}

		sb.WriteString(")")
	}

	return sb.String(), nil
}

func validateFeature(feature string) error {
	if !featureToken.MatchString(feature) {
		return fmt.Errorf("invalid feature name %q: must match %s", feature, featureToken.String())
	}

	return nil
}

func validateAllowList(allowList AllowList) error {
	if allowList.AllowAll && (allowList.Self || len(allowList.Origins) > 0) {
		return fmt.Errorf("the * wildcard cannot be combined with self or explicit origins")
	}

	for _, origin := range allowList.Origins {
		if err := validateOrigin(origin); err != nil {
			return err
		}
	}

	return nil
}

// validateOrigin ensures origin is a serialized http(s) origin: an absolute
// http/https URL with no path, query, fragment, or user info.
func validateOrigin(origin string) error {
	if origin == "" {
		return fmt.Errorf("origin must not be empty")
	}

	if strings.ContainsAny(origin, `"\<>`) {
		return fmt.Errorf("invalid origin %q: must not contain quotes or angle brackets", origin)
	}

	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("invalid origin %q: %w", origin, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid origin %q: scheme must be http or https", origin)
	}

	if u.Host == "" {
		return fmt.Errorf("invalid origin %q: missing host", origin)
	}

	if u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("invalid origin %q: origin must contain only scheme and host (no path, query, fragment, or user info)", origin)
	}

	return nil
}
