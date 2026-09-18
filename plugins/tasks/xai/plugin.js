// xAI official video generation (Grok Imagine).
// Upstream: POST {base}/v1/videos/generations -> {request_id}; GET {base}/v1/videos/{request_id}.
// Docs: https://docs.x.ai/developers/rest-api-reference/inference/videos

const MAX_DURATION_SECONDS = 15;
const MAX_REFERENCE_IMAGES = 7;
const MAX_REFERENCE_DURATION_SECONDS = 10;
const DEFAULT_DURATION_SECONDS = 5;
const MAX_INLINE_IMAGE_BYTES = 20 * 1024 * 1024;
const RESOLUTIONS = ["480p", "720p", "1080p"];

export const meta = {
  apiVersion: 1,
  key: "xai",
  name: "xAI Video",
  icon: "Grok",
  description: {
    en: "xAI Grok Imagine video generation (text-to-video, image-to-video, and reference-to-video)",
    zh: "xAI Grok Imagine 视频生成（文生视频、图生视频、参考图生视频）",
  },
  version: "1.0.0",
  author: { name: "NEXAXIS" },
  // xAI chat and video share the same channel type, base URL and bearer key.
  channelTypes: [48],
  // Provider-shaped inbound API, so a downstream gateway can cascade these
  // models to this gateway through a Task Plugin channel keyed xai with base
  // URL https://<host>/xai. The prefix is required: the unprefixed provider
  // paths would intersect this gateway's own static video routes.
  routes: [
    { method: "POST", path: "/xai/v1/videos/generations", type: "submit", decode: "createVideoTask", render: "videoCreated" },
    { method: "GET", path: "/xai/v1/videos/:request_id", type: "query", render: "videoStatus", taskIdParam: "request_id" },
  ],
  models: ["grok-imagine-video", "grok-imagine-video-1.5"],
  fetchMode: "per_task",
  usageSchema: {
    // Requested duration after the 15-second cap and the 5-second default.
    seconds: {
      type: "number",
      unit: "second",
      description: { en: "Video generation unit price", zh: "视频生成单价" },
    },
    // Resolution actually sent upstream; grok-imagine-video clamps 1080p to 720p.
    output_resolution: {
      enum: RESOLUTIONS,
      enumLabels: {
        "480p": { en: "480p", zh: "480p" },
        "720p": { en: "720p", zh: "720p" },
        "1080p": { en: "1080p", zh: "1080p" },
      },
      description: { en: "Output video resolution", zh: "输出视频分辨率" },
    },
    // Distinct input and reference images; xAI prices each input image.
    input_images: {
      type: "number",
      unit: "count",
      unitLabel: { en: "image", zh: "张" },
      description: { en: "Input image unit price", zh: "输入图片单价" },
    },
  },
  usageExamples: [
    { label: "480p · 5s", facts: { seconds: 5, output_resolution: "480p", input_images: 0 } },
    { label: "720p · 10s", facts: { seconds: 10, output_resolution: "720p", input_images: 0 } },
    { label: "720p · 8s + 1 image", facts: { seconds: 8, output_resolution: "720p", input_images: 1 } },
    { label: "1080p · 4s + 1 image", facts: { seconds: 4, output_resolution: "1080p", input_images: 1 } },
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

function positiveInt(value) {
  if (typeof value === "number") return Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
  if (typeof value === "string" && /^\s*\d+\s*$/.test(value)) return Number.parseInt(value, 10);
  return 0;
}

function isLegacyVideoModel(model) {
  const name = trimmed(model).toLowerCase();
  return name.includes("grok-imagine-video") && !name.includes("grok-imagine-video-1.5");
}

function isVideoModel15(model) {
  return trimmed(model).toLowerCase().includes("grok-imagine-video-1.5");
}

function metadataOf(req) {
  return isObject(req.metadata) ? req.metadata : {};
}

function durationSeconds(req) {
  const metadata = metadataOf(req);
  for (const key of ["durationSeconds", "duration"]) {
    const seconds = positiveInt(metadata[key]);
    if (seconds > 0) return seconds;
  }
  for (const key of ["duration", "seconds"]) {
    const seconds = positiveInt(req[key]);
    if (seconds > 0) return seconds;
  }
  return DEFAULT_DURATION_SECONDS;
}

function parseSize(size) {
  const parts = trimmed(size).toLowerCase().split("x");
  if (parts.length !== 2) return [0, 0];
  return [Number.parseInt(parts[0], 10) || 0, Number.parseInt(parts[1], 10) || 0];
}

function aspectRatio(req) {
  const metadata = metadataOf(req);
  for (const key of ["aspect_ratio", "aspectRatio"]) {
    if (typeof metadata[key] === "string" && trimmed(metadata[key])) return trimmed(metadata[key]);
  }
  if (!trimmed(req.size)) return "16:9";
  const [width, height] = parseSize(req.size);
  if (width <= 0 || height <= 0) return "16:9";
  if (width === height) return "1:1";
  return width > height ? "16:9" : "9:16";
}

function normalizeResolution(value) {
  const raw = trimmed(value).toLowerCase();
  if (raw === "480" || raw === "480p") return "480p";
  if (raw === "720" || raw === "720p") return "720p";
  if (raw === "1080" || raw === "1080p") return "1080p";
  return raw;
}

function requestedResolution(req) {
  const metadata = metadataOf(req);
  if (typeof metadata.resolution === "string" && trimmed(metadata.resolution)) return normalizeResolution(metadata.resolution);
  if (!trimmed(req.size)) return "720p";
  const [width, height] = parseSize(req.size);
  if (width <= 0 || height <= 0) return "720p";
  const shortSide = Math.min(width, height);
  if (shortSide >= 1080) return "1080p";
  if (shortSide >= 720) return "720p";
  return "480p";
}

// grok-imagine-video does not serve 1080p; it is priced and sent as 720p.
function supportedResolution(model, resolution) {
  const normalized = normalizeResolution(resolution);
  if (isLegacyVideoModel(model) && normalized === "1080p") return "720p";
  return normalized;
}

function base64ImageDataURL(raw) {
  const compact = raw.replace(/\s+/g, "");
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(compact)) return "";
  if (Math.floor((compact.length * 3) / 4) > MAX_INLINE_IMAGE_BYTES) return "";
  let mimeType = "application/octet-stream";
  if (compact.startsWith("/9j/")) mimeType = "image/jpeg";
  else if (compact.startsWith("iVBORw0KGgo")) mimeType = "image/png";
  else if (compact.startsWith("R0lGOD")) mimeType = "image/gif";
  else if (compact.startsWith("UklGR")) mimeType = "image/webp";
  return "data:" + mimeType + ";base64," + compact;
}

// Accepts a file id (file_*), an http(s)/data URL, or raw base64 image bytes.
function videoInput(raw) {
  if (isObject(raw)) {
    const input = {};
    if (trimmed(raw.url)) input.url = trimmed(raw.url);
    if (trimmed(raw.file_id)) input.file_id = trimmed(raw.file_id);
    return input.url || input.file_id ? input : null;
  }
  const value = trimmed(raw);
  if (!value) return null;
  if (value.startsWith("file_")) return { file_id: value };
  if (/^https?:\/\//i.test(value) || value.startsWith("data:")) return { url: value };
  const dataURL = base64ImageDataURL(value);
  return dataURL ? { url: dataURL } : null;
}

function requestImages(req) {
  return Array.isArray(req.images) ? req.images : [];
}

function uploadedImage(ctx) {
  for (const field of ["input_reference", "image"]) {
    for (const file of ctx.files || []) {
      if (file && file.field === field) {
        return { url: { __fileRef: file.ref, encoding: "dataUrl", maxBytes: MAX_INLINE_IMAGE_BYTES } };
      }
    }
  }
  return null;
}

function firstImage(ctx, req) {
  const uploaded = uploadedImage(ctx);
  if (uploaded) return uploaded;
  const metadataImage = videoInput(metadataOf(req).image);
  if (metadataImage) return metadataImage;
  const image = videoInput(req.image) || videoInput(req.input_reference);
  if (image) return image;
  const images = requestImages(req);
  return images.length === 1 ? videoInput(images[0]) : null;
}

function referenceImages(req) {
  const metadata = metadataOf(req);
  const refs = [];
  if (Array.isArray(metadata.reference_images)) {
    for (const item of metadata.reference_images) {
      const input = videoInput(item);
      if (input) refs.push(input);
    }
  }
  if (refs.length) return refs;
  const images = requestImages(req);
  if (images.length <= 1) return [];
  for (const item of images) {
    const input = videoInput(item);
    if (input) refs.push(input);
  }
  return refs;
}

function inputImageCount(ctx, req) {
  const seen = {};
  let count = 0;
  const add = function (input) {
    if (!input) return;
    const key = typeof input.url === "string" ? input.url : input.file_id ? "file:" + input.file_id : "";
    if (!key || seen[key]) return;
    seen[key] = true;
    count++;
  };
  if (uploadedImage(ctx)) count++;
  add(videoInput(req.image));
  add(videoInput(req.input_reference));
  for (const item of requestImages(req)) add(videoInput(item));
  const metadata = metadataOf(req);
  add(videoInput(metadata.image));
  if (Array.isArray(metadata.reference_images)) {
    for (const item of metadata.reference_images) add(videoInput(item));
  }
  return count;
}

function usageModel(ctx) {
  return ctx.upstreamModel || ctx.model || "";
}

function validate(ctx, req) {
  const model = usageModel(ctx);
  const seconds = durationSeconds(req);
  if (seconds > MAX_DURATION_SECONDS) throw new Error("duration must not exceed " + MAX_DURATION_SECONDS + " seconds");
  const requested = requestedResolution(req);
  if (!RESOLUTIONS.includes(supportedResolution(model, requested))) {
    throw new Error('resolution "' + requested + '" is not supported; use 480p, 720p or 1080p');
  }
  const refs = referenceImages(req);
  if (refs.length > MAX_REFERENCE_IMAGES) throw new Error("reference_images must not exceed " + MAX_REFERENCE_IMAGES);
  if (isVideoModel15(model)) {
    if (refs.length) throw new Error("grok-imagine-video-1.5 does not support reference_images");
    if (!firstImage(ctx, req)) throw new Error("grok-imagine-video-1.5 requires an input image");
    return;
  }
  if (refs.length && seconds > MAX_REFERENCE_DURATION_SECONDS) {
    throw new Error("reference_images require duration no greater than " + MAX_REFERENCE_DURATION_SECONDS + " seconds");
  }
}

// Legacy per-call units relative to the configured 480p per-second ModelPrice.
function videoUnits(model, resolution, seconds, imageCount) {
  let resolutionRatio = 1;
  let imageUnit = 0;
  if (isVideoModel15(model)) {
    if (resolution === "1080p") resolutionRatio = 3.125;
    else if (resolution === "720p") resolutionRatio = 1.75;
    imageUnit = 0.125;
  } else if (isLegacyVideoModel(model)) {
    if (resolution === "720p") resolutionRatio = 1.4;
    imageUnit = 0.04;
  }
  return seconds * resolutionRatio + imageCount * imageUnit;
}

export function buildSubmitRequest(ctx) {
  const req = ctx.requestBody || {};
  if (!trimmed(req.prompt)) throw new Error("prompt is required");
  if (req.metadata !== undefined && req.metadata !== null && !isObject(req.metadata)) throw new Error("metadata must be an object");
  validate(ctx, req);
  const model = usageModel(ctx) || trimmed(req.model);
  const body = {
    model: model,
    prompt: req.prompt,
    duration: durationSeconds(req),
    aspect_ratio: aspectRatio(req),
    resolution: supportedResolution(model, requestedResolution(req)),
  };
  const metadata = metadataOf(req);
  const seed = Number(metadata.seed);
  if (metadata.seed !== undefined && metadata.seed !== null && Number.isInteger(seed)) body.seed = seed;
  let action = "text_to_video";
  const image = firstImage(ctx, req);
  if (image) {
    body.image = image;
    action = "image_to_video";
  }
  const refs = referenceImages(req);
  if (refs.length) {
    body.reference_images = refs;
    action = "image_to_video";
  }
  return {
    url: trimBase(ctx.baseUrl) + "/v1/videos/generations",
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: body,
    action: action,
  };
}

export function parseSubmitResponse(ctx, resp) {
  const body = isObject(resp.body) ? resp.body : {};
  const requestId = trimmed(body.request_id);
  if (!requestId) throw new Error("missing request_id");
  return { taskId: requestId, taskData: body };
}

export function extractUsage(ctx) {
  const req = ctx.requestBody || {};
  const model = usageModel(ctx);
  const seconds = Math.min(durationSeconds(req), 3600);
  const resolution = supportedResolution(model, requestedResolution(req));
  const images = inputImageCount(ctx, req);
  // Ratio pricing keeps the historical single multiplier on the per-call ModelPrice.
  if (ctx.usagePurpose === "billing_ratios") return { xai_video_units: videoUnits(model, resolution, seconds, images) };
  return { seconds: seconds, output_resolution: RESOLUTIONS.includes(resolution) ? resolution : "720p", input_images: Math.min(images, 128) };
}

// xAI bills the requested duration; completion does not change any fact.
export function extractUsageOnComplete(_task, _taskResult, _body) {
  return {};
}

export function buildQueryRequest(ctx) {
  return {
    url: trimBase(ctx.baseUrl) + "/v1/videos/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
  };
}

function errorMessage(error) {
  if (typeof error === "string") return trimmed(error);
  if (isObject(error)) return trimmed(error.message);
  return "";
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "unrecognized response" };
  let status = trimmed(body.status).toLowerCase();
  // A structured error without a status is a terminal failure; an early poll
  // that only echoes the request id is still in progress.
  if (!status && body.error) status = "error";
  if (["", "processing", "in_progress", "generating"].includes(status)) return { status: "IN_PROGRESS", progress: "30%" };
  if (["queued", "pending"].includes(status)) return { status: "QUEUED", progress: "20%" };
  if (["done", "completed", "success"].includes(status)) {
    const result = { status: "SUCCESS", progress: "100%" };
    const url = trimmed(body.video && body.video.url);
    if (url) result.url = url;
    return result;
  }
  if (status === "expired") return { status: "FAILURE", progress: "100%", reason: "task expired" };
  if (["failed", "error"].includes(status)) return { status: "FAILURE", progress: "100%", reason: errorMessage(body.error) || "task failed" };
  return { status: "UNKNOWN", reason: "unknown xai video task status: " + String(body.status) };
}

function videoURL(data) {
  return isObject(data) && isObject(data.video) ? trimmed(data.video.url) : "";
}

export function listArtifacts(task) {
  if (task.status !== "SUCCESS" || !videoURL(task.data)) return [];
  return [{ key: "video", type: "video", mimeType: "video/mp4" }];
}

function baseOrigin(baseUrl) {
  const match = /^https?:\/\/[^/?#]+/i.exec(trimmed(baseUrl));
  return match ? match[0] : "";
}

// xAI result URLs are public signed CDN links; no channel credential is sent.
// A cascaded gateway instead returns an absolute path on its own host (it
// cannot know its server address), which takes the channel credential.
export function buildContentRequest(ctx) {
  const url = ctx.artifactKey === "video" ? videoURL(ctx.data) : "";
  if (!url) throw new Error("artifact_not_found");
  if (url.startsWith("/")) {
    const origin = baseOrigin(ctx.baseUrl);
    if (!origin) throw new Error("artifact_not_found");
    return { url: origin + url, method: ctx.clientRequest.method, headers: { Authorization: "Bearer " + ctx.apiKey } };
  }
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

// ---------------------------------------------------------------------------
// Inbound provider-shaped API (cascade): POST /xai/v1/videos/generations and
// GET /xai/v1/videos/:request_id answer with xAI's own request and response
// shapes, so this gateway can serve another gateway that runs this plugin.
// ---------------------------------------------------------------------------

const GATEWAY_STATUS = { NOT_START: "processing", SUBMITTED: "processing", QUEUED: "queued", IN_PROGRESS: "processing", SUCCESS: "done", FAILURE: "failed" };

export const native = {
  createVideoTask: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
    const body = ctx.body.value;
    if (!isObject(body)) throw new Error("request body must be an object");
    const model = trimmed(body.model);
    if (!model) throw new Error("model is required");
    if (!trimmed(body.prompt)) throw new Error("prompt is required");
    // The whole request minus model and prompt becomes metadata, which is
    // where this plugin reads duration, resolution, aspect ratio, seed and
    // image inputs from, so the upstream request repeats this request.
    const metadata = Object.assign({}, body);
    delete metadata.model;
    delete metadata.prompt;
    const hasImage = !!videoInput(metadata.image) || referenceImages({ metadata: metadata }).length > 0;
    return {
      kind: "submit",
      model: model,
      action: hasImage ? "image_to_video" : "text_to_video",
      requestBody: { model: model, prompt: String(body.prompt), metadata: metadata },
    };
  },
  videoCreated: function (_ctx, task) {
    return { request_id: task.task_id };
  },
  videoStatus: function (_ctx, task) {
    const output = { request_id: task.task_id, status: GATEWAY_STATUS[task.status] || "processing" };
    const model = trimmed((task.properties || {}).origin_model_name);
    if (model) output.model = model;
    if (task.status === "SUCCESS" && videoURL(task.data)) {
      const upstream = isObject(task.data) && isObject(task.data.video) ? task.data.video : {};
      // The provider URL needs no credential but stays private; clients of the
      // cascade download through this gateway's content proxy.
      output.video = Object.assign({}, upstream, { url: "/v1/videos/" + encodeURIComponent(task.task_id) + "/content" });
    }
    if (task.status === "FAILURE") {
      const upstream = isObject(task.data) && isObject(task.data.error) ? task.data.error : {};
      output.error = { code: trimmed(upstream.code) || "task_failed", message: trimmed(task.fail_reason) || trimmed(upstream.message) || "task failed" };
    }
    return output;
  },
  error: function (_ctx, error) {
    return { error: { code: error.code, message: error.message } };
  },
};

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
      for (const key of ["resolution", "aspect_ratio", "seed"]) {
        if (Object.prototype.hasOwnProperty.call(req, key) && metadata[key] === undefined) metadata[key] = req[key];
      }
      const requestBody = { model: model, prompt: prompt, metadata: metadata };
      if (images.length) requestBody.images = images;
      if (Object.prototype.hasOwnProperty.call(req, "seconds")) requestBody.seconds = req.seconds;
      else if (Object.prototype.hasOwnProperty.call(req, "duration")) requestBody.seconds = req.duration;
      if (Object.prototype.hasOwnProperty.call(req, "size")) requestBody.size = req.size;
      return { kind: "submit", model: model, action: images.length ? "image_to_video" : "text_to_video", requestBody: requestBody };
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
        metadata: { vendor: "xai" },
      };
    },
  },
};

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
  const url = videoURL(task.data);
  // A cascaded gateway stores an absolute path on the upstream gateway; only a
  // public provider URL is handed to clients, who otherwise use the content
  // endpoint of this gateway.
  if (task.status === "SUCCESS" && /^https?:\/\//i.test(url)) output.metadata = { url: url };
  if (task.status === "FAILURE" && trimmed(task.fail_reason)) output.error = { message: task.fail_reason, code: "task_failed" };
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
      let hasImageFile = false;
      for (const file of ctx.body.files || []) {
        if (file.field !== "input_reference" && file.field !== "image") throw new Error("unexpected file field: " + file.field);
        if (hasImageFile) throw new Error("only one input image file may be uploaded");
        hasImageFile = true;
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
      if (hasImageFile) req.has_image_file = true;
    }
    if (req.metadata !== undefined && req.metadata !== null && !isObject(req.metadata)) throw new Error("metadata must be an object");
    const seconds = req.seconds === undefined ? req.duration : req.seconds;
    if (seconds !== undefined && (!Number.isFinite(Number(seconds)) || Number(seconds) <= 0 || Number(seconds) > MAX_DURATION_SECONDS))
      throw new Error("seconds must be between 1 and " + MAX_DURATION_SECONDS);
    const hasImage = !!(req.has_image_file || trimmed(req.input_reference) || trimmed(req.image) || requestImages(req).length);
    delete req.has_image_file;
    return {
      kind: "submit",
      model: ctx.model,
      action: hasImage ? "image_to_video" : "text_to_video",
      requestBody: Object.assign({}, req, { model: ctx.model }),
    };
  },
  render: function (ctx, task) {
    return renderOpenAIVideo(task);
  },
};
