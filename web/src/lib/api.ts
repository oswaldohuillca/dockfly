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

export interface Deployment {
  id: number;
  site_id: number;
  source: 'upload';
  file_count: number;
  total_bytes: number;
  created_at: string;
}

export interface Site {
  id: number;
  name: string;
  domains: string[];
  ssl: boolean;
  force_https: boolean;
  hsts: boolean;
  spa: boolean;
  active_deployment_id: number | null;
  active_deployment?: Deployment;
  created_at: string;
  updated_at: string;
}

export type SiteInput = Pick<Site, 'name' | 'domains' | 'ssl' | 'force_https' | 'hsts' | 'spa'>;

/** Lo que se sube: un .zip, o los archivos de una carpeta con su ruta relativa. */
export type UploadPayload = { archive: File } | { files: { path: string; file: File }[] };

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

  listSites: () => request<Site[]>('GET', '/sites'),
  getSite: (id: number) => request<{ site: Site; deployments: Deployment[] }>('GET', `/sites/${id}`),
  createSite: (data: SiteInput) => request<Site>('POST', '/sites', data),
  updateSite: (id: number, data: SiteInput) => request<Site>('PUT', `/sites/${id}`, data),
  deleteSite: (id: number) => request<void>('DELETE', `/sites/${id}`),
  activateDeployment: (siteId: number, id: number) =>
    request<void>('POST', `/sites/${siteId}/deployments/${id}/activate`),
  deleteDeployment: (siteId: number, id: number) => request<void>('DELETE', `/sites/${siteId}/deployments/${id}`),
  uploadDeployment,
};

/**
 * Sube un despliegue. Usa XMLHttpRequest porque fetch todavía no informa del
 * progreso de subida. onProgress recibe un valor entre 0 y 1.
 */
function uploadDeployment(siteId: number, payload: UploadPayload, onProgress?: (p: number) => void) {
  const form = new FormData();
  if ('archive' in payload) {
    form.append('archive', payload.archive);
  } else {
    // El servidor lee la ruta del nombre del campo: el nombre del archivo
    // se recorta a su base al viajar en multipart.
    for (const { path, file } of payload.files) form.append(`file:${path}`, file, file.name);
  }

  return new Promise<Deployment>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `/api/sites/${siteId}/deployments`);
    xhr.setRequestHeader('X-Dockfly-Upload', '1');
    xhr.responseType = 'json';
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve(xhr.response as Deployment);
      else reject(new ApiError(xhr.status, xhr.response?.error || xhr.statusText));
    };
    xhr.onerror = () => reject(new ApiError(0, 'error de red durante la subida'));
    xhr.send(form);
  });
}

/** Mensaje legible de cualquier error capturado en un `catch`. */
export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
