package controller

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var (
	errNotEmployee        = errors.New("current user is not an enabled employee")
	errNoPermission       = errors.New("no permission to operate this customer")
	errUserNotFound       = errors.New("user does not exist")
	errCustomerNotFound   = errors.New("customer profile does not exist")
	errCustomerDisabled   = errors.New("customer is disabled")
	errInvalidCustomer    = errors.New("customer must be an enabled common user")
	errInvalidEmployee    = errors.New("employee must be an enabled employee")
	errCannotBindSelf     = errors.New("employee and customer cannot be the same user")
	errCustomerIsEmployee = errors.New("customer cannot be an employee")
	errMutualInvitation   = errors.New("employee and customer cannot be mutual inviters")
	errInvalidCustomerId  = errors.New("invalid customer id")
	errInvalidEmployeeId  = errors.New("invalid employee id")
	errInvalidCustomerLog = errors.New("invalid customer quota log")
)

type UpdateCustomerRequest struct {
	Status int    `json:"status" binding:"required,min=1,max=2"`
	Remark string `json:"remark"`
}

type UpdateCustomerUserRequest struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Remark      string `json:"remark"`
}

type TransferQuotaRequest struct {
	Quota  int    `json:"quota" binding:"required,min=0"`
	Mode   string `json:"mode"`
	Remark string `json:"remark"`
}

type CustomerWithUser struct {
	*model.CustomerProfile
	Username            string `json:"username"`
	DisplayName         string `json:"display_name"`
	Email               string `json:"email"`
	Quota               int    `json:"quota"`
	UsedQuota           int    `json:"used_quota"`
	EmployeeUsername    string `json:"employee_username"`
	EmployeeDisplayName string `json:"employee_display_name"`
	CommissionQuota     int64  `json:"commission_quota"`
}

type CustomerQuotaLogWithUser struct {
	*model.CustomerQuotaLog
	EmployeeUsername    string `json:"employee_username"`
	EmployeeDisplayName string `json:"employee_display_name"`
	CustomerUsername    string `json:"customer_username"`
	CustomerDisplayName string `json:"customer_display_name"`
}

func normalizePage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

func buildCustomerWithUser(cp *model.CustomerProfile) CustomerWithUser {
	item := CustomerWithUser{CustomerProfile: cp}
	if cu, err := model.GetUserById(cp.CustomerUserId, false); err == nil && cu != nil {
		item.Username = cu.Username
		item.DisplayName = cu.DisplayName
		item.Email = cu.Email
		item.Quota = cu.Quota
		item.UsedQuota = cu.UsedQuota
	}
	if eu, err := model.GetUserById(cp.EmployeeUserId, false); err == nil && eu != nil {
		item.EmployeeUsername = eu.Username
		item.EmployeeDisplayName = eu.DisplayName
	}
	item.CommissionQuota = model.GetCustomerCommissionTotal(cp.EmployeeUserId, cp.CustomerUserId)
	return item
}

func buildInvitedCustomerWithUser(employeeUserId int, customer *model.User) CustomerWithUser {
	item := CustomerWithUser{
		CustomerProfile: &model.CustomerProfile{
			Id:             customer.Id,
			EmployeeUserId: employeeUserId,
			CustomerUserId: customer.Id,
			Status:         customer.Status,
			Remark:         customer.Remark,
			CreatedAt:      customer.CreatedAt,
		},
		Username:    customer.Username,
		DisplayName: customer.DisplayName,
		Email:       customer.Email,
		Quota:       customer.Quota,
		UsedQuota:   customer.UsedQuota,
	}
	item.CommissionQuota = model.GetCustomerCommissionTotal(employeeUserId, customer.Id)
	return item
}

