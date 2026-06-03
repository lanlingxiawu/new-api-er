package dto

type GroupStatusInfo struct {
	AvailableModels  int     `json:"available_models"`
	TotalModels      int     `json:"total_models"`
	AvailabilityRate float64 `json:"availability_rate"`
	LastTestTime     int64   `json:"last_test_time"`
}

type AvailableModelsResponse struct {
	Models []string `json:"models"`
	Count  int      `json:"count"`
}
