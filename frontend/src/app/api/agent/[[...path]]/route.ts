import type { NextRequest } from "next/server";

const HARNESS_BASE_URL =
  process.env.HARNESS_BASE_URL ?? "http://127.0.0.1:7776";

const HOP_BY_HOP_HEADERS = new Set([
  "connection",
  "content-length",
  "host",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function copyHeaders(source: Headers) {
  const headers = new Headers();
  source.forEach((value, key) => {
    if (!HOP_BY_HOP_HEADERS.has(key.toLowerCase())) {
      headers.set(key, value);
    }
  });
  return headers;
}

function buildTargetURL(request: NextRequest) {
  const upstreamPath =
    request.nextUrl.pathname.replace(/^\/api\/agent(?=\/|$)/, "") || "/";
  const target = new URL(
    `/api/v1${upstreamPath}`,
    HARNESS_BASE_URL.endsWith("/") ? HARNESS_BASE_URL : `${HARNESS_BASE_URL}/`,
  );
  request.nextUrl.searchParams.forEach((value, key) => {
    target.searchParams.append(key, value);
  });
  return target;
}

async function proxyHarnessRequest(request: NextRequest) {
  const headers = copyHeaders(request.headers);
  // Compressed upstream responses can force intermediaries to buffer before
  // forwarding. SSE is already compact and must remain incrementally readable.
  headers.set("accept-encoding", "identity");

  let body: ArrayBuffer | undefined;
  if (request.method !== "GET" && request.method !== "HEAD") {
    body = await request.arrayBuffer();
  }

  try {
    const upstream = await fetch(buildTargetURL(request), {
      method: request.method,
      headers,
      body,
      redirect: "manual",
      cache: "no-store",
      signal: request.signal,
    });
    const responseHeaders = copyHeaders(upstream.headers);
    if (responseHeaders.get("content-type")?.includes("text/event-stream")) {
      responseHeaders.set("cache-control", "no-cache, no-transform");
      responseHeaders.set("x-accel-buffering", "no");
    }

    return new Response(upstream.body, {
      status: upstream.status,
      statusText: upstream.statusText,
      headers: responseHeaders,
    });
  } catch (error) {
    const detail =
      error instanceof Error ? error.message : "Failed to reach Go Harness";
    return Response.json({ detail }, { status: 502 });
  }
}

type HarnessRouteContext = {
  params: Promise<{ path?: string[] }>;
};

export async function GET(request: NextRequest, _context: HarnessRouteContext) {
  return proxyHarnessRequest(request);
}

export async function HEAD(
  request: NextRequest,
  _context: HarnessRouteContext,
) {
  return proxyHarnessRequest(request);
}

export async function POST(
  request: NextRequest,
  _context: HarnessRouteContext,
) {
  return proxyHarnessRequest(request);
}

export async function PUT(request: NextRequest, _context: HarnessRouteContext) {
  return proxyHarnessRequest(request);
}

export async function PATCH(
  request: NextRequest,
  _context: HarnessRouteContext,
) {
  return proxyHarnessRequest(request);
}

export async function DELETE(
  request: NextRequest,
  _context: HarnessRouteContext,
) {
  return proxyHarnessRequest(request);
}

export async function OPTIONS(
  request: NextRequest,
  _context: HarnessRouteContext,
) {
  return proxyHarnessRequest(request);
}