func buildInvitedCustomersWithUser(employeeUserId int, customers []*model.User) ([]CustomerWithUser, error) {
	customerIds := make([]int, 0, len(customers))
	for _, customer := range customers {
		customerIds = append(customerIds, customer.Id)
	}
	commissionTotals, err := model.GetCustomerCommissionTotals(employeeUserId, customerIds)
	if err != nil {
		return nil, err
	}
	items := make([]CustomerWithUser, 0, len(customers))
	for _, customer := range customers {
		item := CustomerWithUser{
			CustomerProfile: &model.CustomerProfile{
				Id:             customer.Id,
				EmployeeUserId: employeeUserId,
				CustomerUserId: customer.Id,
				Status:         customer.Status,
				Remark:         customer.Remark,
				CreatedAt:      customer.CreatedAt,
			},
			Username:        customer.Username,
			DisplayName:     customer.DisplayName,
			Email:           customer.Email,
			Quota:           customer.Quota,
			UsedQuota:       customer.UsedQuota,
			CommissionQuota: commissionTotals[customer.Id],
		}
		items = append(items, item)
	}
	return items, nil
}

func buildCustomerQuotaLogWithUser(log *model.CustomerQuotaLog) CustomerQuotaLogWithUser {
	item := CustomerQuotaLogWithUser{CustomerQuotaLog: log}
	if eu, err := model.GetUserById(log.EmployeeUserId, false); err == nil && eu != nil {
		item.EmployeeUsername = eu.Username
		item.EmployeeDisplayName = eu.DisplayName
	}
	if cu, err := model.GetUserById(log.CustomerUserId, false); err == nil && cu != nil {
		item.CustomerUsername = cu.Username
		item.CustomerDisplayName = cu.DisplayName
	}
	return item
}

func buildCustomerQuotaLogsWithUser(logs []*model.CustomerQuotaLog) []CustomerQuotaLogWithUser {
	items := make([]CustomerQuotaLogWithUser, 0, len(logs))
	for _, log := range logs {
		items = append(items, buildCustomerQuotaLogWithUser(log))
	}
	return items
}

func logBlockedMutualInvitation(action string, employeeUserId, customerUserId, operatedBy int) {
	adminInfo := map[string]interface{}{
		"action":           action,
		"reason":           "mutual_invitation",
		"employee_user_id": employeeUserId,
		"customer_user_id": customerUserId,
		"operated_by":      operatedBy,
	}
	model.RecordLogWithAdminInfo(
		customerUserId,
		model.LogTypeManage,
		"blocked employee customer binding because employee and customer are mutual inviters",
		adminInfo,
	)
	common.SysLog(fmt.Sprintf(
		"employee_customer: blocked mutual invitation action=%s employee_user_id=%d customer_user_id=%d operated_by=%d",
		action,
		employeeUserId,
		customerUserId,
		operatedBy,
	))
}

func validateCustomerBinding(employeeUserId, customerUserId int) error {
	if employeeUserId <= 0 {
		return errInvalidEmployeeId
	}
	if customerUserId <= 0 {
		return errInvalidCustomerId
	}
	if employeeUserId == customerUserId {
		return errCannotBindSelf
	}
	if !model.IsEmployee(employeeUserId) {
		return errInvalidEmployee
	}

	customerUser, err := model.GetUserById(customerUserId, false)
	if err != nil || customerUser == nil {
		return errUserNotFound
	}
	if customerUser.Role != common.RoleCommonUser || customerUser.Status != common.UserStatusEnabled {
		return errInvalidCustomer
	}
	if model.HasEmployeeProfile(customerUserId) {
		return errCustomerIsEmployee
	}
	mutual, err := model.IsMutualInvitation(customerUserId, employeeUserId)
	if err != nil {
		return err
	}
	if mutual {
		return errMutualInvitation
	}
	return nil
}

func getOwnedCustomerProfile(c *gin.Context, employeeUserId int) (*model.CustomerProfile, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}

	cp, err := model.GetCustomerProfileById(id)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if cp.EmployeeUserId != employeeUserId {
		common.ApiError(c, errNoPermission)
		return nil, false
	}
	return cp, true
}

