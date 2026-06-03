package dto

type ModelWithAvailability struct {
	Name            string `json:"name"`
	DisplayName     string `json:"display_name,omitempty"`
	Owner           string `json:"owner,omitempty"`
	Available       bool   `json:"available"`
	Reason          string `json:"reason,omitempty"`
	LastCheckedTime int64  `json:"last_checked_time"`
}

type ModelListWithAvailability struct {
	Models     []ModelWithAvailability `json:"models"`
	TotalCount int                     `json:"total_count"`
	GroupName  string                  `json:"group_name,omitempty"`
}
