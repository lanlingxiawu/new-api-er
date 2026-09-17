// Third-party Seedance 2.0 video service (channel type ThirdPartySD2).
// Upstream: POST {base}/v1/video/generate -> {task:{id}}; GET {base}/v1/video/tasks/{id} -> {task:{status, outputs, usage, error}}.

const RESOLUTION_RANK = { "480p": 1, "720p": 2, "1080p": 3, "4k": 4 };
const STANDARD_MODEL = "dreamina-seedance-2-0-260128";
const FAST_MODEL = "dreamina-seedance-2-0-fast-260128";
// Submit-time token reservation; completion overlays the upstream usage tokens.
const ESTIMATED_TOKENS = 1000000;

const VIDEO_INPUT_SCHEMA = {
  enum: ["none", "video"],
  enumLabels: { none: { en: "No reference video", zh: "无参考视频" }, video: { en: "With reference video", zh: "有参考视频" } },
  description: { en: "Reference video input", zh: "参考视频输入" },
};

const TOKENS_SCHEMA = {
  type: "number",
  unit: "token",
  description: { en: "Billing token unit price", zh: "计费 Token 单价" },
};

function resolutionSchema(values) {
  const labels = {};
  for (const value of values) labels[value] = { en: value, zh: value };
  return { enum: values, enumLabels: labels, description: { en: "Output video resolution", zh: "输出视频分辨率" } };
}

function usageExamples(values) {
  const examples = [];
  for (const value of values) {
    examples.push({ label: value + " · none", facts: { tokens: ESTIMATED_TOKENS, output_resolution: value, video_input: "none" } });
    examples.push({ label: value + " · video", facts: { tokens: ESTIMATED_TOKENS, output_resolution: value, video_input: "video" } });
  }
  return examples;
}

// Output resolutions the plugin recognizes. Which of them a model accepts is
// decided by the host from the admin pricing matrix (thirdpartysd2_pricing), so
// a tier added to the matrix is accepted without a plugin change.
const RESOLUTIONS = ["480p", "720p", "1080p", "4k"];
// Resolutions of the default pricing matrix, used for display examples only.
const DEFAULT_MODEL_RESOLUTIONS = {
  [STANDARD_MODEL]: ["480p", "720p", "1080p", "4k"],
  [FAST_MODEL]: ["480p", "720p"],
};
// Hosts besides the channel base URL host whose task outputs require the
// channel key. The key is sent only to the channel host and these hosts;
// outputs on any other host are fetched without credentials. Deployments whose
// provider serves authenticated downloads on another host list it here in an
// override plugin (entries are host or host:port).
const CREDENTIAL_HOSTS = [];

