package ionet

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validDeploymentRequest returns a request that passes every validation check,
// so individual tests can zero-out a single field to exercise one branch.
func validDeploymentRequest() *DeploymentRequest {
	return &DeploymentRequest{
		ResourcePrivateName: "res",
		DurationHours:       2,
		GPUsPerContainer:    1,
		HardwareID:          5,
		LocationIDs:         []int{1},
		ContainerConfig:     ContainerConfig{ReplicaCount: 1},
		RegistryConfig:      RegistryConfig{ImageURL: "img"},
	}
}

// ---------------------------------------------------------------------------
// DeployContainer — validation matrix + success + errors
// ---------------------------------------------------------------------------

func TestDeployContainer_NilRequest(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.DeployContainer(nil)
	assert.ErrorContains(t, err, "deployment request cannot be nil")
}

func TestDeployContainer_ValidationMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DeploymentRequest)
		want   string
	}{
		{"emptyResourceName", func(r *DeploymentRequest) { r.ResourcePrivateName = "" }, "resource_private_name is required"},
		{"noLocations", func(r *DeploymentRequest) { r.LocationIDs = nil }, "location_ids is required"},
		{"hardwareZero", func(r *DeploymentRequest) { r.HardwareID = 0 }, "hardware_id is required"},
		{"hardwareNegative", func(r *DeploymentRequest) { r.HardwareID = -1 }, "hardware_id is required"},
		{"noImage", func(r *DeploymentRequest) { r.RegistryConfig.ImageURL = "" }, "registry_config.image_url is required"},
		{"gpusZero", func(r *DeploymentRequest) { r.GPUsPerContainer = 0 }, "gpus_per_container must be at least 1"},
		{"durationZero", func(r *DeploymentRequest) { r.DurationHours = 0 }, "duration_hours must be at least 1"},
		{"replicaZero", func(r *DeploymentRequest) { r.ContainerConfig.ReplicaCount = 0 }, "container_config.replica_count must be at least 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newStubClient(okResp("{}"), nil)
			req := validDeploymentRequest()
			tc.mutate(req)
			_, err := c.DeployContainer(req)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestDeployContainer_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"deployment_id":"dep-9","status":"pending"}`), nil)
	resp, err := c.DeployContainer(validDeploymentRequest())
	require.NoError(t, err)
	assert.Equal(t, "dep-9", resp.DeploymentID)
	assert.Equal(t, "pending", resp.Status)
	assert.Equal(t, "POST", stub.last.Method)
	assert.Equal(t, "/deploy", requestPath(t, stub))
}

func TestDeployContainer_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.DeployContainer(validDeploymentRequest())
	assert.ErrorContains(t, err, "failed to deploy container")
}

func TestDeployContainer_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("not json"), nil)
	_, err := c.DeployContainer(validDeploymentRequest())
	assert.ErrorContains(t, err, "failed to parse deployment response")
}

// ---------------------------------------------------------------------------
// ListDeployments
// ---------------------------------------------------------------------------

func TestListDeployments_NilOpts(t *testing.T) {
	c, stub := newStubClient(okResp(`{"data":{"deployments":[],"total":0}}`), nil)
	_, err := c.ListDeployments(nil)
	require.NoError(t, err)
	assert.Equal(t, "/deployments", requestPath(t, stub))
}

func TestListDeployments_WithOptsAndDerivedFields(t *testing.T) {
	c, stub := newStubClient(okResp(`{"data":{"deployments":[{"id":"d1","hardware_quantity":4}],"total":1}}`), nil)
	list, err := c.ListDeployments(&ListDeploymentsOptions{
		Status: "running", LocationID: 2, Page: 1, PageSize: 20, SortBy: "created_at", SortOrder: "desc",
	})
	require.NoError(t, err)
	require.Len(t, list.Deployments, 1)
	// GPUCount and Replicas are derived from HardwareQuantity.
	assert.Equal(t, 4, list.Deployments[0].GPUCount)
	assert.Equal(t, 4, list.Deployments[0].Replicas)
	assert.Equal(t, 4, list.Deployments[0].HardwareQuantity)
	ep := requestPath(t, stub)
	assert.True(t, strings.HasPrefix(ep, "/deployments?"))
	assert.Contains(t, ep, "status=running")
	assert.Contains(t, ep, "page=1")
}

func TestListDeployments_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.ListDeployments(nil)
	assert.ErrorContains(t, err, "failed to list deployments")
}

func TestListDeployments_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.ListDeployments(nil)
	assert.ErrorContains(t, err, "failed to parse deployments list")
}

// ---------------------------------------------------------------------------
// GetDeployment
// ---------------------------------------------------------------------------

func TestGetDeployment_EmptyID(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetDeployment("")
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
}

func TestGetDeployment_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"data":{"id":"d1","status":"active","created_at":"2023-06-15T12:30:45"}}`), nil)
	got, err := c.GetDeployment("d1")
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, 2023, got.CreatedAt.Year())
	assert.Equal(t, "/deployment/d1", requestPath(t, stub))
}

