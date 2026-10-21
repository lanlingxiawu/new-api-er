package common

import "sync/atomic"

// RequestValidationConfig is published by performance_setting, without a
// reverse dependency from common to setting or a database read on requests.
type RequestValidationConfig struct {
	StrictJSONEnabled bool
}

var requestValidationConfig atomic.Pointer[RequestValidationConfig]

func GetRequestValidationConfig() RequestValidationConfig {
	if value := requestValidationConfig.Load(); value != nil {
		return *value
	}
	return RequestValidationConfig{}
}

func SetRequestValidationConfig(value RequestValidationConfig) {
	requestValidationConfig.Store(&value)
}
