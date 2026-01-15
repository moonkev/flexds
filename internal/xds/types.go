package xds

// RoutePattern defines a single routing rule for a service
type RoutePattern struct {
	Name             string
	MatchType        string // "path", "header", or "both"
	PathPrefix       string
	HeaderName       string
	HeaderValue      string
	PrefixRewrite    string // legacy: simple string rewrite
	RegexRewrite     string // regex pattern to match for rewriting
	RegexReplacement string // what to replace the regex match with
	Hosts            []string
}
