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
import { updateSystemOptionGroup } from "@/features/system-settings/api";
import type { SystemOptionsResponse } from "@/features/system-settings/types";
import { api } from "@/lib/api";

import type {
  VeridropDetectionOptions,
  VeridropDetectionResultsResponse,
  VeridropDetectionResultsRequest,
  VeridropManualDetectionRequest,
  VeridropManualDetectionResponse,
  VeridropDetectionStartRequest,
  VeridropDetectionTaskResponse,
  VeridropDetectionTargetsResponse,
  VeridropDetectionCleanupRequest,
  VeridropDetectionBatchRequest,
  VeridropDetectionTargetsRequest,
} from "./types";

export const VERIDROP_OPTION_KEYS = {
  enabled: "veridrop_monitor_setting.enabled",
  base_url: "veridrop_monitor_setting.base_url",
  admin_api_key: "veridrop_monitor_setting.admin_api_key",
  default_mode: "veridrop_monitor_setting.default_mode",
  default_openai_wire_api: "veridrop_monitor_setting.default_openai_wire_api",
  include_long_context: "veridrop_monitor_setting.include_long_context",
  include_long_context_extreme:
    "veridrop_monitor_setting.include_long_context_extreme",
  max_concurrent: "veridrop_monitor_setting.max_concurrent",
  batch_size: "veridrop_monitor_setting.batch_size",
  submit_timeout_seconds: "veridrop_monitor_setting.submit_timeout_seconds",
  poll_interval_seconds: "veridrop_monitor_setting.poll_interval_seconds",
  job_timeout_seconds: "veridrop_monitor_setting.job_timeout_seconds",
  auto_detection_enabled: "veridrop_monitor_setting.auto_detection_enabled",
  detection_interval_minutes:
    "veridrop_monitor_setting.detection_interval_minutes",
} as const;

export const VERIDROP_DEFAULT_OPTIONS: VeridropDetectionOptions = {
  enabled: false,
  base_url: "",
  admin_api_key: "",
  default_mode: "quick",
  default_openai_wire_api: "chat_completions",
  include_long_context: false,
  include_long_context_extreme: false,
  max_concurrent: 2,
  batch_size: 100,
  submit_timeout_seconds: 30,
  poll_interval_seconds: 5,
  job_timeout_seconds: 300,
  auto_detection_enabled: false,
  detection_interval_minutes: 1440,
};

const booleanKeys = new Set<keyof VeridropDetectionOptions>([
  "enabled",
  "include_long_context",
  "include_long_context_extreme",
  "auto_detection_enabled",
]);

const numberKeys = new Set<keyof VeridropDetectionOptions>([
  "max_concurrent",
  "batch_size",
  "submit_timeout_seconds",
  "poll_interval_seconds",
  "job_timeout_seconds",
  "detection_interval_minutes",
]);

function parseOptionValue(
  key: keyof VeridropDetectionOptions,
  value: string,
): string | number | boolean {
  if (booleanKeys.has(key)) return value === "true" || value === "1";
  if (numberKeys.has(key)) {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : VERIDROP_DEFAULT_OPTIONS[key];
  }
  return value;
}

export function normalizeVeridropOptions(
  options: SystemOptionsResponse["data"] | undefined,
): VeridropDetectionOptions {
  const result = { ...VERIDROP_DEFAULT_OPTIONS };
  if (!Array.isArray(options)) return result;

  for (const option of options) {
    const entry = Object.entries(VERIDROP_OPTION_KEYS).find(
      ([, fullKey]) => fullKey === option.key,
    );
    if (!entry) continue;
    const localKey = entry[0] as keyof VeridropDetectionOptions;
    const writable = result as Record<
      keyof VeridropDetectionOptions,
      string | number | boolean
    >;
    writable[localKey] = parseOptionValue(localKey, option.value);
  }

  return result;
}

export async function getVeridropOptions() {
  const res = await api.get<SystemOptionsResponse>("/api/option/");
  return normalizeVeridropOptions(res.data.data);
}

