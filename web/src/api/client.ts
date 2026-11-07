// 类型化 API 客户端 — 对齐 server.go 30 条路由(已核);按域分模块
// 约定:全部 JSON;错误走 HTTP 状态码+body error 字段

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      /* 忽略非 JSON body */
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body === undefined ? undefined : JSON.stringify(body) }),
};

// ---- 域模块(P2 逐页落地时扩充类型) ----

export const configApi = {
  get: () => api.get<Record<string, unknown>>("/api/config"),
  put: (patch: Record<string, unknown>) => api.put<Record<string, unknown>>("/api/config", patch),
  getScope: () => api.get<Record<string, unknown>>("/api/config/scope"),
};

export const vendorsApi = {
  list: () => api.get<Record<string, unknown>>("/api/vendors"),
};

export const mcpApi = {
  get: () => api.get<Record<string, unknown>>("/api/mcp"),
  status: () => api.get<Record<string, unknown>>("/api/mcp/status"),
};

export const imApi = {
  get: () => api.get<Record<string, unknown>>("/api/im"),
  status: () => api.get<Record<string, unknown>>("/api/im/status"),
  adapters: () => api.get<Record<string, unknown>>("/api/im/adapters"),
};

export const a2aApi = {
  get: () => api.get<Record<string, unknown>>("/api/a2a"),
};

export const sessionsApi = {
  list: () => api.get<unknown[]>("/api/sessions"),
  history: () => api.get<unknown[]>("/api/chat/history"),
};

export const knightApi = {
  get: () => api.get<Record<string, unknown>>("/api/knight"),
  skills: () => api.get<Record<string, unknown>>("/api/knight/skills"),
};

export const generalApi = {
  get: () => api.get<Record<string, unknown>>("/api/general"),
};
