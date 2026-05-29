import http from "k6/http";
import { check, sleep } from "k6";

const BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8081";
const ENDPOINT = __ENV.ENDPOINT || "/v1/chat/completions";
const MODEL = __ENV.MODEL || "gemini-3.5-flash-thinking-high";
const MESSAGE = __ENV.MESSAGE || "Write a short haiku about load testing.";
const DURATION = __ENV.DURATION || "30s";
const VUS = Number(__ENV.VUS || 10);
const THINK_SUFFIX = __ENV.THINK_SUFFIX || "";

export const options = {
  vus: VUS,
  duration: DURATION,
  thresholds: {
    http_req_failed: ["rate<0.05"],
    checks: ["rate>0.95"],
  },
};

function modelName() {
  if (!THINK_SUFFIX) {
    return MODEL;
  }
  if (MODEL.endsWith("-low") || MODEL.endsWith("-medium") || MODEL.endsWith("-high") || MODEL.endsWith("-xhigh") || MODEL.endsWith("-max")) {
    return MODEL;
  }
  return `${MODEL}-${THINK_SUFFIX}`;
}

export default function () {
  const payload = JSON.stringify({
    model: modelName(),
    stream: true,
    messages: [
      {
        role: "user",
        content: MESSAGE,
      },
    ],
  });

  const headers = {
    "Content-Type": "application/json",
    Accept: "text/event-stream",
  };

  if (__ENV.API_KEY) {
    headers.Authorization = `Bearer ${__ENV.API_KEY}`;
  }
  if (__ENV.RESIN_PLATFORM) {
    headers["X-Resin-Platform"] = __ENV.RESIN_PLATFORM;
  }
  if (__ENV.RESIN_ACCOUNT) {
    headers["X-Resin-Account"] = __ENV.RESIN_ACCOUNT;
  }
  if (__ENV.RESIN_MODE) {
    headers["X-Resin-Mode"] = __ENV.RESIN_MODE;
  }

  const res = http.post(`${BASE_URL}${ENDPOINT}`, payload, { headers, timeout: "180s" });

  const okStatus = check(res, {
    "status is 200": (r) => r.status === 200,
    "content-type is event stream": (r) =>
      String(r.headers["Content-Type"] || r.headers["content-type"] || "").includes("text/event-stream"),
  });

  if (!okStatus) {
    console.error(`bad response: status=${res.status}, body=${String(res.body || "").slice(0, 400)}`);
  } else {
    const body = String(res.body || "");
    check(res, {
      "contains SSE frame": () => body.includes("data:"),
      "contains done marker or finish reason": () =>
        body.includes("data: [DONE]") ||
        body.includes("\"finish_reason\"") ||
        body.includes("\"finishReason\""),
    });
  }

  sleep(Number(__ENV.SLEEP_SECONDS || 0.2));
}
