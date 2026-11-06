import { NextResponse, type NextRequest } from "next/server";

const apiURL = (process.env.GREYSHIP_API_URL ?? "").replace(/\/$/, "");

// /v1 is the API served on the dashboard's own origin: session cookies stay host-only on this host, and no app on
// another domain can ride on them (decisions.md 037, 038). The visitor's address goes along with the shared secret
// so the API's per-IP limits count people, not this proxy.
function toAPI(request: NextRequest) {
  const headers = new Headers(request.headers);
  headers.set("x-greyship-client-ip", request.headers.get("x-forwarded-for")?.split(",")[0]?.trim() ?? "");
  headers.set("x-greyship-proxy-secret", process.env.GREYSHIP_PROXY_SECRET ?? "");
  const target = new URL(request.nextUrl.pathname + request.nextUrl.search, apiURL);
  return NextResponse.rewrite(target, { request: { headers } });
}

export function proxy(request: NextRequest) {
  if (request.nextUrl.pathname.startsWith("/v1/")) {
    return apiURL ? toAPI(request) : new NextResponse("No API configured.", { status: 404 });
  }
  return NextResponse.next();
}

export const config = {
  matcher: ["/((?!_next/).*)"],
};
