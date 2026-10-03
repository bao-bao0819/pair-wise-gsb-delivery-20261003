// Package permissionspolicybuilder provides a typed builder for the
// Permissions-Policy header.
//
// Unlike Content-Security-Policy, a Permissions-Policy header is a comma
// separated list of feature directives:
//
//	geolocation=(self "https://example.com"), camera=()
//
// The allowlist keywords "*", "self" and "none" are unquoted, while explicit
// origins are serialized inside double quotes. This package follows those rules
// rather than the quoting and separator rules used by CSP.
package permissionspolicybuilder

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// AllowlistType describes how a feature may be used.
type AllowlistType string

const (
	// AllowAll permits the feature for all origins. It serializes to "*".
	AllowAll AllowlistType = "*"
	// AllowNone denies the feature for all origins. It serializes to "()".
	AllowNone AllowlistType = "none"
	// AllowSelf permits the feature for the document's own origin.
	// It serializes to "(self)".
	AllowSelf AllowlistType = "self"
	// AllowOrigins permits the feature only for the explicit origins listed
	// in FeatureRule.Origins. An empty list is equivalent to AllowNone.
	AllowOrigins AllowlistType = "origins"
)

// FeatureRule is the typed allowlist for a single feature.
type FeatureRule struct {
	// Type selects the kind of allowlist. It must be one of AllowAll,
	// AllowNone, AllowSelf or AllowOrigins.
	Type AllowlistType
	// Origins is consulted only when Type is AllowOrigins. Each entry must
	// be an absolute http or https URL with no path, query or fragment.
	Origins []string
}

// Builder assembles a Permissions-Policy header value from named feature
// rules. The zero value Builder produces an empty policy.
type Builder struct {
	Features map[string]FeatureRule
}

var featureNameRegexp = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// Add registers the allowlist rule for a feature.
//
// Giving the same feature more than once is accepted only when the resulting
// rule is identical; contradictory values for the same feature return an
// error. Origins are validated individually before the rule is stored.
func (builder *Builder) Add(feature string, rule FeatureRule) error {
	if !featureNameRegexp.MatchString(feature) {
		return fmt.Errorf("invalid feature name %q", feature)
	}

	directive, err := buildFeature(feature, rule)
	if err != nil {
		return err
	}

	if existing, ok := builder.Features[feature]; ok {
		existingDirective, err := buildFeature(feature, existing)
		if err != nil {
			return err
		}

		if existingDirective != directive {
			return fmt.Errorf("contradictory rules provided for feature %s", feature)
		}

		return nil
	}

	if builder.Features == nil {
		builder.Features = make(map[string]FeatureRule)
	}

	builder.Features[feature] = rule

	return nil
}

// MustBuild is like Build but panics if an error occurs.
func (builder *Builder) MustBuild() string {
	policy, err := builder.Build()
	if err != nil {
		panic(err)
	}

	return policy
}

// Build creates a Permissions-Policy string from the configured feature rules.
//
// Feature directives are emitted in lexicographic feature-name order so the
// output is stable. It is an error to give the same feature contradictory
// configuration more than once (for example via a duplicate map entry), and
// every explicit origin is validated individually.
func (builder *Builder) Build() (string, error) {
	featureNames := make([]string, 0, len(builder.Features))
	for feature := range builder.Features {
		featureNames = append(featureNames, feature)
	}

	// Stable ordering of the feature directives.
	sort.Strings(featureNames)

	directives := make([]string, 0, len(featureNames))

	for _, feature := range featureNames {
		if !featureNameRegexp.MatchString(feature) {
			return "", fmt.Errorf("invalid feature name %q", feature)
		}

		directive, err := buildFeature(feature, builder.Features[feature])
		if err != nil {
			return "", err
		}

		directives = append(directives, directive)
	}

	// Permissions-Policy directives are comma separated, unlike CSP.
	return strings.Join(directives, ", "), nil
}

func buildFeature(feature string, rule FeatureRule) (string, error) {
	switch rule.Type {
	case AllowAll:
		if len(rule.Origins) != 0 {
			return "", fmt.Errorf("feature %s set to allow all but also lists origins", feature)
		}

		return feature + "=*", nil
	case AllowNone:
		if len(rule.Origins) != 0 {
			return "", fmt.Errorf("feature %s set to deny all but also lists origins", feature)
		}

		return feature + "=()", nil
	case AllowSelf:
		if len(rule.Origins) != 0 {
			return "", fmt.Errorf("feature %s set to self but also lists origins", feature)
		}

		return feature + "=(self)", nil
	case AllowOrigins:
		// An empty origin allowlist denies all origins.
		if len(rule.Origins) == 0 {
			return feature + "=()", nil
		}

		origins := make([]string, 0, len(rule.Origins))

		for _, origin := range rule.Origins {
			canonical, err := validateOrigin(feature, origin)
			if err != nil {
				return "", err
			}

			origins = append(origins, canonical)
		}

		return fmt.Sprintf("%s=(%s)", feature, strings.Join(quoteOrigins(origins), " ")), nil
	default:
		return "", fmt.Errorf("feature %s has unknown allowlist type %q", feature, rule.Type)
	}
}

func quoteOrigins(origins []string) []string {
	quoted := make([]string, len(origins))
	for i, origin := range origins {
		quoted[i] = strconv.Quote(origin)
	}

	return quoted
}

func validateOrigin(feature, origin string) (string, error) {
	if strings.ContainsAny(origin, "?#") {
		return "", fmt.Errorf("invalid origin %q for feature %s: query strings and fragments are not allowed", origin, feature)
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("invalid origin %q for feature %s: %w", origin, feature, err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid origin %q for feature %s: scheme must be http or https", origin, feature)
	}

	if parsed.User != nil {
		return "", fmt.Errorf("invalid origin %q for feature %s: user info is not allowed", origin, feature)
	}

	host := parsed.Hostname()
	if host == "" || strings.Contains(host, "*") {
		return "", fmt.Errorf("invalid origin %q for feature %s: a concrete host is required", origin, feature)
	}

	if port := parsed.Port(); port != "" {
		if _, err := strconv.Atoi(port); err != nil {
			return "", fmt.Errorf("invalid origin %q for feature %s: invalid port", origin, feature)
		}
	}

	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("invalid origin %q for feature %s: a path is not allowed", origin, feature)
	}

	if parsed.Fragment != "" {
		return "", fmt.Errorf("invalid origin %q for feature %s: a fragment is not allowed", origin, feature)
	}

	return parsed.Scheme + "://" + parsed.Host, nil
}
