import type { NextRequest } from "next/server";

const DRAMA_BACKEND_BASE_URL =
  process.env.DRAMA_BACKEND_BASE_URL ?? "http://127.0.0.1:8003";

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

function buildTargetUrl(request: NextRequest) {
  const backendPath =
    request.nextUrl.pathname.replace(/^\/drama-api(?=\/|$)/, "/api/drama") ||
    "/api/drama";
  const target = new URL(
    backendPath.replace(/\/$/, ""),
    DRAMA_BACKEND_BASE_URL.endsWith("/")
      ? DRAMA_BACKEND_BASE_URL
      : `${DRAMA_BACKEND_BASE_URL}/`,
  );

  request.nextUrl.searchParams.forEach((value, key) => {
    target.searchParams.append(key, value);
  });

  return target;
}

function copyHeaders(source: Headers) {
  const headers = new Headers();

  source.forEach((value, key) => {
    if (!HOP_BY_HOP_HEADERS.has(key.toLowerCase())) {
      headers.set(key, value);
    }
  });

  return headers;
}

async function proxyDramaRequest(
  request: NextRequest,
) {
  const headers = copyHeaders(request.headers);
  const targetUrl = buildTargetUrl(request);

  // For non-GET/HEAD requests, read body as ArrayBuffer to avoid stream issues
  let body: ArrayBuffer | undefined;
  if (request.method !== "GET" && request.method !== "HEAD") {
    body = await request.arrayBuffer();
  }

  const init: RequestInit = {
    method: request.method,
    headers,
    redirect: "manual",
    body,
  };

  try {
    const upstream = await fetch(targetUrl, init);
    const responseHeaders = copyHeaders(upstream.headers);

    return new Response(upstream.body, {
      status: upstream.status,
      statusText: upstream.statusText,
      headers: responseHeaders,
    });
  } catch (error) {
    const detail =
      error instanceof Error ? error.message : "Failed to reach drama backend";

    return Response.json(
      {
        detail,
      },
      { status: 502 },
    );
  }
}

type DramaRouteContext = {
  params: Promise<{
    path?: string[];
  }>;
};

export async function GET(request: NextRequest, _context: DramaRouteContext) {
  return proxyDramaRequest(request);
}

export async function HEAD(request: NextRequest, _context: DramaRouteContext) {
  return proxyDramaRequest(request);
}

export async function POST(request: NextRequest, _context: DramaRouteContext) {
  return proxyDramaRequest(request);
}

export async function PUT(request: NextRequest, _context: DramaRouteContext) {
  return proxyDramaRequest(request);
}

export async function PATCH(request: NextRequest, _context: DramaRouteContext) {
  return proxyDramaRequest(request);
}

export async function DELETE(request: NextRequest, _context: DramaRouteContext) {
  return proxyDramaRequest(request);
}

export async function OPTIONS(request: NextRequest, _context: DramaRouteContext) {
  return proxyDramaRequest(request);
}
