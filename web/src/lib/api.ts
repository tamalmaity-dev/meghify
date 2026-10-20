import { QueryClient } from "@tanstack/react-query";

/**
 * A single query client for the app. Real-time updates from SSE call
 * `invalidateQueries` rather than writing into the cache directly, so the server
 * stays the only source of truth.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      gcTime: 5 * 60_000,
      retry: (failureCount, error) => {
        if (error instanceof ApiError && error.status === 401) return false;
        return failureCount < 2;
      },
      refetchOnWindowFocus: false,
    },
  },
});

export const API_BASE = "/api";

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly code?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    credentials: "same-origin",
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
    },
    ...init,
  });

  if (!response.ok) {
    let message = `Request failed with ${response.status}`;
    let code: string | undefined;
    try {
      const body = (await response.json()) as { error?: string; code?: string };
      if (body.error) message = body.error;
      code = body.code;
    } catch {
      // Non-JSON error bodies are treated as opaque failures.
    }

    // Only an authentication challenge means the cookie is gone. A failed
    // login, a wrong TOTP code or a wrong current password are normal 401s
    // that the calling form must render — never redirect on those.
    if (
      response.status === 401 &&
      message === "authentication required" &&
      !window.location.pathname.startsWith("/login")
    ) {
      window.location.assign("/login");
    }
    throw new ApiError(response.status, message, code);
  }

  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),
  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PATCH", body: body ? JSON.stringify(body) : undefined }),
  delete: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "DELETE", body: body ? JSON.stringify(body) : undefined }),
};

/** Turns a UTC timestamp from the API into a readable local string. */
export function formatDate(value: string | undefined | null): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

/** Formats bytes as a compact human string, e.g. 1.4 GB. */
export function formatBytes(bytes: number | undefined): string {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exp = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** exp;
  return `${value.toFixed(value >= 100 || exp === 0 ? 0 : 1)} ${units[exp]}`;
}

/**
 * Opens an SSE stream and yields decoded event payloads. The server frames
 * every event as {"channel", "payload", "at"}; the payload is what consumers
 * care about. Reconnects with backoff because proxies and laptops drop
 * connections constantly.
 */
export async function* streamEvents(
  path: string,
  signal?: AbortSignal,
): AsyncGenerator<Record<string, unknown>> {
  let attempt = 0;
  for (;;) {
    if (signal?.aborted) return;
    try {
      const response = await fetch(path, { credentials: "same-origin", signal });
      if (!response.ok || !response.body) throw new Error(`stream ${response.status}`);
      attempt = 0;

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });

        let index = buffer.indexOf("\n\n");
        while (index >= 0) {
          const frame = buffer.slice(0, index);
          buffer = buffer.slice(index + 2);
          if (frame.startsWith("data: ")) {
            try {
              const envelope = JSON.parse(frame.slice(6)) as Record<string, unknown>;
              const payload =
                envelope && typeof envelope === "object" && "payload" in envelope
                  ? (envelope.payload as Record<string, unknown>)
                  : envelope;
              yield payload;
            } catch {
              // A malformed frame is skipped; the next one usually arrives fine.
            }
          }
          index = buffer.indexOf("\n\n");
        }
      }
    } catch (error) {
      if (signal?.aborted) return;
      attempt += 1;
      await new Promise((resolve) => setTimeout(resolve, Math.min(attempt * 500, 5_000)));
      if (attempt > 20) throw error;
    }
  }
}
