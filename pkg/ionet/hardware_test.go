package ionet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GetAvailableReplicas
// ---------------------------------------------------------------------------

func TestGetAvailableReplicas_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetAvailableReplicas(0, 1)
	assert.ErrorContains(t, err, "hardware_id must be greater than 0")
	_, err = c.GetAvailableReplicas(-1, 1)
	assert.ErrorContains(t, err, "hardware_id must be greater than 0")
	_, err = c.GetAvailableReplicas(5, 0)
	assert.ErrorContains(t, err, "gpu_count must be at least 1")
}

func TestGetAvailableReplicas_Success(t *testing.T) {
	body := `{"data":[{"id":7,"iso2":"us","name":"US East","available_replicas":3}]}`
	c, stub := newStubClient(okResp(body), nil)
	resp, err := c.GetAvailableReplicas(5, 2)
	require.NoError(t, err)
	require.Len(t, resp.Replicas, 1)
	r := resp.Replicas[0]
	assert.Equal(t, 7, r.LocationID)
	assert.Equal(t, "US East", r.LocationName)
	assert.Equal(t, 5, r.HardwareID)     // echoed from arg
	assert.Equal(t, 3, r.AvailableCount) // mapped from available_replicas
	assert.Equal(t, 2, r.MaxGPUs)        // echoed from gpuCount
	ep := requestPath(t, stub)
	assert.Contains(t, ep, "hardware_id=5")
	assert.Contains(t, ep, "hardware_qty=2")
}

func TestGetAvailableReplicas_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetAvailableReplicas(5, 1)
	assert.ErrorContains(t, err, "failed to get available replicas")
}

func TestGetAvailableReplicas_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.GetAvailableReplicas(5, 1)
	assert.ErrorContains(t, err, "failed to parse available replicas response")
}

// ---------------------------------------------------------------------------
// GetMaxGPUsPerContainer
// ---------------------------------------------------------------------------

func TestGetMaxGPUsPerContainer_Success(t *testing.T) {
	body := `{"data":{"hardware":[{"hardware_id":1,"max_gpus_per_container":8,"available":4}],"total":4}}`
	c, stub := newStubClient(okResp(body), nil)
	resp, err := c.GetMaxGPUsPerContainer()
	require.NoError(t, err)
	require.Len(t, resp.Hardware, 1)
	assert.Equal(t, 8, resp.Hardware[0].MaxGPUsPerContainer)
	assert.Equal(t, 4, resp.Total)
	assert.Equal(t, "/hardware/max-gpus-per-container", requestPath(t, stub))
}

func TestGetMaxGPUsPerContainer_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetMaxGPUsPerContainer()
	assert.ErrorContains(t, err, "failed to get max GPUs per container")
}

func TestGetMaxGPUsPerContainer_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.GetMaxGPUsPerContainer()
	assert.ErrorContains(t, err, "failed to parse max GPU response")
}

// ---------------------------------------------------------------------------
// ListHardwareTypes
// ---------------------------------------------------------------------------

func TestListHardwareTypes_MappingAndNamedHardware(t *testing.T) {
	body := `{"data":{"hardware":[
		{"hardware_id":1,"max_gpus_per_container":8,"available":2,"hardware_name":"A100","brand_name":" NVIDIA "},
		{"hardware_id":2,"max_gpus_per_container":4,"available":0,"hardware_name":"  "}
	],"total":10}}`
	c, _ := newStubClient(okResp(body), nil)
	hw, total, err := c.ListHardwareTypes()
	require.NoError(t, err)
	require.Len(t, hw, 2)

	// first: has a name and is available (available>0), brand trimmed
	assert.Equal(t, "A100", hw[0].Name)
	assert.True(t, hw[0].Available)
	assert.Equal(t, "NVIDIA", hw[0].BrandName)
	assert.Equal(t, 8, hw[0].MaxGPUs)
	assert.Equal(t, 2, hw[0].AvailableCount)

	// second: blank name → synthesized "Hardware 2", available==0 → not available
	assert.Equal(t, "Hardware 2", hw[1].Name)
	assert.False(t, hw[1].Available)

	// total comes from the response Total (non-zero) branch
	assert.Equal(t, 10, total)
}

func TestListHardwareTypes_TotalFallbackSum(t *testing.T) {
	// total==0 → sum of Available values (2+3=5).
	body := `{"data":{"hardware":[
		{"hardware_id":1,"available":2},
		{"hardware_id":2,"available":3}
	],"total":0}}`
	c, _ := newStubClient(okResp(body), nil)
	_, total, err := c.ListHardwareTypes()
	require.NoError(t, err)
	assert.Equal(t, 5, total)
}

