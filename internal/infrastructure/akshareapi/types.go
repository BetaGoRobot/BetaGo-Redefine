package akshareapi

type Domain string

const (
	DomainStock   Domain = "stock"
	DomainGold    Domain = "gold"
	DomainFutures Domain = "futures"
	DomainInfo    Domain = "info"
)

type ValueKind string

const (
	ValueKindString  ValueKind = "string"
	ValueKindInteger ValueKind = "integer"
	ValueKindNumber  ValueKind = "number"
	ValueKindBoolean ValueKind = "boolean"
	ValueKindArray   ValueKind = "array"
	ValueKindObject  ValueKind = "object"
	ValueKindUnknown ValueKind = "unknown"
)

type ParamSpec struct {
	Name        string
	GoName      string
	Kind        ValueKind
	Description string
}

type FieldSpec struct {
	Name        string
	Kind        ValueKind
	Description string
}

type Endpoint struct {
	Name        string
	MethodName  string
	Tags        []Domain
	Summary     string
	Description string
	DocURL      string
	SourceURL   string
	TargetURL   string
	Params      []ParamSpec
	Fields      []FieldSpec
}

type Row map[string]any
type Rows []Row

var (
	orderedEndpoints []Endpoint
	endpointIndex    map[string]Endpoint
	domainIndex      map[Domain][]Endpoint
)

func registerGeneratedEndpoints(endpoints []Endpoint) {
	orderedEndpoints = make([]Endpoint, len(endpoints))
	copy(orderedEndpoints, endpoints)

	endpointIndex = make(map[string]Endpoint, len(endpoints))
	domainIndex = make(map[Domain][]Endpoint, 4)
	for _, endpoint := range orderedEndpoints {
		endpointIndex[endpoint.Name] = endpoint
		for _, tag := range endpoint.Tags {
			domainIndex[tag] = append(domainIndex[tag], endpoint)
		}
	}
}

func AllEndpoints() []Endpoint {
	out := make([]Endpoint, len(orderedEndpoints))
	copy(out, orderedEndpoints)
	return out
}

func EndpointByName(name string) (Endpoint, bool) {
	endpoint, ok := endpointIndex[name]
	return endpoint, ok
}

func EndpointsForDomain(domain Domain) []Endpoint {
	items := domainIndex[domain]
	out := make([]Endpoint, len(items))
	copy(out, items)
	return out
}
