// Cliente mínimo de la API de Dockfly. Los tipos reflejan los structs de Go
// (internal/store, internal/docker, internal/traefik).

export type TargetType = 'docker' | 'host' | 'external';
export type Scheme = 'http' | 'https';

export interface User {
  id: number;
  email: string;
  name: string;
  created_at: string;
}

export interface ProxyHost {
  id: number;
  domains: string[];
  target_type: TargetType;
  forward_scheme: Scheme;
  forward_host: string;
  forward_port: number;
  ssl: boolean;
  force_https: boolean;
  hsts: boolean;
  skip_tls_verify: boolean;
  preserve_host: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

/** Campos editables de un proxy host (lo que acepta la API al crear/editar). */
export type ProxyHostInput = Omit<ProxyHost, 'id' | 'created_at' | 'updated_at'>;

export interface SaveResult {
  proxy_host: ProxyHost;
  warnings: string[] | null;
}

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  networks: string[];
  ports: number[];
}

export interface ContainerList {
  available: boolean;
  self_networks?: string[];
  containers: Container[];
}

export interface Status {
  version: string;
  docker_available: boolean;
  traefik: { running: boolean; started_at?: string; last_error?: string };
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const opts: RequestInit = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }
  const res = await fetch('/api' + path, opts);
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.error || res.statusText);
  return data as T;
}

export const api = {
  setupStatus: () => request<{ needs_setup: boolean }>('GET', '/setup'),
  setup: (data: { name: string; email: string; password: string }) => request<User>('POST', '/setup', data),
  login: (data: { email: string; password: string }) => request<User>('POST', '/auth/login', data),
  logout: () => request<void>('POST', '/auth/logout'),
  me: () => request<User>('GET', '/auth/me'),
  status: () => request<Status>('GET', '/status'),
  listProxyHosts: () => request<ProxyHost[]>('GET', '/proxy-hosts'),
  createProxyHost: (data: ProxyHostInput) => request<SaveResult>('POST', '/proxy-hosts', data),
  updateProxyHost: (id: number, data: ProxyHostInput) => request<SaveResult>('PUT', `/proxy-hosts/${id}`, data),
  deleteProxyHost: (id: number) => request<void>('DELETE', `/proxy-hosts/${id}`),
  listContainers: () => request<ContainerList>('GET', '/docker/containers'),
};

/** Mensaje legible de cualquier error capturado en un `catch`. */
export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