func TestListHardwareTypes_ErrorFromUnderlyingCall(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, _, err := c.ListHardwareTypes()
	assert.ErrorContains(t, err, "failed to list hardware types")
}

// ---------------------------------------------------------------------------
// ListLocations
// ---------------------------------------------------------------------------

func TestListLocations_Success_ISO2UppercasedAndTotal(t *testing.T) {
	body := `{"data":{"locations":[{"id":1,"name":"US","iso2":" us ","available":4}],"total":9}}`
	c, stub := newStubClient(okResp(body), nil)
	resp, err := c.ListLocations()
	require.NoError(t, err)
	require.Len(t, resp.Locations, 1)
	assert.Equal(t, "US", resp.Locations[0].ISO2) // trimmed + uppercased
	assert.Equal(t, 9, resp.Total)                // provided total kept
	assert.Equal(t, "/locations", requestPath(t, stub))
}

func TestListLocations_TotalFallbackSum(t *testing.T) {
	body := `{"data":{"locations":[{"id":1,"available":4},{"id":2,"available":6}],"total":0}}`
	c, _ := newStubClient(okResp(body), nil)
	resp, err := c.ListLocations()
	require.NoError(t, err)
	assert.Equal(t, 10, resp.Total) // 4+6
}

func TestListLocations_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.ListLocations()
	assert.ErrorContains(t, err, "failed to list locations")
}

func TestListLocations_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.ListLocations()
	assert.ErrorContains(t, err, "failed to parse locations response")
}

// ---------------------------------------------------------------------------
// GetHardwareType
// ---------------------------------------------------------------------------

func TestGetHardwareType_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetHardwareType(0)
	assert.ErrorContains(t, err, "hardware ID must be greater than 0")
}

func TestGetHardwareType_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"id":3,"name":"A100","available":true}`), nil)
	hw, err := c.GetHardwareType(3)
	require.NoError(t, err)
	assert.Equal(t, "A100", hw.Name)
	assert.True(t, hw.Available)
	assert.Equal(t, "/hardware/types/3", requestPath(t, stub))
}

func TestGetHardwareType_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetHardwareType(3)
	assert.ErrorContains(t, err, "failed to get hardware type")
}

func TestGetHardwareType_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.GetHardwareType(3)
	assert.ErrorContains(t, err, "failed to parse hardware type")
}

// ---------------------------------------------------------------------------
// GetLocation
// ---------------------------------------------------------------------------

func TestGetLocation_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetLocation(0)
	assert.ErrorContains(t, err, "location ID must be greater than 0")
}

func TestGetLocation_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"id":2,"name":"US East"}`), nil)
	loc, err := c.GetLocation(2)
	require.NoError(t, err)
	assert.Equal(t, "US East", loc.Name)
	assert.Equal(t, "/locations/2", requestPath(t, stub))
}

func TestGetLocation_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetLocation(2)
	assert.ErrorContains(t, err, "failed to get location")
}

func TestGetLocation_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.GetLocation(2)
	assert.ErrorContains(t, err, "failed to parse location")
}

// ---------------------------------------------------------------------------
// GetLocationAvailability
// ---------------------------------------------------------------------------

func TestGetLocationAvailability_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetLocationAvailability(0)
	assert.ErrorContains(t, err, "location ID must be greater than 0")
}

func TestGetLocationAvailability_Success(t *testing.T) {
	body := `{"location_id":2,"location_name":"US","available":true,"hardware_availability":[{"hardware_id":1,"available_count":5}]}`
	c, stub := newStubClient(okResp(body), nil)
	av, err := c.GetLocationAvailability(2)
	require.NoError(t, err)
	assert.Equal(t, 2, av.LocationID)
	assert.True(t, av.Available)
	require.Len(t, av.HardwareAvailability, 1)
	assert.Equal(t, 5, av.HardwareAvailability[0].AvailableCount)
	assert.Equal(t, "/locations/2/availability", requestPath(t, stub))
}

func TestGetLocationAvailability_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetLocationAvailability(2)
	assert.ErrorContains(t, err, "failed to get location availability")
}

func TestGetLocationAvailability_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.GetLocationAvailability(2)
	assert.ErrorContains(t, err, "failed to parse location availability")
}
