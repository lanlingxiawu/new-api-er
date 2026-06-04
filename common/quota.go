package common

func GetTrustQuota() int {
	return int(10 * QuotaPerUnit)
}

// QuotaToUSD 将 quota 单位转换为 USD 金额（保留 6 位精度）。
func QuotaToUSD(quota int64) float64 {
	if QuotaPerUnit == 0 {
		return 0
	}
	return float64(quota) / QuotaPerUnit
}
