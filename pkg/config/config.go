package config

// Config holds the configuration for the HTTPRequestEngram.
type Config struct {
	DefaultURL      string             `json:"defaultURL"`
	DefaultMethod   string             `json:"defaultMethod"`
	DefaultHeaders  map[string]string  `json:"defaultHeaders"`
	Auth            *AuthConfig        `json:"auth"`
	Response        *ResponseConfig    `json:"response"`
	NeverError      bool               `json:"neverError"`
	Timeout         string             `json:"timeout"`
	Redirects       *RedirectsConfig   `json:"redirects"`
	IgnoreSSLIssues bool               `json:"ignoreSSLIssues"`
	Proxy           string             `json:"proxy"`
	Batching        *BatchingConfig    `json:"batching"`
	Pagination      *PaginationConfig  `json:"pagination"`
	QueryParams     *QueryParamsConfig `json:"queryParams"`
}

// AuthConfig defines the structure for authentication settings.
type AuthConfig struct {
	Type       string `json:"type"`
	HeaderName string `json:"headerName"`
}

// ResponseConfig defines how the HTTP response should be handled.
type ResponseConfig struct {
	IncludeHeaders  bool   `json:"includeHeaders"`
	IncludeStatus   bool   `json:"includeStatus"`
	Format          string `json:"format"`
	OutputFieldName string `json:"outputFieldName"`
}

// RedirectsConfig defines the behavior for handling HTTP redirects.
type RedirectsConfig struct {
	Follow       bool `json:"follow"`
	MaxRedirects int  `json:"maxRedirects"`
}

// BatchingConfig defines the behavior for batching requests in streaming mode.
type BatchingConfig struct {
	ItemsPerBatch int    `json:"itemsPerBatch"`
	BatchInterval string `json:"batchInterval"`
}

// PaginationConfig defines the behavior for paginating API results.
type PaginationConfig struct {
	Mode        string             `json:"mode"`
	ResultsPath string             `json:"resultsPath"`
	MaxPages    int                `json:"maxPages"`
	NextURLPath string             `json:"nextURLPath"`
	UpdateParam *UpdateParamConfig `json:"updateParam"`
}

// UpdateParamConfig defines how to update a query parameter for pagination.
type UpdateParamConfig struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	InitialValue string `json:"initialValue"`
	Increment    int    `json:"increment"`
	CursorPath   string `json:"cursorPath"`
}

// QueryParamsConfig defines how query parameters should be formatted.
type QueryParamsConfig struct {
	ArrayFormat string `json:"arrayFormat"`
}
