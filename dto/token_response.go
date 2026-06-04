package dto

type AvailableModelsResponse struct {
	Models []string `json:"models"`
	Count  int      `json:"count"`
}
