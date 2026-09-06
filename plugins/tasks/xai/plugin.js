export const meta = {
  apiVersion: 1,
  key: "xai",
  name: "xAI Grok Video",
  icon: "Grok.Color",
  description: {
    en: "xAI Grok Imagine video generation (text-to-video and image-to-video)",
    zh: "xAI Grok Imagine 视频生成（文生视频、图生视频）",
  },
  version: "1.0.0",
  author: { name: "SakuraPuare" },
  channelTypes: [48],
  models: ["grok-imagine-video", "grok-imagine-video-1.5", "grok-imagine-video-1.5-preview"],
  fetchMode: "per_task",
  usageSchema: {
    duration: { type: "number", unit: "second", description: { en: "Requested video duration in seconds (1-15).", zh: "请求的视频时长，单位为秒（1-15）。" } },
    aspect_ratio: {
      enum: ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"],
      description: { en: "Requested output aspect ratio.", zh: "请求的输出宽高比。" },
    },
    resolution: { enum: ["480p", "720p", "1080p"], description: { en: "Requested output resolution.", zh: "请求的输出分辨率。" } },
  },
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

const ASPECT_RATIOS = ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"];
const RESOLUTIONS = ["480p", "720p", "1080p"];

function trimmed(value) {
  return String(value || "").trim();
}

function durationOf(req) {
  const value = req.seconds === undefined ? req.duration : req.seconds;
  if (value === undefined || value === null || value === "") return 8;
  const duration = Number(value);
  if (!Number.isInteger(duration) || duration < 1 || duration > 15) throw new Error("duration must be an integer between 1 and 15 seconds");
  return duration;
}

function aspectRatioOf(req) {
  const value = trimmed(req.aspect_ratio || req.aspectRatio) || "16:9";
  if (!ASPECT_RATIOS.includes(value)) throw new Error("aspect_ratio must be one of 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3");
  return value;
}

function resolutionOf(req) {
  const value = trimmed(req.resolution) || "480p";
  if (!RESOLUTIONS.includes(value)) throw new Error("resolution must be one of 480p, 720p, 1080p");
  return value;
}

function requestBody(req, model) {
  const body = { model, prompt: trimmed(req.prompt), duration: durationOf(req), aspect_ratio: aspectRatioOf(req), resolution: resolutionOf(req) };
  if (!body.prompt) throw new Error("field prompt is required");
  if (req.generate_audio !== undefined) body.generate_audio = Boolean(req.generate_audio);
  const image = req.image || req.input_reference;
  if (image) {
    body.image = typeof image === "object" && !Array.isArray(image) && image.__fileRef ? image : { url: trimmed(image) };
    if (!body.image.url && !body.image.__fileRef) throw new Error("image must contain a URL or file reference");
  }
  return body;
}

function responsesInput(req) {
  const texts = [],
    images = [],
    input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input))
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") texts.push(part);
        else if (part && typeof part === "object" && ["input_text", "text"].includes(part.type)) texts.push(part.text || "");
        else if (part && typeof part === "object" && ["input_image", "image_url"].includes(part.type)) {
          const image = typeof part.image_url === "object" ? part.image_url.url : part.image_url;
          if (trimmed(image)) images.push(trimmed(image));
        }
      }
    }
  return { prompt: texts.filter((text) => trimmed(text)).join("\n"), images };
}

function videoText(ctx) {
  const artifact = ctx && ctx.artifacts && ctx.artifacts.video;
  const url = trimmed(artifact && artifact.url);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

export function buildSubmitRequest(ctx) {
  const body = requestBody(ctx.requestBody || {}, ctx.upstreamModel || ctx.model);
  return {
    url: ctx.baseUrl + "/v1/videos/generations",
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + ctx.apiKey },
    body,
    action: body.image ? "image_to_video" : "text_to_video",
  };
}

export function parseSubmitResponse(_ctx, resp) {
  const body = resp.body || {},
    taskId = body.request_id || body.id || body.task_id;
  if (!taskId) throw new Error("request_id is empty");
  return { taskId, taskData: body };
}

