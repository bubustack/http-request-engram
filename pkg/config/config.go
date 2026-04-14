package config

// Config holds the configuration for the HTTPRequestEngram.
type Config struct {
	DefaultURL      string             `json:"defaultURL" mapstructure:"defaultURL"`
	DefaultMethod   string             `json:"defaultMethod" mapstructure:"defaultMethod"`
	DefaultHeaders  map[string]string  `json:"defaultHeaders" mapstructure:"defaultHeaders"`
	Auth            *AuthConfig        `json:"auth" mapstructure:"auth"`
	Response        *ResponseConfig    `json:"response" mapstructure:"response"`
	NeverError      bool               `json:"neverError" mapstructure:"neverError"`
	Timeout         string             `json:"timeout" mapstructure:"timeout"`
	Redirects       *RedirectsConfig   `json:"redirects" mapstructure:"redirects"`
	IgnoreSSLIssues bool               `json:"ignoreSSLIssues" mapstructure:"ignoreSSLIssues"`
	Proxy           string             `json:"proxy" mapstructure:"proxy"`
	Batching        *BatchingConfig    `json:"batching" mapstructure:"batching"`
	Pagination      *PaginationConfig  `json:"pagination" mapstructure:"pagination"`
	QueryParams     *QueryParamsConfig `json:"queryParams" mapstructure:"queryParams"`
}

// AuthConfig defines the structure for authentication settings.
type AuthConfig struct {
	Type       string `json:"type" mapstructure:"type"`
	HeaderName string `json:"headerName" mapstructure:"headerName"`
}

// ResponseConfig defines how the HTTP response should be handled.
type ResponseConfig struct {
	IncludeHeaders  bool   `json:"includeHeaders" mapstructure:"includeHeaders"`
	IncludeStatus   bool   `json:"includeStatus" mapstructure:"includeStatus"`
	Format          string `json:"format" mapstructure:"format"`
	OutputFieldName string `json:"outputFieldName" mapstructure:"outputFieldName"`
}

// RedirectsConfig defines the behavior for handling HTTP redirects.
type RedirectsConfig struct {
	Follow       bool `json:"follow" mapstructure:"follow"`
	MaxRedirects int  `json:"maxRedirects" mapstructure:"maxRedirects"`
}

// BatchingConfig defines the behavior for batching requests in streaming mode.
type BatchingConfig struct {
	ItemsPerBatch int    `json:"itemsPerBatch" mapstructure:"itemsPerBatch"`
	BatchInterval string `json:"batchInterval" mapstructure:"batchInterval"`
}

// PaginationConfig defines the behavior for paginating API results.
type PaginationConfig struct {
	Mode        string             `json:"mode" mapstructure:"mode"`
	ResultsPath string             `json:"resultsPath" mapstructure:"resultsPath"`
	MaxPages    int                `json:"maxPages" mapstructure:"maxPages"`
	NextURLPath string             `json:"nextURLPath" mapstructure:"nextURLPath"`
	UpdateParam *UpdateParamConfig `json:"updateParam" mapstructure:"updateParam"`
}

// UpdateParamConfig defines how to update a query parameter for pagination.
type UpdateParamConfig struct {
	Name         string `json:"name" mapstructure:"name"`
	Type         string `json:"type" mapstructure:"type"`
	InitialValue string `json:"initialValue" mapstructure:"initialValue"`
	Increment    int    `json:"increment" mapstructure:"increment"`
	CursorPath   string `json:"cursorPath" mapstructure:"cursorPath"`
}

// QueryParamsConfig defines how query parameters should be formatted.
type QueryParamsConfig struct {
	ArrayFormat string `json:"arrayFormat" mapstructure:"arrayFormat"`
}
