import {
  INSTALLER_SCRIPT,
  INSTALLER_SCRIPT_SHA256,
} from "./installer-script.js";

const REDIRECT_URL = "https://github.com/onekvm";
const COMMAND_LINE_USER_AGENT = /\b(?:curl|wget)(?:\/|\b)/i;
const SCRIPT_CACHE_CONTROL =
  "public, max-age=300, s-maxage=31536000, immutable";

function cacheKey(request) {
  const url = new URL(request.url);
  url.pathname = `/__onekvm-script/${INSTALLER_SCRIPT_SHA256}`;
  url.search = "";
  return new Request(url, { method: "GET" });
}

async function scriptResponse(request, ctx) {
  if (INSTALLER_SCRIPT.length === 0) {
    return new Response("The release script has not been embedded.\n", {
      status: 503,
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  }

  const key = cacheKey(request);
  const cached = await caches.default.match(key);
  if (cached) {
    return request.method === "HEAD"
      ? new Response(null, { status: cached.status, headers: cached.headers })
      : cached;
  }

  const response = new Response(INSTALLER_SCRIPT, {
    status: 200,
    headers: {
      "cache-control": SCRIPT_CACHE_CONTROL,
      "content-disposition": 'inline; filename="onekvm.sh"',
      "content-type": "text/plain; charset=utf-8",
      vary: "user-agent",
      "x-onekvm-script-sha256": INSTALLER_SCRIPT_SHA256,
      "x-content-type-options": "nosniff",
    },
  });

  ctx?.waitUntil(caches.default.put(key, response.clone()));
  return request.method === "HEAD"
    ? new Response(null, { status: response.status, headers: response.headers })
    : response;
}

function redirectResponse() {
  return new Response(null, {
    status: 301,
    headers: {
      "cache-control": "no-store",
      location: REDIRECT_URL,
    },
  });
}

export default {
  async fetch(request, _env, ctx) {
    const userAgent = request.headers.get("user-agent") || "";
    if (!COMMAND_LINE_USER_AGENT.test(userAgent)) {
      return redirectResponse();
    }
    return scriptResponse(request, ctx);
  },
};
