import type {
  CreateThreadInput,
  HealthInfo,
  ProviderConfig,
  ProviderConfigInput,
  ProviderInfo,
  RunInfo,
  Thread,
  ThreadDetail,
  UpdateThreadInput,
} from "@/lib/types";

const configuredBase = process.env.NEXT_PUBLIC_CODJENT_API_URL?.replace(
  /\/+$/,
  "",
);

export function apiBase(): string {
  if (configuredBase) {
    return configuredBase;
  }
  if (typeof window === "undefined") {
    return "http://127.0.0.1:8787";
  }
  if (window.location.protocol === "file:") {
    return "http://127.0.0.1:8787";
  }
  if (window.location.port === "3000") {
    return "http://127.0.0.1:8787";
  }
  return window.location.origin;
}

export class APIError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  let response: Response;
  try {
    response = await fetch(`${apiBase()}${path}`, {
      ...init,
      headers,
    });
  } catch {
    throw new APIError(
      `Could not reach codjent at ${apiBase()}. Is the backend running?`,
      0,
    );
  }

  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) {
        message = body.error;
      }
    } catch {
      // Keep the status text when the server did not return JSON.
    }
    throw new APIError(message, response.status);
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const text = await response.text();
  if (!text) {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}

export const api = {
  health(): Promise<HealthInfo> {
    return request<HealthInfo>("/api/health");
  },

  providers(): Promise<ProviderInfo[]> {
    return request<ProviderInfo[]>("/api/providers");
  },

  listProviderConfigs(): Promise<ProviderConfig[]> {
    return request<ProviderConfig[]>("/api/settings/providers");
  },

  createProviderConfig(input: ProviderConfigInput): Promise<ProviderConfig> {
    return request<ProviderConfig>("/api/settings/providers", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  updateProviderConfig(
    id: string,
    input: ProviderConfigInput,
  ): Promise<ProviderConfig> {
    return request<ProviderConfig>(
      `/api/settings/providers/${encodeURIComponent(id)}`,
      {
        method: "PUT",
        body: JSON.stringify(input),
      },
    );
  },

  deleteProviderConfig(id: string): Promise<void> {
    return request<void>(`/api/settings/providers/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },

  listThreads(): Promise<Thread[]> {
    return request<Thread[]>("/api/threads");
  },

  createThread(input: CreateThreadInput): Promise<Thread> {
    return request<Thread>("/api/threads", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  getThread(id: string): Promise<ThreadDetail> {
    return request<ThreadDetail>(`/api/threads/${encodeURIComponent(id)}`);
  },

  updateThread(id: string, input: UpdateThreadInput): Promise<Thread> {
    return request<Thread>(`/api/threads/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(input),
    });
  },

  deleteThread(id: string): Promise<void> {
    return request<void>(`/api/threads/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },

  startMessage(threadID: string, content: string): Promise<RunInfo> {
    return request<RunInfo>(
      `/api/threads/${encodeURIComponent(threadID)}/messages`,
      {
        method: "POST",
        body: JSON.stringify({ content }),
      },
    );
  },

  getRun(runID: string): Promise<RunInfo> {
    return request<RunInfo>(`/api/runs/${encodeURIComponent(runID)}`);
  },

  resolveApproval(
    runID: string,
    approvalID: string,
    allow: boolean,
    note?: string,
  ): Promise<void> {
    return request<void>(
      `/api/runs/${encodeURIComponent(runID)}/approvals/${encodeURIComponent(approvalID)}`,
      {
        method: "POST",
        body: JSON.stringify({ allow, note }),
      },
    );
  },

  cancelRun(runID: string): Promise<void> {
    return request<void>(`/api/runs/${encodeURIComponent(runID)}/cancel`, {
      method: "POST",
      body: "{}",
    });
  },

  eventsURL(runID: string): string {
    return `${apiBase()}/api/runs/${encodeURIComponent(runID)}/events`;
  },
};
