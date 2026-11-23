package auditsecurity

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// This identifies repeated operations, not robots or trusted callers. All
// findings, including batch/source/IP rules, are still evaluated independently.
func repeatOperationKey(e model.SecurityEvent, finding model.SecurityFinding) string {
	if finding.Rule != "sensitive_operation" || finding.Severity != 2 || finding.Truncated || !e.Success || e.Count != 1 || e.ActorID <= 0 || e.TargetID <= 0 || e.ResourceType != "user" || e.At <= 0 || e.At > 1<<62 {
		return ""
	}
	if e.Action != "user.quota_add" && e.Action != "user.quota_subtract" {
		return ""
	}
	var facts Facts
	if common.UnmarshalJsonStr(e.Facts, &facts) != nil || facts.EvidenceIncomplete || facts.FollowupFailed || facts.TargetRole != common.RoleCommonUser || facts.From == nil || facts.To == nil || facts.Quota == nil || highRisk(e, facts) != 2 {
		return ""
	}
	identity := facts.Identity
	if identity == nil || identity.ActorID != e.ActorID || identity.Role != e.ActorRole || identity.AuthVersion <= 0 || identity.Version() != identity.AuthVersion || identity.AuthMethod != "access_token" || identity.CredentialRef != e.TokenRef || (identity.CredentialKind != "pat" && identity.CredentialKind != "legacy") {
		return ""
	}
	if e.ActorRole != common.RoleAdminUser && e.ActorRole != common.RoleRootUser {
		return ""
	}
	for _, digest := range []string{identity.CredentialRef, identity.CredentialGrant, facts.ClientRef} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 {
			return ""
		}
	}
	ip, err := netip.ParseAddr(e.IP)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
		return ""
	}
	from, to, quota := *facts.From, *facts.To, *facts.Quota
	// Valid wallet balances are bounded, so this subtraction cannot overflow.
	if from < -int64(common.MaxWalletQuota) || from > int64(common.MaxWalletQuota) || to < -int64(common.MaxWalletQuota) || to > int64(common.MaxWalletQuota) || quota <= 0 || quota > int64(common.MaxWalletQuota) {
		return ""
	}
	delta := to - from
	if e.Action == "user.quota_subtract" {
		delta = -delta
	}
	if delta != quota {
		return ""
	}
	parts := []string{
		"repeat-operation-v1", finding.Rule, strconv.Itoa(alertRuleVersion(finding.Rule, e)),
		e.SubjectRef, strconv.Itoa(e.ActorID), strconv.Itoa(e.ActorRole), strconv.FormatInt(identity.AuthVersion, 10),
		identity.CredentialKind, identity.CredentialRef, identity.CredentialGrant,
		e.Action, ip.Unmap().String(), facts.ClientRef, strconv.Itoa(facts.TargetRole), strconv.FormatInt(quota, 10),
		e.ResourceType, strconv.Itoa(e.Visibility),
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}