export function extractUsage(ctx) {
  const req = ctx.requestBody || {};
  return { duration: durationOf(req), aspect_ratio: aspectRatioOf(req), resolution: resolutionOf(req) };
}

export function extractUsageOnComplete(_task, _taskResult, body) {
  const duration = Number(body && body.video && body.video.duration);
  return Number.isFinite(duration) && duration > 0 ? { duration: Math.min(Math.ceil(duration), 15) } : null;
}

export function buildQueryRequest(ctx) {
  return { url: ctx.baseUrl + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
}

export function parseTaskResult(_ctx, body) {
  const status = trimmed(body && body.status).toLowerCase();
  if (status === "pending") return { status: "IN_PROGRESS" };
  if (status === "done") return { status: "SUCCESS", url: body.video && body.video.url ? body.video.url : "" };
  if (status === "failed" || status === "expired") return { status: "FAILURE", reason: (body.error && body.error.message) || "video generation " + status };
  return { status: "UNKNOWN", reason: "unrecognized status: " + status };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && task.data && task.data.video && trimmed(task.data.video.url)
    ? [{ key: "video", type: "video", mimeType: "video/mp4" }]
    : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = ctx.data && ctx.data.video && trimmed(ctx.data.video.url);
  if (!url) throw new Error("artifact_not_found");
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export const protocols = {
  openai_responses: {
    decodeRequest(ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      const input = responsesInput(req),
        prompt = input.prompt || trimmed(req.prompt);
      if (!prompt) throw new Error("input is required");
      const normalized = Object.assign({}, req, { prompt });
      if (!normalized.image && !normalized.input_reference && input.images.length) normalized.image = input.images[0];
      return {
        kind: "submit",
        model: trimmed(req.model) || ctx.model,
        action: normalized.image || normalized.input_reference ? "image_to_video" : "text_to_video",
        requestBody: normalized,
      };
    },
    renderEvents(ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status, progress };
      if (status === "SUCCESS")
        return { events: previousState && previousState.status === status ? [] : [{ type: "output", data: videoText(ctx) }], state, done: true };
      if (status === "FAILURE") return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state, done: true };
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state, done: false };
    },
    renderFinal(ctx) {
      return {
        output: [
          { type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text: videoText(ctx), annotations: [], logprobs: [] }] },
        ],
        metadata: { vendor: "xai" },
      };
    },
  },
  openai_video: {
    decodeRequest(ctx) {
      if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
      let req = ctx.body.kind === "json" ? Object.assign({}, ctx.body.value || {}) : {},
        hasFile = false;
      if (ctx.body.kind === "multipart") {
        for (const name of Object.keys(ctx.body.fields || {})) {
          const values = ctx.body.fields[name] || [];
          if (values.length > 1) throw new Error(name + " must be provided once");
          req[name] = values[0];
        }
        for (const file of ctx.body.files || []) {
          if (file.field !== "input_reference") throw new Error("unexpected file field: " + file.field);
          if (hasFile) throw new Error("input_reference must be provided once");
          hasFile = true;
        }
      }
      if (hasFile) req.image = { __fileRef: "request_file:input_reference", encoding: "dataUrl", maxBytes: 15728640 };
      const model = ctx.model || req.model;
      requestBody(Object.assign({}, req, { prompt: req.prompt || "placeholder" }), model);
      return {
        kind: "submit",
        model,
        action: hasFile || req.image || req.input_reference ? "image_to_video" : "text_to_video",
        requestBody: Object.assign({}, req, { model }),
      };
    },
    render(_ctx, task) {
      const statuses = { SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      return {
        id: task.task_id,
        object: "video",
        model: (task.properties || {}).origin_model_name || "",
        status: statuses[task.status] || "unknown",
        progress: Number(String(task.progress || "0").replace("%", "")),
        created_at: Number(task.created_at || 0),
      };
    },
  },
};
