import {
  INSTALLER_SCRIPT,
  INSTALLER_RELEASE_ID,
  INSTALLER_RELEASE_URL,
  INSTALLER_SCRIPT_SHA256,
} from "./installer-script.js";

const REDIRECT_URL = "https://github.com/onekvm";
const COMMAND_LINE_USER_AGENT = /\bcurl(?:\/|\b)/i;
const CHINA_COUNTRY = "CN";
const RELEASE_PROXY_PATH = "/__onekvm-release/latest/download";
const RELEASE_PROXY_ASSET_PREFIX = `${RELEASE_PROXY_PATH}/`;
const GITHUB_RELEASE_URL =
  INSTALLER_RELEASE_URL ||
  "https://github.com/onekvm/onekvm-installer/releases/latest/download";
const RELEASE_ASSET =
  /^(?:SHA256SUMS|onekvm-installer-[0-9][0-9A-Za-z.+_-]*-linux-(?:arm64|riscv64))$/;
const SCRIPT_CACHE_CONTROL =
  "public, max-age=300, s-maxage=31536000, immutable";
const RELEASE_CACHE_CONTROL =
  "public, max-age=300, s-maxage=31536000, immutable";
const RELEASE_CACHE_ID = INSTALLER_RELEASE_ID || INSTALLER_SCRIPT_SHA256;

function isChina(request) {
  const country = request.cf?.country || request.headers.get("cf-ipcountry");
  return typeof country === "string" && country.trim().toUpperCase() === CHINA_COUNTRY;
}

function cacheKey(request, variant) {
  const url = new URL(request.url);
  url.pathname = `/__onekvm-script/${INSTALLER_SCRIPT_SHA256}/${variant}`;
  url.search = "";
  return new Request(url, { method: "GET" });
}

function proxyCacheKey(request, asset) {
  const url = new URL(request.url);
  url.pathname = `/__onekvm-release/${RELEASE_CACHE_ID}/${asset}`;
  url.search = "";
  return new Request(url, { method: "GET" });
}

function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function chinaScript(request) {
  const origin = new URL(request.url).origin;
  const releaseURL = `${origin}${RELEASE_PROXY_PATH}`;
  const assignment = [
    "if [ -z \"${ONEKVM_INSTALLER_RELEASE_URL:-}\" ]; then",
    `  ONEKVM_INSTALLER_RELEASE_URL=${shellQuote(releaseURL)}`,
    "fi",
    "",
  ].join("\n");

  return INSTALLER_SCRIPT.replace(/^(#!\/bin\/sh\r?\n)/, (_, shebang) => shebang + assignment);
}

function headResponse(response) {
  return new Response(null, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
}

async function scriptResponse(request, ctx, china) {
  if (INSTALLER_SCRIPT.length === 0) {
    return new Response("The release script has not been embedded.\n", {
      status: 503,
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  }

  const variant = china ? "cn" : "default";
  const key = cacheKey(request, variant);
  const cached = await caches.default.match(key);
  if (cached) {
    return request.method === "HEAD" ? headResponse(cached) : cached;
  }

  const response = new Response(china ? chinaScript(request) : INSTALLER_SCRIPT, {
    status: 200,
    headers: {
      "cache-control": SCRIPT_CACHE_CONTROL,
      "content-disposition": 'inline; filename="onekvm.sh"',
      "content-type": "text/plain; charset=utf-8",
      vary: "user-agent, cf-ipcountry",
      "x-onekvm-script-sha256": INSTALLER_SCRIPT_SHA256,
      "x-content-type-options": "nosniff",
      ...(china ? { "x-onekvm-release-proxy": "cloudflare" } : {}),
    },
  });

  ctx?.waitUntil?.(caches.default.put(key, response.clone()));
  return request.method === "HEAD" ? headResponse(response) : response;
}

function requestedAsset(request) {
  const pathname = new URL(request.url).pathname;
  if (!pathname.startsWith(RELEASE_PROXY_ASSET_PREFIX)) {
    return null;
  }

  const encodedAsset = pathname.slice(RELEASE_PROXY_ASSET_PREFIX.length);
  if (!encodedAsset || encodedAsset.includes("/")) {
    return null;
  }

  let asset;
  try {
    asset = decodeURIComponent(encodedAsset);
  } catch {
    return null;
  }
  return RELEASE_ASSET.test(asset) ? asset : null;
}

async function releaseResponse(request, ctx) {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("Only GET and HEAD are supported.\n", {
      status: 405,
      headers: { allow: "GET, HEAD", "content-type": "text/plain; charset=utf-8" },
    });
  }

  const asset = requestedAsset(request);
  if (!asset) {
    return new Response("Release asset not found.\n", {
      status: 404,
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  }

  const key = proxyCacheKey(request, asset);
  const cached = await caches.default.match(key);
  if (cached) {
    return request.method === "HEAD" ? headResponse(cached) : cached;
  }

  const upstreamURL = `${GITHUB_RELEASE_URL}/${encodeURIComponent(asset)}`;
  let upstream;
  try {
    upstream = await fetch(upstreamURL, { redirect: "follow" });
  } catch {
    return new Response("Unable to fetch the installer release.\n", {
      status: 502,
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  }

  if (!upstream.ok) {
    return upstream;
  }

  const headers = new Headers(upstream.headers);
  headers.set("cache-control", RELEASE_CACHE_CONTROL);
  headers.set("x-content-type-options", "nosniff");
  const response = new Response(upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers,
  });
  ctx?.waitUntil?.(caches.default.put(key, response.clone()));
  return request.method === "HEAD" ? headResponse(response) : response;
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
    const pathname = new URL(request.url).pathname;
    if (pathname.startsWith(RELEASE_PROXY_PATH)) {
      return releaseResponse(request, ctx);
    }

    const userAgent = request.headers.get("user-agent") || "";
    if (!COMMAND_LINE_USER_AGENT.test(userAgent)) {
      return redirectResponse();
    }
    return scriptResponse(request, ctx, isChina(request));
  },
};
