package config

// RoutePattern defines a single routing rule for a service
type RoutePattern struct {
	Name          string
	MatchType     string // "path", "header", or "both"
	PathPrefix    string
	HeaderName    string
	HeaderValue   string
	PrefixRewrite string
	Hosts         []string
}