func TestGetDeployment_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetDeployment("d1")
	assert.ErrorContains(t, err, "failed to get deployment details")
}

func TestGetDeployment_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.GetDeployment("d1")
	assert.ErrorContains(t, err, "failed to parse deployment details")
}

// ---------------------------------------------------------------------------
// UpdateDeployment
// ---------------------------------------------------------------------------

func TestUpdateDeployment_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.UpdateDeployment("", &UpdateDeploymentRequest{})
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
	_, err = c.UpdateDeployment("d1", nil)
	assert.ErrorContains(t, err, "update request cannot be nil")
}

func TestUpdateDeployment_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"status":"ok","deployment_id":"d1"}`), nil)
	resp, err := c.UpdateDeployment("d1", &UpdateDeploymentRequest{ImageURL: "img2"})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "PATCH", stub.last.Method)
	assert.Equal(t, "/deployment/d1", requestPath(t, stub))
}

func TestUpdateDeployment_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.UpdateDeployment("d1", &UpdateDeploymentRequest{})
	assert.ErrorContains(t, err, "failed to update deployment")
}

func TestUpdateDeployment_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.UpdateDeployment("d1", &UpdateDeploymentRequest{})
	assert.ErrorContains(t, err, "failed to parse update deployment response")
}

// ---------------------------------------------------------------------------
// ExtendDeployment
// ---------------------------------------------------------------------------

func TestExtendDeployment_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.ExtendDeployment("", &ExtendDurationRequest{DurationHours: 1})
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
	_, err = c.ExtendDeployment("d1", nil)
	assert.ErrorContains(t, err, "extend request cannot be nil")
	_, err = c.ExtendDeployment("d1", &ExtendDurationRequest{DurationHours: 0})
	assert.ErrorContains(t, err, "duration_hours must be at least 1")
}

func TestExtendDeployment_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"data":{"id":"d1","status":"active","created_at":"2023-06-15T12:30:45"}}`), nil)
	got, err := c.ExtendDeployment("d1", &ExtendDurationRequest{DurationHours: 5})
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, "/deployment/d1/extend", requestPath(t, stub))
}

func TestExtendDeployment_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.ExtendDeployment("d1", &ExtendDurationRequest{DurationHours: 1})
	assert.ErrorContains(t, err, "failed to extend deployment")
}

func TestExtendDeployment_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.ExtendDeployment("d1", &ExtendDurationRequest{DurationHours: 1})
	assert.ErrorContains(t, err, "failed to parse extended deployment details")
}

// ---------------------------------------------------------------------------
// DeleteDeployment
// ---------------------------------------------------------------------------

func TestDeleteDeployment_EmptyID(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.DeleteDeployment("")
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
}