func getOwnedInvitedCustomer(c *gin.Context, employeeUserId int, selectAll bool) (*model.User, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}

	customer, err := model.GetInvitedCustomerByEmployee(employeeUserId, id, selectAll)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiError(c, errNoPermission)
			return nil, false
		}
		common.ApiError(c, err)
		return nil, false
	}
	return customer, true
}

func EmployeeCreateCustomer(c *gin.Context) {
	employeeUserId := c.GetInt("id")
	if !model.IsEmployee(employeeUserId) {
		common.ApiError(c, errNotEmployee)
		return
	}

	var req struct {
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Remark      string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	newUser := &model.User{
		Username:    strings.TrimSpace(req.Username),
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		InviterId:   employeeUserId,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Remark:      req.Remark,
	}
	if newUser.DisplayName == "" {
		newUser.DisplayName = newUser.Username
	}
	if err := newUser.Insert(employeeUserId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildInvitedCustomerWithUser(employeeUserId, newUser))
}

func EmployeeListCustomers(c *gin.Context) {
	employeeUserId := c.GetInt("id")
	if !model.IsEmployee(employeeUserId) {
		common.ApiError(c, errNotEmployee)
		return
	}

	page, pageSize := normalizePage(c)
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	status, _ := strconv.Atoi(c.Query("status"))
	keyword := strings.TrimSpace(c.Query("keyword"))
	customers, total, err := model.GetInvitedCustomersByEmployeeWithFilter(model.InvitedCustomerFilter{
		EmployeeUserId: employeeUserId,
		CustomerUserId: customerUserId,
		Keyword:        keyword,
		Status:         status,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items, err := buildInvitedCustomersWithUser(employeeUserId, customers)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func EmployeeGetCustomer(c *gin.Context) {
	employeeUserId := c.GetInt("id")
	if !model.IsEmployee(employeeUserId) {
		common.ApiError(c, errNotEmployee)
		return
	}

	customer, ok := getOwnedInvitedCustomer(c, employeeUserId, false)
	if !ok {
		return
	}
	common.ApiSuccess(c, buildInvitedCustomerWithUser(employeeUserId, customer))
}

func EmployeeUpdateCustomer(c *gin.Context) {
	employeeUserId := c.GetInt("id")
	if !model.IsEmployee(employeeUserId) {
		common.ApiError(c, errNotEmployee)
		return
	}

	customer, ok := getOwnedInvitedCustomer(c, employeeUserId, true)
	if !ok {
		return
	}

	var req struct {
		Remark string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	customer.Remark = req.Remark
	if err := customer.Update(false); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

/*
func EmployeeUpdateCustomerUser(c *gin.Context) {
	employeeUserId := c.GetInt("id")
	if !model.IsEmployee(employeeUserId) {
		common.ApiError(c, errNotEmployee)
		return
	}

	cp, ok := getOwnedCustomerProfile(c, employeeUserId)
	if !ok {
		return
	}

	var req UpdateCustomerUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	custUser, err := model.GetUserById(cp.CustomerUserId, true)
	if err != nil || custUser == nil {
		common.ApiError(c, errUserNotFound)
		return
	}
	if req.DisplayName != "" {
		custUser.DisplayName = req.DisplayName
	}
	if req.Email != "" {
		custUser.Email = req.Email
	}
	if req.Password != "" {
		custUser.Password = req.Password
	}
	if req.Remark != "" {
		custUser.Remark = req.Remark
	}
	updatePassword := req.Password != ""
	if err := custUser.Update(updatePassword); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
*/

/*
func EmployeeTransferQuota(c *gin.Context) {
	employeeUserId := c.GetInt("id")
	if !model.IsEmployee(employeeUserId) {
		common.ApiError(c, errNotEmployee)
		return
	}

	cp, ok := getOwnedCustomerProfile(c, employeeUserId)
	if !ok {
		return
	}
	if cp.Status != model.CustomerStatusEnabled {
		common.ApiError(c, errCustomerDisabled)
		return
	}

	var req TransferQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = model.QuotaAdjustModeAdd
	}
	logEntry, err := model.AdjustCustomerQuota(employeeUserId, cp.CustomerUserId, req.Quota, mode, req.Remark)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildCustomerQuotaLogWithUser(logEntry))
}
*/

func EmployeeListQuotaLogs(c *gin.Context) {
	employeeUserId := c.GetInt("id")
	if !model.IsEmployee(employeeUserId) {
		common.ApiError(c, errNotEmployee)
		return
	}

	page, pageSize := normalizePage(c)
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	logs, total, err := model.GetCustomerQuotaLogs(model.CustomerQuotaLogFilter{
		EmployeeUserId: employeeUserId,
		CustomerUserId: customerUserId,
		StartTime:      timeRange.StartTime,
		EndTime:        timeRange.EndTime,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     buildCustomerQuotaLogsWithUser(logs),
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func AdminListCustomers(c *gin.Context) {
	page, pageSize := normalizePage(c)
	employeeUserId, _ := strconv.Atoi(c.Query("employee_user_id"))

	customers, total, err := model.GetAllCustomers(page, pageSize, employeeUserId)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items := make([]CustomerWithUser, 0, len(customers))
	for _, cp := range customers {
		items = append(items, buildCustomerWithUser(cp))
	}

	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func AdminCreateCustomer(c *gin.Context) {
	var req struct {
		EmployeeUserId int    `json:"employee_user_id" binding:"required"`
		CustomerUserId int    `json:"customer_user_id" binding:"required"`
		Remark         string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateCustomerBinding(req.EmployeeUserId, req.CustomerUserId); err != nil {
		if errors.Is(err, errMutualInvitation) {
			logBlockedMutualInvitation("admin_create_customer", req.EmployeeUserId, req.CustomerUserId, c.GetInt("id"))
		}
		common.ApiError(c, err)
		return
	}

	cp := &model.CustomerProfile{
		EmployeeUserId: req.EmployeeUserId,
		CustomerUserId: req.CustomerUserId,
		Status:         model.CustomerStatusEnabled,
		Remark:         req.Remark,
	}
	if err := model.UpdateUserInviterId(req.CustomerUserId, req.EmployeeUserId); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateCustomerProfile(cp); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildCustomerWithUser(cp))
}

func AdminGetCustomer(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cp, err := model.GetCustomerProfileById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildCustomerWithUser(cp))
}

func AdminUpdateCustomer(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req UpdateCustomerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	cp := &model.CustomerProfile{
		Id:     id,
		Status: req.Status,
		Remark: req.Remark,
	}
	if err := model.UpdateCustomerProfile(cp); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiError(c, errCustomerNotFound)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminUpdateCustomerUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cp, err := model.GetCustomerProfileById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	var req UpdateCustomerUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	custUser, err := model.GetUserById(cp.CustomerUserId, true)
	if err != nil || custUser == nil {
		common.ApiError(c, errUserNotFound)
		return
	}
	if req.DisplayName != "" {
		custUser.DisplayName = req.DisplayName
	}
	if req.Email != "" {
		custUser.Email = req.Email
	}
	if req.Remark != "" {
		custUser.Remark = req.Remark
	}
	if err := custUser.Update(false); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminDeleteCustomer(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if id <= 0 {
		common.ApiError(c, errInvalidCustomerId)
		return
	}
	if err := model.DeleteCustomerProfile(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminListCustomerQuotaLogs(c *gin.Context) {
	page, pageSize := normalizePage(c)
	employeeUserId, _ := strconv.Atoi(c.Query("employee_user_id"))
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	logs, total, err := model.GetCustomerQuotaLogs(model.CustomerQuotaLogFilter{
		EmployeeUserId: employeeUserId,
		CustomerUserId: customerUserId,
		StartTime:      timeRange.StartTime,
		EndTime:        timeRange.EndTime,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		common.ApiError(c, errInvalidCustomerLog)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     buildCustomerQuotaLogsWithUser(logs),
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}
