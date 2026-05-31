package proxy

type rawQueryRequest struct {
	ResolverName string `json:"resolver_name"`
	DNSMessage   string `json:"dns_message"`
}

type rawQueryResponse struct {
	DNSMessage      string       `json:"dns_message,omitempty"`
	ResolverName    string       `json:"resolver_name"`
	Protocol        string       `json:"protocol"`
	ResolverAddress string       `json:"resolver_address,omitempty"`
	Cache           cacheDetails `json:"cache"`
	DurationMS      int64        `json:"duration_ms"`
	Error           *apiError    `json:"error,omitempty"`
}

type lookupRequest struct {
	ResolverName string `json:"resolver_name"`
	Hostname     string `json:"hostname"`
	Type         string `json:"type"`
	Class        string `json:"class"`
}

type lookupResponse struct {
	rawQueryResponse
	Question string      `json:"question"`
	Answers  []dnsAnswer `json:"answers"`
	RCode    string      `json:"rcode"`
}

type cacheDetails struct {
	Hit bool `json:"hit"`
	TTL int  `json:"ttl"`
}

type dnsAnswer struct {
	Name string `json:"name"`
	Type string `json:"type"`
	TTL  uint32 `json:"ttl"`
	Data string `json:"data"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
