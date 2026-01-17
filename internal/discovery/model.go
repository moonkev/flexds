package discovery

// ServiceInstance represents a discovered service instance
type ServiceInstance struct {
	Address string
	Port    int
}

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

// DiscoveredService represents a service with its instances and routing configuration
type DiscoveredService struct {
	Name        string
	EnableHTTP2 bool
	Instances   []*ServiceInstance
	Routes      []RoutePattern // Routing patterns for this service
}