export async function updateVeridropOptions(
  values: VeridropDetectionOptions,
  defaults: VeridropDetectionOptions,
) {
  const updates = Object.entries(values).filter(([key, value]) => {
    if (key === "admin_api_key" && String(value).trim() === "") return false;
    return value !== defaults[key as keyof VeridropDetectionOptions];
  });

  if (updates.length === 0) return 0;

  const res = await updateSystemOptionGroup({
    module: "veridrop_monitor_setting",
    values: Object.fromEntries(
      updates.map(([key, value]) => [key, String(value)]),
    ),
  });
  if (!res.success || res.data?.applied !== true) {
    throw new Error(res.message || "veridrop settings were not applied");
  }

  return updates.length;
}

export async function startEnabledVeridropDetection(
  request: VeridropDetectionStartRequest,
) {
  const res = await api.post<VeridropDetectionTaskResponse>(
    "/api/channel/veridrop/detect_enabled",
    request,
    { skipBusinessError: true, skipErrorHandler: true },
  );
  return res.data;
}

export async function startChannelVeridropDetection(channelId: number) {
  const res = await api.post<VeridropDetectionTaskResponse>(
    "/api/channel/veridrop/detect",
    { channel_id: channelId },
    { skipBusinessError: true, skipErrorHandler: true },
  );
  return res.data;
}

export async function startChannelsVeridropDetection(
  request: VeridropDetectionBatchRequest,
) {
  const res = await api.post<VeridropDetectionTaskResponse>(
    "/api/channel/veridrop/detect_batch",
    request,
    { skipBusinessError: true, skipErrorHandler: true },
  );
  return res.data;
}

export async function startManualVeridropDetection(
  request: VeridropManualDetectionRequest,
) {
  const res = await api.post<VeridropManualDetectionResponse>(
    "/api/channel/veridrop/detect_manual",
    request,
    { skipBusinessError: true, skipErrorHandler: true },
  );
  return res.data;
}

export async function fetchManualUpstreamModels(
  request: Pick<
    VeridropManualDetectionRequest,
    "base_url" | "api_key" | "protocol"
  >,
) {
  const channelType = {
    openai: 1,
    anthropic: 14,
    gemini: 24,
  }[request.protocol];
  if (channelType == null) throw new Error("unsupported protocol");

  const res = await api.post<{ success: boolean; data?: string[] }>(
    "/api/channel/fetch_models",
    {
      base_url: request.base_url.trim(),
      key: request.api_key.trim(),
      type: channelType,
    },
    { skipBusinessError: true, skipErrorHandler: true },
  );
  if (!res.data.success || !Array.isArray(res.data.data)) {
    throw new Error("model discovery failed");
  }

  return [
    ...new Set(res.data.data.map((model) => model.trim()).filter(Boolean)),
  ].sort((left, right) => left.localeCompare(right));
}

export async function listVeridropDetectionResults(
  request: VeridropDetectionResultsRequest = {},
) {
  const res = await api.get<VeridropDetectionResultsResponse>(
    "/api/channel/veridrop/results",
    {
      params: {
        limit: 100,
        ...request,
        outcomes: request.outcomes?.join(","),
      },
      disableDuplicate: true,
    },
  );
  return res.data;
}

export async function listVeridropDetectionTargets(
  request: VeridropDetectionTargetsRequest,
) {
  const res = await api.get<VeridropDetectionTargetsResponse>(
    "/api/channel/veridrop/targets",
    {
      params: request,
      disableDuplicate: true,
    },
  );
  return res.data;
}

export async function cleanupVeridropDetectionResults(
  request: VeridropDetectionCleanupRequest,
) {
  const res = await api.post<VeridropDetectionTaskResponse>(
    "/api/channel/veridrop/results/cleanup",
    request,
    { skipBusinessError: true, skipErrorHandler: true },
  );
  return res.data;
}
