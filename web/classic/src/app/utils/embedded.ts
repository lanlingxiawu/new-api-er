export type EmbeddedHistoryRetentionMode = "turns" | "days";

const EMBEDDED_MODEL_READY_EVENT = "new-api:embedded-model-ready";
const EMBEDDED_RUNTIME_CONFIG_EVENT = "new-api:embedded-runtime-config";
const EMBEDDED_MODEL_READY_KEY = "__NEW_API_EMBEDDED_MODEL_READY__";
const EMBEDDED_RUNTIME_CONFIG_KEY = "__NEW_API_NEXTCHAT_RUNTIME__";
const EMBEDDED_INITIAL_PROMPT_KEY = "__NEW_API_NEXTCHAT_INITIAL_PROMPT__";

export type EmbeddedRuntimeConfig = {
  enabled?: boolean;
  apiBase?: string;
  apiKey?: string;
  userId?: number;
  tokenId?: string;
};

export function getEmbeddedRuntimeConfig(): EmbeddedRuntimeConfig {
  if (typeof window === "undefined") return {};
  return ((window as any)[EMBEDDED_RUNTIME_CONFIG_KEY] || {}) as EmbeddedRuntimeConfig;
}

export function setEmbeddedRuntimeConfig(config: EmbeddedRuntimeConfig) {
  if (typeof window === "undefined") return;
  (window as any)[EMBEDDED_RUNTIME_CONFIG_KEY] = {
    ...getEmbeddedRuntimeConfig(),
    ...config,
    apiBase: (config.apiBase || getEmbeddedRuntimeConfig().apiBase || "").replace(/\/$/, ""),
  };
  window.dispatchEvent(
    new CustomEvent(EMBEDDED_RUNTIME_CONFIG_EVENT, {
      detail: getEmbeddedRuntimeConfig(),
    }),
  );
}

export function setEmbeddedInitialPrompt(prompt: string) {
  if (typeof window === "undefined") return;
  (window as any)[EMBEDDED_INITIAL_PROMPT_KEY] = prompt.trim();
}

export function takeEmbeddedInitialPrompt() {
  if (typeof window === "undefined") return "";
  const prompt = String((window as any)[EMBEDDED_INITIAL_PROMPT_KEY] || "").trim();
  delete (window as any)[EMBEDDED_INITIAL_PROMPT_KEY];
  return prompt;
}

export function isEmbeddedRuntime() {
  const runtime = getEmbeddedRuntimeConfig();
  return Boolean(
    runtime.enabled ||
      runtime.apiKey ||
      runtime.apiBase ||
      runtime.userId ||
      runtime.tokenId,
  );
}

export function isEmbeddedModelReady() {
  if (!isEmbeddedRuntime()) return true;
  if (typeof window === "undefined") return false;
  return Boolean((window as any)[EMBEDDED_MODEL_READY_KEY]);
}

export function setEmbeddedModelReady(ready: boolean) {
  if (typeof window === "undefined") return;
  (window as any)[EMBEDDED_MODEL_READY_KEY] = ready;
  window.dispatchEvent(
    new CustomEvent(EMBEDDED_MODEL_READY_EVENT, { detail: { ready } }),
  );
}

export function onEmbeddedModelReadyChange(listener: () => void) {
  if (typeof window === "undefined") return () => {};
  window.addEventListener(EMBEDDED_MODEL_READY_EVENT, listener);
  return () => window.removeEventListener(EMBEDDED_MODEL_READY_EVENT, listener);
}

export function onEmbeddedRuntimeConfigChange(listener: () => void) {
  if (typeof window === "undefined") return () => {};
  window.addEventListener(EMBEDDED_RUNTIME_CONFIG_EVENT, listener);
  return () => window.removeEventListener(EMBEDDED_RUNTIME_CONFIG_EVENT, listener);
}

export function getEmbeddedUserId() {
  const runtimeUserId = Number(getEmbeddedRuntimeConfig().userId || 0);
  if (Number.isInteger(runtimeUserId) && runtimeUserId > 0) {
    return runtimeUserId;
  }

  return 0;
}

export function getEmbeddedApiKey() {
  const apiKey = getEmbeddedRuntimeConfig().apiKey;
  if (apiKey) return apiKey;

  return "";
}

export function getEmbeddedApiBase() {
  const apiBase = getEmbeddedRuntimeConfig().apiBase;
  if (apiBase) return apiBase.replace(/\/$/, "");

  return "";
}

export function getEmbeddedTokenId() {
  return String(getEmbeddedRuntimeConfig().tokenId || "");
}

export function normalizeEmbeddedRetentionMode(
  mode?: string | null,
): EmbeddedHistoryRetentionMode {
  return mode === "days" ? "days" : "turns";
}

export function normalizeEmbeddedPositiveInt(
  value: unknown,
  fallback: number,
) {
  const nextValue = Number(value);
  return Number.isFinite(nextValue) && nextValue > 0
    ? Math.floor(nextValue)
    : fallback;
}

export function parseEmbeddedBoolean(value: unknown) {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    return value === "true" || value === "1";
  }
  return false;
}
