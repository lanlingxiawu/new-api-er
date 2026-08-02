import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { persistTwoFALoginFlow } from './twofa-login'

describe('persistTwoFALoginFlow', () => {
  it('persists the backend flow token and expiry before the OTP redirect', () => {
    const calls: Array<[string, number]> = []
    const saved = persistTwoFALoginFlow(
      { require_2fa: true, flow_token: 'flow-1', expires_at: 1_800_000_000 },
      (token, expiresAt) => {
        calls.push([token, expiresAt])
        return true
      }
    )

    assert.equal(saved, true)
    assert.deepEqual(calls, [['flow-1', 1_800_000_000]])
  })

  it('rejects incomplete challenges without persisting them', () => {
    let calls = 0
    const persist = () => {
      calls += 1
      return true
    }

    assert.equal(
      persistTwoFALoginFlow({ require_2fa: true, flow_token: 'flow-1' }, persist),
      false
    )
    assert.equal(
      persistTwoFALoginFlow({ require_2fa: true, expires_at: 1_800_000_000 }, persist),
      false
    )
    assert.equal(calls, 0)
  })

  it('propagates storage rejection so the UI does not redirect with no flow', () => {
    assert.equal(
      persistTwoFALoginFlow(
        { require_2fa: true, flow_token: 'flow-1', expires_at: 1_800_000_000 },
        () => false
      ),
      false
    )
  })
})
