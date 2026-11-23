package service

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type TwoFASetup struct {
	Secret      string   `json:"secret"`
	QRCodeData  string   `json:"qr_code_data"`
	BackupCodes []string `json:"backup_codes"`
	FlowToken   string   `json:"flow_token"`
	ExpiresAt   int64    `json:"expires_at"`
}

func StartTwoFASetup(identity AuthIdentity, authorization *model.AuthFlowAuthorization) (*TwoFASetup, error) {
	if err := ValidateFlowAuthorization(identity, VerificationOperation{Scope: VerificationScopeTwoFASetup}, authorization); err != nil {
		return nil, err
	}
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		return nil, err
	}
	key, err := common.GenerateTOTPSecret(user.Username)
	if err != nil {
		return nil, err
	}
	codes, err := common.GenerateBackupCodes()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(5 * time.Minute)
	flowToken, err := model.CreateTwoFAEnrollment(identity, authorization, key.Secret(), codes, expiresAt)
	if err != nil {
		return nil, err
	}
	return &TwoFASetup{Secret: key.Secret(), QRCodeData: common.GenerateQRCodeData(key.Secret(), user.Username), BackupCodes: codes, FlowToken: flowToken, ExpiresAt: expiresAt.Unix()}, nil
}

func FinishTwoFASetup(identity AuthIdentity, flowToken, code string) error {
	_, err := FinishTwoFASetupWithOutcome(identity, flowToken, code)
	return err
}

func FinishTwoFASetupWithOutcome(identity AuthIdentity, flowToken, code string) (int64, error) {
	flow, err := model.GetAuthFlow(flowToken, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeTwoFASetup, UserId: identity.UserID, SessionId: identity.SessionID,
	})
	if errors.Is(err, model.ErrAuthFlowInvalid) || errors.Is(err, model.ErrAuthFlowExpired) || errors.Is(err, model.ErrAuthFlowConsumed) {
		return 0, model.ErrTwoFASetupInvalid
	}
	if err != nil {
		return 0, err
	}
	var payload struct {
		Authorization *model.AuthFlowAuthorization `json:"authorization"`
	}
	if err := common.UnmarshalJsonStr(flow.Payload, &payload); err != nil {
		return 0, err
	}
	if err := ValidateFlowAuthorization(identity, VerificationOperation{Scope: VerificationScopeTwoFASetup}, payload.Authorization); err != nil {
		if errors.Is(err, model.ErrAuthFlowInvalid) {
			return 0, model.ErrTwoFASetupInvalid
		}
		return 0, err
	}
	return model.EnableTwoFAEnrollmentWithOutcome(identity, flowToken, code)
}
