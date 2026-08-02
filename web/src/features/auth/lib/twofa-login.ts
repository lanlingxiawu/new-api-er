type PersistPendingFlow = (flowToken: string, expiresAt: number) => boolean
type TwoFALoginChallenge = {
  require_2fa?: boolean
  flow_token?: string
  expires_at?: number
}

export function persistTwoFALoginFlow(
  data: TwoFALoginChallenge | null | undefined,
  persist: PersistPendingFlow
): boolean {
  if (
    !data?.require_2fa ||
    typeof data.flow_token !== 'string' ||
    !data.flow_token.trim() ||
    typeof data.expires_at !== 'number' ||
    !Number.isFinite(data.expires_at)
  ) {
    return false
  }

  return persist(data.flow_token, data.expires_at)
}
