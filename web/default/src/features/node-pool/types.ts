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
export type NodeStatus = 'online' | 'offline'

export type Node = {
  public_ip: string
  internal_ip: string
  node_name: string
  listen_port: number
  cpu_usage: number
  mem_usage: number
  upload_bandwidth: number
  download_bandwidth: number
  today_requests: number
  total_requests: number
  today_consumption: number
  total_consumption: number
  total_rpm: number
  current_rpm: number
  avg_response_time: number
  status: NodeStatus
  last_seen: string
}

export type NodeAccount = {
  id: string
  name: string
  status: string
  req: number
  tokens: number
  account_cost: number
  user_cost: number
  total_capacity: number
  used_capacity: number
}

export type NodesResponse = {
  nodes: Node[]
}

export type NodeAccountsResponse = {
  public_ip: string
  node_name: string
  accounts: NodeAccount[]
  updated_at: string
}

export type NodeStats = {
  total: number
  online: number
  offline: number
  todayConsumption: number
  totalConsumption: number
  totalRpm: number
}
// end
