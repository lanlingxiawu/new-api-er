package xai

type videoGenerationRequest struct {
	Model           string       `json:"model"`
	Prompt          string       `json:"prompt"`
	Duration        int          `json:"duration,omitempty"`
	AspectRatio     string       `json:"aspect_ratio,omitempty"`
	Resolution      string       `json:"resolution,omitempty"`
	Seed            *int         `json:"seed,omitempty"`
	Image           *videoInput  `json:"image,omitempty"`
	ReferenceImages []videoInput `json:"reference_images,omitempty"`
}

type videoInput struct {
	URL    string `json:"url,omitempty"`
	FileID string `json:"file_id,omitempty"`
}

type submitResponse struct {
	RequestID string `json:"request_id"`
}

type statusResponse struct {
	Status string `json:"status"`
	Model  string `json:"model,omitempty"`
	Video  *struct {
		URL      string `json:"url,omitempty"`
		Duration int    `json:"duration,omitempty"`
	} `json:"video,omitempty"`
	Error *struct {
		Message string `json:"message,omitempty"`
		Code    string `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

type requestMetadata struct {
	Duration        *int         `json:"duration,omitempty"`
	DurationSeconds *int         `json:"durationSeconds,omitempty"`
	AspectRatio     string       `json:"aspect_ratio,omitempty"`
	Resolution      string       `json:"resolution,omitempty"`
	Seed            *int         `json:"seed,omitempty"`
	Image           *videoInput  `json:"image,omitempty"`
	ReferenceImages []videoInput `json:"reference_images,omitempty"`
}