export const meta = {
  apiVersion: 1,
  key: "thirdpartysd2",
  name: "Third-party Seedance 2.0",
  icon: "ByteDance.Color",
  description: {
    en: "Seedance 2.0 video generation through a third-party service (text-to-video, image-to-video, and video-to-video)",
    zh: "通过第三方服务调用 Seedance 2.0 视频生成（文生视频、图生视频、视频生视频）",
  },
  version: "1.0.0",
  author: { name: "NEXAXIS" },
  channelTypes: [58],
  allowedHosts: CREDENTIAL_HOSTS,
  models: [STANDARD_MODEL, FAST_MODEL],
  fetchMode: "per_task",
  usageSchema: {
    // Upstream billing tokens (reserved at submit, actual usage on completion).
    tokens: TOKENS_SCHEMA,
    // Highest resolution declared by the request, normalized to a pricing tier.
    output_resolution: resolutionSchema(RESOLUTIONS),
    // Whether metadata.content carries a reference video.
    video_input: VIDEO_INPUT_SCHEMA,
  },
  usageExamples: usageExamples(DEFAULT_MODEL_RESOLUTIONS[STANDARD_MODEL]),
  usageProfiles: [
    {
      models: [FAST_MODEL],
      schema: { tokens: TOKENS_SCHEMA, output_resolution: resolutionSchema(RESOLUTIONS), video_input: VIDEO_INPUT_SCHEMA },
      examples: usageExamples(DEFAULT_MODEL_RESOLUTIONS[FAST_MODEL]),
    },
  ],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

function trimmed(value) {
  return String(value == null ? "" : value).trim();
}

function isObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function trimBase(url) {
  return String(url || "").replace(/\/+$/, "");
}

function classifyHeight(height) {
  if (height >= 2160) return "4k";
  if (height >= 1080) return "1080p";
  if (height >= 720) return "720p";
  if (height > 0) return "480p";
  return "";
}

function normalizeResolution(raw) {
  const value = trimmed(raw).toLowerCase().replace(/ /g, "");
  if (RESOLUTION_RANK[value]) return value;
  if (value === "2160p") return "4k";
  if (/^\d+p$/.test(value)) return classifyHeight(Number.parseInt(value, 10));
  const match = /^(\d+)x(\d+)$/.exec(value);
  if (!match) return "";
  const width = Number.parseInt(match[1], 10);
  const height = Number.parseInt(match[2], 10);
  if (width <= 0 || height <= 0) return "";
  return classifyHeight(Math.min(width, height));
}

// The larger of metadata.resolution and size wins so a request cannot be priced
// below the resolution it actually asks for.
function requestResolution(req) {
  const metadata = isObject(req.metadata) ? req.metadata : {};
  let best = "";
  for (const value of [metadata.resolution, req.size]) {
    const normalized = typeof value === "string" ? normalizeResolution(value) : "";
    if (normalized && (!best || RESOLUTION_RANK[normalized] > RESOLUTION_RANK[best])) best = normalized;
  }
  return best;
}

function hasVideoInput(req) {
  const metadata = isObject(req.metadata) ? req.metadata : {};
  if (!Array.isArray(metadata.content)) return false;
  return metadata.content.some(function (item) {
    return isObject(item) && (item.type === "video_url" || Object.prototype.hasOwnProperty.call(item, "video_url"));
  });
}

function modelOf(ctx, req) {
  return trimmed(ctx.upstreamModel) || trimmed(ctx.model) || trimmed(req.model);
}

function mediaURL(value, field) {
  if (value === undefined || value === null) return undefined;
  if (!isObject(value)) throw new Error("metadata.content " + field + " must be an object");
  return value.url === undefined ? {} : { url: String(value.url) };
}

function contentItem(item) {
  if (!isObject(item)) throw new Error("metadata.content items must be objects");
  const result = {};
  for (const key of ["type", "text", "role"]) {
    if (item[key] !== undefined && item[key] !== null && item[key] !== "") result[key] = String(item[key]);
  }
  for (const key of ["image_url", "video_url", "audio_url"]) {
    const media = mediaURL(item[key], key);
    if (media !== undefined) result[key] = media;
  }
  return result;
}

function intValue(value, field) {
  if (typeof value === "number" && Number.isFinite(value)) return Math.trunc(value);
  if (typeof value === "string" && /^[-+]?\d+$/.test(value.trim())) return Number.parseInt(value, 10);
  throw new Error("metadata." + field + " must be an integer");
}

function boolValue(value, field) {
  if (typeof value === "boolean") return value;
  if (value === "true") return true;
  if (value === "false") return false;
  throw new Error("metadata." + field + " must be a boolean");
}

const STRING_FIELDS = ["callback_url", "service_tier", "resolution", "ratio"];
const INT_FIELDS = ["execution_expires_after", "duration", "frames", "seed"];
const BOOL_FIELDS = ["return_last_frame", "generate_audio", "draft", "camera_fixed", "watermark"];

function requestDuration(req) {
  const seconds = Number.parseInt(trimmed(req.seconds), 10);
  if (seconds > 0) return seconds;
  const duration = Number(req.duration);
  return Number.isFinite(duration) && duration > 0 ? Math.trunc(duration) : 0;
}

export function buildSubmitRequest(ctx) {
  const req = ctx.requestBody || {};
  if (!trimmed(req.prompt)) throw new Error("prompt is required");
  if (req.metadata !== undefined && req.metadata !== null && !isObject(req.metadata)) throw new Error("metadata must be an object");
  const model = modelOf(ctx, req);
  if (!model) throw new Error("model is required");
  const resolution = requestResolution(req);
  if (!resolution) throw new Error("a recognizable resolution is required (metadata.resolution or size, e.g. 720p or 1280x720)");

  const body = { model: model, content: [] };
  const images = Array.isArray(req.images) ? req.images : [];
  for (const url of images) body.content.push({ type: "image_url", image_url: { url: String(url) } });

  const metadata = isObject(req.metadata) ? req.metadata : {};
  if (metadata.content !== undefined && metadata.content !== null) {
    if (!Array.isArray(metadata.content)) throw new Error("metadata.content must be an array");
    body.content = metadata.content.map(contentItem);
  }
  for (const key of STRING_FIELDS) {
    if (metadata[key] !== undefined && metadata[key] !== null && metadata[key] !== "") body[key] = String(metadata[key]);
  }
  for (const key of INT_FIELDS) {
    if (metadata[key] !== undefined && metadata[key] !== null) body[key] = intValue(metadata[key], key);
  }
  for (const key of BOOL_FIELDS) {
    if (metadata[key] !== undefined && metadata[key] !== null) body[key] = boolValue(metadata[key], key);
  }
  if (Array.isArray(metadata.tools)) {
    body.tools = metadata.tools.map(function (tool) {
      return isObject(tool) && tool.type ? { type: String(tool.type) } : {};
    });
  }
  body.resolution = resolution;
  const duration = requestDuration(req);
  if (duration > 0) body.duration = duration;

  body.content = body.content.filter(function (item) {
    return item.type !== "text";
  });
  const hasReference = body.content.length > 0;
  body.content.push({ type: "text", text: String(req.prompt) });
  return {
    url: trimBase(ctx.baseUrl) + "/v1/video/generate",
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: body,
    action: hasReference ? "image_to_video" : "text_to_video",
  };
}

export function parseSubmitResponse(ctx, resp) {
  const body = isObject(resp.body) ? resp.body : {};
  if (!isObject(body.task) || !trimmed(body.task.id)) throw new Error("task_id is empty");
  return { taskId: trimmed(body.task.id), taskData: body };
}

export function extractUsage(ctx) {
  const req = ctx.requestBody || {};
  return {
    tokens: ESTIMATED_TOKENS,
    output_resolution: requestResolution(req) || "720p",
    video_input: hasVideoInput(req) ? "video" : "none",
  };
}

export function buildQueryRequest(ctx) {
  return {
    url: trimBase(ctx.baseUrl) + "/v1/video/tasks/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Accept: "application/json", "Content-Type": "application/json", Authorization: "Bearer " + ctx.apiKey },
  };
}

// task.error is either a plain string or {code, message}.
function errorReason(error) {
  if (typeof error === "string") return trimmed(error);
  if (!isObject(error)) return "";
  return trimmed(error.message) || trimmed(error.code);
}

function firstOutput(task) {
  const outputs = isObject(task) && Array.isArray(task.outputs) ? task.outputs : [];
  for (const output of outputs) {
    if (typeof output === "string" && trimmed(output)) return trimmed(output);
  }
  return "";
}

function positiveTokens(value) {
  const number = Number(value);
  return Number.isFinite(number) && number > 0 ? number : 0;
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || !isObject(body.task)) throw new Error("task is empty");
  const task = body.task;
  const status = trimmed(task.status);
  if (status === "pending" || status === "queued") return { status: "QUEUED", progress: "10%" };
  if (status === "processing" || status === "running" || status === "in_progress") return { status: "IN_PROGRESS", progress: "50%" };
  if (status === "completed" || status === "succeeded" || status === "success") {
    const result = { status: "SUCCESS", progress: "100%" };
    // The output URL needs the channel credential; it stays private and the
    // host exposes it only through the authenticated content proxy.
    const url = firstOutput(task);
    if (url) result.url = url;
    const usage = isObject(task.usage) ? task.usage : {};
    const completionTokens = positiveTokens(usage.completion_tokens);
    const totalTokens = positiveTokens(usage.total_tokens);
    if (completionTokens) result.completionTokens = completionTokens;
    if (totalTokens) result.totalTokens = totalTokens;
    return result;
  }
  if (status === "failed" || status === "error" || status === "cancelled") {
    return { status: "FAILURE", progress: "100%", reason: errorReason(task.error) || "task failed" };
  }
  // The service does not publish its full status vocabulary. Unlisted states
  // stay in progress; the host's task timeout still bounds the task.
  return { status: "IN_PROGRESS", progress: "30%" };
}