func TestDeleteDeployment_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"status":"deleted","deployment_id":"d1"}`), nil)
	resp, err := c.DeleteDeployment("d1")
	require.NoError(t, err)
	assert.Equal(t, "deleted", resp.Status)
	assert.Equal(t, "DELETE", stub.last.Method)
}

func TestDeleteDeployment_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.DeleteDeployment("d1")
	assert.ErrorContains(t, err, "failed to delete deployment")
}

func TestDeleteDeployment_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.DeleteDeployment("d1")
	assert.ErrorContains(t, err, "failed to parse delete deployment response")
}

// ---------------------------------------------------------------------------
// GetPriceEstimation — validation + duration-type switch + computation
// ---------------------------------------------------------------------------

func validPriceReq() *PriceEstimationRequest {
	return &PriceEstimationRequest{
		LocationIDs:      []int{1},
		HardwareID:       5,
		GPUsPerContainer: 2,
		DurationHours:    3,
		ReplicaCount:     1,
	}
}

func TestGetPriceEstimation_NilRequest(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetPriceEstimation(nil)
	assert.ErrorContains(t, err, "price estimation request cannot be nil")
}

func TestGetPriceEstimation_ValidationMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PriceEstimationRequest)
		want   string
	}{
		{"noLocations", func(r *PriceEstimationRequest) { r.LocationIDs = nil }, "location_ids is required"},
		{"hardwareZero", func(r *PriceEstimationRequest) { r.HardwareID = 0 }, "hardware_id is required"},
		{"replicaZero", func(r *PriceEstimationRequest) { r.ReplicaCount = 0 }, "replica_count must be at least 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newStubClient(okResp("{}"), nil)
			req := validPriceReq()
			tc.mutate(req)
			_, err := c.GetPriceEstimation(req)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestGetPriceEstimation_DurationQtyFallbackAndZeroError(t *testing.T) {
	// DurationQty=0 and DurationHours=0 → duration_qty must be at least 1.
	c, _ := newStubClient(okResp("{}"), nil)
	req := validPriceReq()
	req.DurationHours = 0
	req.DurationQty = 0
	_, err := c.GetPriceEstimation(req)
	assert.ErrorContains(t, err, "duration_qty must be at least 1")
}

func TestGetPriceEstimation_HardwareQtyFallbackAndZeroError(t *testing.T) {
	// HardwareQty=0 and GPUsPerContainer=0 → hardware_qty must be at least 1.
	c, _ := newStubClient(okResp("{}"), nil)
	req := validPriceReq()
	req.HardwareQty = 0
	req.GPUsPerContainer = 0
	_, err := c.GetPriceEstimation(req)
	assert.ErrorContains(t, err, "hardware_qty must be at least 1")
}

func TestGetPriceEstimation_Success_HourlyDefault(t *testing.T) {
	body := `{"data":{"total_cost_usdc":100,"ionet_fee":10,"currency_conversion_fee":5}}`
	c, stub := newStubClient(okResp(body), nil)
	resp, err := c.GetPriceEstimation(validPriceReq())
	require.NoError(t, err)
	assert.Equal(t, float64(100), resp.EstimatedCost)
	assert.Equal(t, "USDC", resp.Currency) // default currency uppercased
	assert.True(t, resp.EstimationValid)
	assert.Equal(t, float64(85), resp.PriceBreakdown.ComputeCost) // 100-10-5
	assert.Equal(t, float64(100), resp.PriceBreakdown.TotalCost)
	// DurationHours=3 → hourlyRate = 100/3
	assert.InDelta(t, 100.0/3.0, resp.PriceBreakdown.HourlyRate, 1e-9)
	ep := requestPath(t, stub)
	assert.Contains(t, ep, "duration_type=hourly")
	assert.Contains(t, ep, "currency=usdc")
}

func TestGetPriceEstimation_DurationTypeSwitch(t *testing.T) {
	// Each branch sets duration_type and the hourly-rate divisor differently.
	cases := []struct {
		durType     string
		durQty      int
		wantAPIType string
		wantHours   float64 // divisor for hourly rate
	}{
		{"hour", 4, "hourly", 4},
		{"days", 2, "daily", 2 * 24},
		{"weekly", 1, "weekly", 1 * 24 * 7},
		{"month", 1, "monthly", 1 * 24 * 30},
		{"unknown", 6, "hourly", 6}, // default switch arm: falls back, keeps durationHours or qty
	}
	for _, tc := range cases {
		t.Run(tc.durType, func(t *testing.T) {
			c, stub := newStubClient(okResp(`{"data":{"total_cost_usdc":240}}`), nil)
			req := validPriceReq()
			req.DurationType = tc.durType
			req.DurationQty = tc.durQty
			req.DurationHours = 0 // force durationHoursForRate to derive from qty/switch
			resp, err := c.GetPriceEstimation(req)
			require.NoError(t, err)
			assert.Contains(t, requestPath(t, stub), "duration_type="+tc.wantAPIType)
			assert.InDelta(t, 240.0/tc.wantHours, resp.PriceBreakdown.HourlyRate, 1e-9)
		})
	}
}

func TestGetPriceEstimation_CustomCurrencyUppercased(t *testing.T) {
	c, _ := newStubClient(okResp(`{"data":{"total_cost_usdc":10}}`), nil)
	req := validPriceReq()
	req.Currency = " eur "
	resp, err := c.GetPriceEstimation(req)
	require.NoError(t, err)
	assert.Equal(t, "EUR", resp.Currency)
}

func TestGetPriceEstimation_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetPriceEstimation(validPriceReq())
	assert.ErrorContains(t, err, "failed to get price estimation")
}

func TestGetPriceEstimation_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.GetPriceEstimation(validPriceReq())
	assert.ErrorContains(t, err, "failed to parse price estimation response")
}

// ---------------------------------------------------------------------------
// CheckClusterNameAvailability
// ---------------------------------------------------------------------------

func TestCheckClusterNameAvailability_EmptyName(t *testing.T) {
	c, _ := newStubClient(okResp("true"), nil)
	_, err := c.CheckClusterNameAvailability("")
	assert.ErrorContains(t, err, "cluster name cannot be empty")
}

func TestCheckClusterNameAvailability_TrueAndFalse(t *testing.T) {
	c, stub := newStubClient(okResp("true"), nil)
	ok, err := c.CheckClusterNameAvailability("my-cluster")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Contains(t, requestPath(t, stub), "cluster_name=my-cluster")

	c2, _ := newStubClient(okResp("false"), nil)
	ok, err = c2.CheckClusterNameAvailability("taken")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCheckClusterNameAvailability_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.CheckClusterNameAvailability("x")
	assert.ErrorContains(t, err, "failed to check cluster name availability")
}

func TestCheckClusterNameAvailability_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("notabool"), nil)
	_, err := c.CheckClusterNameAvailability("x")
	assert.ErrorContains(t, err, "failed to parse cluster name availability response")
}

// ---------------------------------------------------------------------------
// UpdateClusterName
// ---------------------------------------------------------------------------

func TestUpdateClusterName_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.UpdateClusterName("", &UpdateClusterNameRequest{Name: "n"})
	assert.ErrorContains(t, err, "cluster ID cannot be empty")
	_, err = c.UpdateClusterName("cid", nil)
	assert.ErrorContains(t, err, "update cluster name request cannot be nil")
	_, err = c.UpdateClusterName("cid", &UpdateClusterNameRequest{Name: ""})
	assert.ErrorContains(t, err, "cluster name cannot be empty")
}

func TestUpdateClusterName_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"status":"ok","message":"renamed"}`), nil)
	resp, err := c.UpdateClusterName("cid", &UpdateClusterNameRequest{Name: "new-name"})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "renamed", resp.Message)
	assert.Equal(t, "PUT", stub.last.Method)
	assert.Equal(t, "/clusters/cid/update-name", requestPath(t, stub))
}

func TestUpdateClusterName_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.UpdateClusterName("cid", &UpdateClusterNameRequest{Name: "n"})
	assert.ErrorContains(t, err, "failed to update cluster name")
}

func TestUpdateClusterName_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("bad"), nil)
	_, err := c.UpdateClusterName("cid", &UpdateClusterNameRequest{Name: "n"})
	assert.ErrorContains(t, err, "failed to parse update cluster name response")
}
