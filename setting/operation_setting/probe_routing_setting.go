package operation_setting

import (
	"errors"
	"github.com/QuantumNous/new-api/setting/config"
)

// Enablement belongs exclusively to each channel, not to a global switch.
type ProbeRoutingSetting struct {
	MaxInputChars int `json:"max_input_chars"`
}

var probeRoutingSetting = ProbeRoutingSetting{MaxInputChars: 128}
var probeRoutingSnapshot config.Snapshot[ProbeRoutingSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot("probe_routing_setting", &probeRoutingSetting, publishProbeRoutingSetting)
}

func publishProbeRoutingSetting() {
	value := probeRoutingSetting
	if ValidateProbeRoutingSetting(value) != nil {
		value.MaxInputChars = 128
	}
	probeRoutingSnapshot.Publish(value)
}

func GetProbeRoutingSnapshot() *ProbeRoutingSetting { return probeRoutingSnapshot.Load() }

func ReplaceProbeRoutingSetting(value ProbeRoutingSetting) {
	config.WithConfigDraft(func() { probeRoutingSetting = value; publishProbeRoutingSetting() })
}

func ValidateProbeRoutingSetting(value ProbeRoutingSetting) error {
	if value.MaxInputChars < 1 || value.MaxInputChars > 1024 {
		return errors.New("probe input character limit must be between 1 and 1024")
	}
	return nil
}
