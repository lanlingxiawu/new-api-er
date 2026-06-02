/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
// xiugai 添加号池节点功能
import { api } from '@/lib/api'
import type { NodeAccountsResponse, NodesResponse } from './types'

export async function getNodes(): Promise<NodesResponse> {
  const res = await api.get<NodesResponse>('/api/node-pool/nodes')
  return res.data
}

export async function getNodeAccounts(
  publicIp: string,
  nodeName: string
): Promise<NodeAccountsResponse> {
  const ip = encodeURIComponent(publicIp)
  const name = encodeURIComponent(nodeName)
  const res = await api.get<NodeAccountsResponse>(
    `/api/node-pool/nodes/${ip}/${name}/accounts`
  )
  return res.data
}

export async function deleteNode(
  publicIp: string,
  nodeName: string
): Promise<void> {
  const ip = encodeURIComponent(publicIp)
  const name = encodeURIComponent(nodeName)
  await api.delete(`/api/node-pool/nodes/${ip}/${name}`)
}
// end
