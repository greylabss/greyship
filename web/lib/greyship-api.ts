// Browser calls to the real /v1 API, which the dashboard serves on its own origin (proxy.ts), so cookies stay on
// this host and no CORS is involved. Without GREYSHIP_API_URL the auth pages stay in mock mode.
export const apiURL = "";
export const apiEnabled = process.env.NEXT_PUBLIC_API_ENABLED === "1";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
  ) {
    super(message);
  }
}

export async function call<T>(method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE", path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(apiURL + path, {
      method,
      credentials: "include",
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError("Can't reach Greyship right now. Check your connection and try again.", 0, "network");
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(data?.error?.message ?? `Request failed (${res.status})`, res.status, data?.error?.code ?? "unknown");
  }
  return data as T;
}

export function oauthStartURL(provider: "google" | "github") {
  return `${apiURL}/v1/auth/oauth/${provider}/start`;
}