export function extractUsageOnComplete(_task, _taskResult, body) {
  if (!isObject(body) || !isObject(body.task)) return {};
  const status = trimmed(body.task.status);
  if (status !== "completed" && status !== "succeeded" && status !== "success") return {};
  const usage = isObject(body.task.usage) ? body.task.usage : {};
  const tokens = positiveTokens(usage.total_tokens) || positiveTokens(usage.completion_tokens);
  return tokens ? { tokens: tokens } : {};
}

export function listArtifacts(task) {
  if (task.status !== "SUCCESS" || !isObject(task.data) || !firstOutput(task.data.task)) return [];
  return [{ key: "video", type: "video", mimeType: "video/mp4" }];
}

// Lowercased host without userinfo and without the scheme's default port, as
// the host compares request URLs against the channel host and allowedHosts.
function canonicalHost(url) {
  const match = /^(https?):\/\/([^/?#]+)/i.exec(trimmed(url));
  if (!match) return "";
  const scheme = match[1].toLowerCase();
  const authority = match[2].toLowerCase();
  const host = authority.slice(authority.lastIndexOf("@") + 1);
  if ((scheme === "https" && host.endsWith(":443")) || (scheme === "http" && host.endsWith(":80"))) return host.replace(/:\d+$/, "");
  return host;
}

function sendsCredential(url, baseUrl) {
  const host = canonicalHost(url);
  if (!host) return false;
  if (host === canonicalHost(baseUrl)) return true;
  const scheme = /^https:/i.test(trimmed(url)) ? "https" : "http";
  return CREDENTIAL_HOSTS.some(function (entry) {
    return canonicalHost(scheme + "://" + trimmed(entry)) === host;
  });
}

export function buildContentRequest(ctx) {
  const url = ctx.artifactKey === "video" && isObject(ctx.data) ? firstOutput(ctx.data.task) : "";
  if (!url) throw new Error("artifact_not_found");
  // Outputs on the channel host or a declared credential host require the
  // channel bearer key. Outputs on any other host are fetched without
  // credentials so the key never leaves the configured upstream.
  if (sendsCredential(url, ctx.baseUrl)) {
    return { url: url, method: ctx.clientRequest.method, headers: { Authorization: "Bearer " + ctx.apiKey } };
  }
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

function responsesInput(req) {
  const texts = [],
    images = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!isObject(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!isObject(part)) continue;
        if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
        if (["input_image", "image_url"].includes(part.type)) {
          let image = part.image_url;
          if (isObject(image)) image = image.url;
          if (trimmed(image)) images.push(trimmed(image));
        }
      }
    }
  }
  return {
    prompt: texts
      .filter(function (text) {
        return trimmed(text);
      })
      .join("\n"),
    images: images,
  };
}

function responsesVideoText(ctx) {
  const artifact = ctx && ctx.artifacts && ctx.artifacts.video;
  const url = trimmed(artifact && artifact.url);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!isObject(req)) throw new Error("request body must be an object");
      const model = trimmed(req.model);
      if (!model) throw new Error("model is required");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      if (req.images !== undefined && !Array.isArray(req.images)) throw new Error("images must be an array");
      if (req.metadata !== undefined && !isObject(req.metadata)) throw new Error("metadata must be an object");
      const input = responsesInput(req);
      const prompt = input.prompt || trimmed(req.prompt);
      if (!prompt) throw new Error("input is required");
      const images = [];
      for (const image of [req.image, req.input_reference].concat(req.images || [], input.images)) {
        if (trimmed(image) && !images.includes(trimmed(image))) images.push(trimmed(image));
      }
      const metadata = Object.assign({}, req.metadata || {});
      for (const key of ["resolution", "ratio"]) {
        if (Object.prototype.hasOwnProperty.call(req, key) && metadata[key] === undefined) metadata[key] = req[key];
      }
      const requestBody = { model: model, prompt: prompt, metadata: metadata };
      if (images.length) requestBody.images = images;
      if (Object.prototype.hasOwnProperty.call(req, "seconds")) requestBody.seconds = req.seconds;
      else if (Object.prototype.hasOwnProperty.call(req, "duration")) requestBody.seconds = req.duration;
      if (Object.prototype.hasOwnProperty.call(req, "size")) requestBody.size = req.size;
      const hasReference = images.length > 0 || (Array.isArray(metadata.content) && metadata.content.length > 0);
      return { kind: "submit", model: model, action: hasReference ? "image_to_video" : "text_to_video", requestBody: requestBody };
    },
    renderEvents: function (ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };
      if (status === "SUCCESS") {
        const text = responsesVideoText(ctx);
        const events = previousState && previousState.status === status ? [] : [{ type: "output", data: text }];
        return { events: events, state: state, done: true };
      }
      if (status === "FAILURE")
        return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state: state, done: true };
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state: state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },
    renderFinal: function (ctx, _task) {
      return {
        output: [
          {
            type: "message",
            status: "completed",
            role: "assistant",
            content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }],
          },
        ],
        metadata: { vendor: "thirdpartysd2" },
      };
    },
  },
};

// Never echoes task.data: its outputs are credential-gated upstream URLs.
function renderOpenAIVideo(task) {
  const statuses = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
  const output = {
    id: task.task_id,
    object: "video",
    model: (task.properties || {}).origin_model_name || "",
    status: statuses[task.status] || "unknown",
    progress: Number(String(task.progress || "0").replace("%", "")),
    created_at: Number(task.created_at || 0),
  };
  const upstream = isObject(task.data) && isObject(task.data.task) ? task.data.task : null;
  const upstreamStatus = upstream ? trimmed(upstream.status) : "";
  if (upstreamStatus === "failed" || upstreamStatus === "error" || upstreamStatus === "cancelled") {
    const error = upstream.error;
    output.error = {
      message: errorReason(error) || task.fail_reason || "",
      code: (isObject(error) && trimmed(error.code)) || "task_failed",
    };
  } else if (task.status === "FAILURE" && trimmed(task.fail_reason)) {
    output.error = { message: task.fail_reason, code: "task_failed" };
  }
  return output;
}

protocols.openai_video = {
  decodeRequest: function (ctx) {
    if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
    let req;
    if (ctx.body.kind === "json") {
      if (!isObject(ctx.body.value)) throw new Error("JSON object required");
      req = Object.assign({}, ctx.body.value);
    } else {
      if ((ctx.body.files || []).length) throw new Error("image and video references must be URLs in images or metadata.content");
      req = {};
      const fields = ctx.body.fields || {};
      for (const name of Object.keys(fields)) {
        const values = fields[name] || [];
        if (name === "images") {
          req.images = values.slice();
          continue;
        }
        if (values.length > 1) throw new Error(name + " must be provided once");
        req[name] = values[0];
      }
      if (req.metadata !== undefined) {
        let parsed;
        try {
          parsed = JSON.parse(req.metadata);
        } catch (e) {
          throw new Error("metadata must be a JSON object string");
        }
        if (!isObject(parsed)) throw new Error("metadata must be a JSON object string");
        req.metadata = parsed;
      }
    }
    if (req.metadata !== undefined && req.metadata !== null && !isObject(req.metadata)) throw new Error("metadata must be an object");
    if (req.images !== undefined && !Array.isArray(req.images)) throw new Error("images must be an array");
    if (!(req.images || []).length && trimmed(req.image)) req.images = [trimmed(req.image)];
    const seconds = req.seconds === undefined ? req.duration : req.seconds;
    if (seconds !== undefined && (!Number.isFinite(Number(seconds)) || Number(seconds) <= 0 || Number(seconds) > 3600))
      throw new Error("seconds must be between 1 and 3600");
    const metadata = isObject(req.metadata) ? req.metadata : {};
    const hasReference = (req.images || []).length > 0 || (Array.isArray(metadata.content) && metadata.content.length > 0);
    return {
      kind: "submit",
      model: ctx.model,
      action: hasReference ? "image_to_video" : "text_to_video",
      requestBody: Object.assign({}, req, { model: ctx.model }),
    };
  },
  render: function (ctx, task) {
    return renderOpenAIVideo(task);
  },
};
