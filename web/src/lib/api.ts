// Cliente mínimo de la API de Dockfly. Los tipos reflejan los structs de Go
// (internal/store, internal/docker, internal/traefik).

export type TargetType = 'docker' | 'host' | 'external';
export type Scheme = 'http' | 'https';

export interface User {
  id: number;
  email: string;
  name: string;
  role: Role;
  totp_enabled: boolean;
  created_at: string;
}

export type Role = 'admin' | 'deployer' | 'viewer';

export interface UserEntry extends User {
  /** Invitado que todavía no ha elegido contraseña. */
  pending: boolean;
}

export interface APIToken {
  id: number;
  name: string;
  prefix: string;
  role: Role;
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
}

export interface PasswordLink {
  link: string;
  expires_at: string;
}

/** Respuesta del login: la sesión, o el ticket para el segundo paso. */
export type LoginResult = User | { two_factor: true; ticket: string };

export interface TwoFactorSetup {
  secret: string;
  uri: string;
  qr_svg: string;
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
  access_list_id: number | null;
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
  self: boolean;
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

export type GitProvider = 'github' | 'gitlab';

export interface Deployment {
  id: number;
  site_id: number;
  source: 'upload' | GitProvider;
  file_count: number;
  total_bytes: number;
  created_at: string;
  commit_sha: string;
  commit_message: string;
  branch: string;
  /** Rama de la preview ('' = producción). */
  preview: string;
}

/** Último despliegue de una rama publicada como preview. */
export interface Preview {
  preview: string;
  branch: string;
  url: string;
  deployment: Deployment;
}

export interface Settings {
  public_url: string;
  preview_domain: string;
  preview_access_list_id: number | null;
  /** Hay un certificado comodín (zona DNS o propio) para las previews. */
  preview_ssl: boolean;
}

/** Configuración de build de un sitio enlazado a un repositorio. */
export interface SiteGit {
  git_connection_id: number | null;
  repo: string;
  branch: string;
  root_dir: string;
  install_command: string;
  build_command: string;
  output_dir: string;
  build_image: string;
  webhook_id: string;
}

export interface SiteGitInput {
  connection_id: number;
  repo: string;
  branch: string;
  root_dir: string;
  install_command: string;
  build_command: string;
  output_dir: string;
  build_image: string;
  /** null = conservar las variables actuales. */
  env: Record<string, string> | null;
}

export type AuthType = 'token' | 'github_app' | 'gitlab_oauth';

export interface GitConnection {
  id: number;
  provider: GitProvider;
  auth_type: AuthType;
  base_url: string;
  username: string;
  account_type: string;
  settings_url: string;
  github_app_id: number | null;
  installation_id: number | null;
  gitlab_app_id: number | null;
  created_at: string;
}

export interface GitHubApp {
  id: number;
  web_url: string;
  app_id: number;
  slug: string;
  name: string;
  owner: string;
  html_url: string;
  webhook_active: boolean;
  created_at: string;
}

export interface GitLabApp {
  id: number;
  web_url: string;
  client_id: string;
  created_at: string;
}

export interface Repo {
  full_name: string;
  default_branch: string;
  private: boolean;
  web_url: string;
}

export type BuildStatus = 'queued' | 'running' | 'success' | 'failed' | 'canceled';

export interface Build {
  id: number;
  site_id: number;
  status: BuildStatus;
  trigger: 'push' | 'manual';
  branch: string;
  preview: string;
  commit_sha: string;
  commit_message: string;
  deployment_id: number | null;
  error: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export const buildFinished = (b: Build) => b.status === 'success' || b.status === 'failed' || b.status === 'canceled';

export interface Site extends SiteGit {
  id: number;
  name: string;
  domains: string[];
  ssl: boolean;
  force_https: boolean;
  hsts: boolean;
  spa: boolean;
  previews: boolean;
  access_list_id: number | null;
  active_deployment_id: number | null;
  active_deployment?: Deployment;
  created_at: string;
  updated_at: string;
}

export type SiteInput = Pick<
  Site,
  'name' | 'domains' | 'ssl' | 'force_https' | 'hsts' | 'spa' | 'previews' | 'access_list_id'
>;

/** Lo que se sube: un .zip, o los archivos de una carpeta con su ruta relativa. */
export type UploadPayload = { archive: File } | { files: { path: string; file: File }[] };

export type CertState = 'valid' | 'expiring' | 'expired' | 'error' | 'pending';

export interface CertStatus {
  domain: string;
  status: CertState;
  not_after?: string;
  issuer?: string;
  staging: boolean;
  error?: string;
  error_at?: string;
  hint?: string;
}

export type ChannelType = 'email' | 'discord' | 'slack' | 'telegram' | 'webhook';

export type NotificationEvent =
  | 'build_failed'
  | 'build_succeeded'
  | 'cert_error'
  | 'cert_expiring'
  | 'container_down'
  | 'container_recovered';

export interface NotificationChannel {
  id: number;
  type: ChannelType;
  name: string;
  events: NotificationEvent[];
  enabled: boolean;
  last_error: string;
  last_sent_at: string | null;
  created_at: string;
  /** Los campos secretos llegan enmascarados (SECRET_MASK). */
  config: Record<string, unknown>;
}

export interface ChannelInput {
  type: ChannelType;
  name: string;
  events: NotificationEvent[];
  enabled: boolean;
  config: Record<string, unknown>;
}

/** Valor con el que el servidor enmascara los secretos; reenviarlo los conserva. */
export const SECRET_MASK = '••••••••';

export type AppSource = 'image' | 'git' | 'compose';
export type AppDeployStatus = 'queued' | 'running' | 'live' | 'replaced' | 'failed' | 'canceled';

export interface AppVolume {
  name: string;
  path: string;
}

export interface AppDeployment {
  id: number;
  app_id: number;
  status: AppDeployStatus;
  trigger: 'manual' | 'push' | 'rollback';
  image: string;
  container: string;
  commit_sha: string;
  commit_message: string;
  error: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface App {
  id: number;
  name: string;
  source: AppSource;
  image: string;
  git_connection_id: number | null;
  repo: string;
  branch: string;
  root_dir: string;
  dockerfile: string;
  compose_file: string;
  compose_service: string;
  webhook_id: string;
  port: number;
  domains: string[];
  ssl: boolean;
  force_https: boolean;
  hsts: boolean;
  volumes: AppVolume[];
  command: string;
  memory_mb: number;
  cpus: number;
  health_path: string;
  access_list_id: number | null;
  stopped: boolean;
  current_deployment_id: number | null;
  current?: AppDeployment;
  /** Estado real del contenedor (running, exited, restarting…); vacío si nunca se desplegó. */
  state: string;
  created_at: string;
  updated_at: string;
}

export interface AppInput {
  name: string;
  source: AppSource;
  image: string;
  connection_id: number;
  repo: string;
  branch: string;
  root_dir: string;
  dockerfile: string;
  compose_file: string;
  compose_service: string;
  port: number;
  domains: string[];
  ssl: boolean;
  force_https: boolean;
  hsts: boolean;
  /** null = conservar las variables actuales. */
  env: Record<string, string> | null;
  volumes: AppVolume[];
  command: string;
  memory_mb: number;
  cpus: number;
  health_path: string;
  access_list_id: number | null;
  deploy?: boolean;
}

export const appDeployFinished = (d: AppDeployment) => d.status !== 'queued' && d.status !== 'running';

export interface AccessList {
  id: number;
  name: string;
  users: string[];
  allow_ips: string[];
  used_by: number;
}

export interface AccessListInput {
  name: string;
  /** password vacío al editar = conservar la actual. */
  users: { username: string; password: string }[];
  allow_ips: string[];
}

export interface RedirectHost {
  id: number;
  domains: string[];
  target: string;
  permanent: boolean;
  preserve_path: boolean;
  ssl: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export type RedirectInput = Omit<RedirectHost, 'id' | 'created_at' | 'updated_at'>;

export interface Stream {
  id: number;
  protocol: 'tcp' | 'udp';
  listen_port: number;
  forward_host: string;
  forward_port: number;
  enabled: boolean;
  created_at: string;
  /** null si no se puede saber (Dockfly no corre en Docker). */
  published: boolean | null;
}

export type StreamInput = Pick<Stream, 'protocol' | 'listen_port' | 'forward_host' | 'forward_port' | 'enabled'>;

export interface DNSField {
  env: string;
  label: string;
  secret: boolean;
  optional: boolean;
}

export interface DNSProviderInfo {
  code: string;
  name: string;
  fields: DNSField[];
  help?: string;
}

export interface DNSProvider {
  id: number;
  provider: string;
  zones: string[];
  /** Valores enmascarados (SECRET_MASK); reenviarlos los conserva. */
  env: Record<string, string>;
  created_at: string;
}

export interface CustomCertificate {
  id: number;
  name: string;
  domains: string[];
  issuer: string;
  not_after: string;
  created_at: string;
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
  // Si la sesión caduca mientras se usa el panel, volvemos al login (y de
  // ahí a la misma página).
  if (res.status === 401 && !path.startsWith('/auth/') && !path.startsWith('/setup')) {
    const here = window.location.pathname + window.location.search;
    window.location.href = `/login?redirect=${encodeURIComponent(here)}`;
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.error || res.statusText);
  return data as T;
}

export const api = {
  setupStatus: () => request<{ needs_setup: boolean }>('GET', '/setup'),
  setup: (data: { name: string; email: string; password: string }) => request<User>('POST', '/setup', data),
  login: (data: { email: string; password: string }) => request<LoginResult>('POST', '/auth/login', data),
  login2FA: (data: { ticket: string; code: string }) =>
    request<{ user: User; used_recovery_code: boolean; recovery_codes_left: number }>('POST', '/auth/login/2fa', data),
  changePassword: (current: string, next: string) => request<void>('PUT', '/auth/password', { current, new: next }),
  getInvite: (token: string) =>
    request<{ name: string; email: string; new: boolean }>('GET', `/invite/${encodeURIComponent(token)}`),
  acceptInvite: (token: string, password: string) =>
    request<LoginResult>('POST', `/invite/${encodeURIComponent(token)}`, { password }),
  listTokens: () => request<APIToken[]>('GET', '/tokens'),
  createToken: (data: { name: string; role: Role; expires_days: number }) =>
    request<{ token: string; api_token: APIToken }>('POST', '/tokens', data),
  deleteToken: (id: number) => request<void>('DELETE', `/tokens/${id}`),
  listUsers: () => request<UserEntry[]>('GET', '/users'),
  inviteUser: (data: { name: string; email: string; role: Role }) =>
    request<PasswordLink & { user: UserEntry }>('POST', '/users', data),
  updateUser: (id: number, data: { name: string; role: Role }) => request<UserEntry>('PUT', `/users/${id}`, data),
  deleteUser: (id: number) => request<void>('DELETE', `/users/${id}`),
  userLink: (id: number) => request<PasswordLink>('POST', `/users/${id}/link`),
  get2FA: () => request<{ enabled: boolean; recovery_codes_left: number }>('GET', '/auth/2fa'),
  setup2FA: (password: string) => request<TwoFactorSetup>('POST', '/auth/2fa/setup', { password }),
  enable2FA: (code: string) => request<{ recovery_codes: string[] }>('POST', '/auth/2fa/enable', { code }),
  disable2FA: (password: string) => request<void>('POST', '/auth/2fa/disable', { password }),
  regenerateRecoveryCodes: (password: string) =>
    request<{ recovery_codes: string[] }>('POST', '/auth/2fa/recovery-codes', { password }),
  logout: () => request<void>('POST', '/auth/logout'),
  me: () => request<User>('GET', '/auth/me'),
  status: () => request<Status>('GET', '/status'),
  certificates: () => request<CertStatus[]>('GET', '/certificates'),
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
  listPreviews: (siteId: number) => request<Preview[]>('GET', `/sites/${siteId}/previews`),
  deletePreview: (siteId: number, preview: string) =>
    request<void>('DELETE', `/sites/${siteId}/previews/${encodeURIComponent(preview)}`),
  uploadDeployment,

  listConnections: () => request<GitConnection[]>('GET', '/git/connections'),
  createConnection: (data: { provider: GitProvider; base_url: string; token: string }) =>
    request<GitConnection>('POST', '/git/connections', data),
  deleteConnection: (id: number) => request<void>('DELETE', `/git/connections/${id}`),
  listRepos: (id: number, q = '') => request<Repo[]>('GET', `/git/connections/${id}/repos?q=${encodeURIComponent(q)}`),
  listBranches: (id: number, repo: string) =>
    request<string[]>('GET', `/git/connections/${id}/branches?repo=${encodeURIComponent(repo)}`),

  dnsCatalog: () => request<DNSProviderInfo[]>('GET', '/dns-providers/catalog'),
  listDNSProviders: () => request<DNSProvider[]>('GET', '/dns-providers'),
  saveDNSProvider: (data: { provider: string; zones: string[]; env: Record<string, string> }) =>
    request<{ id: number }>('PUT', '/dns-providers', data),
  deleteDNSProvider: (id: number) => request<void>('DELETE', `/dns-providers/${id}`),
  listCustomCerts: () => request<CustomCertificate[]>('GET', '/custom-certificates'),
  uploadCustomCert: (data: { name: string; cert: string; key: string }) =>
    request<CustomCertificate>('POST', '/custom-certificates', data),
  deleteCustomCert: (id: number) => request<void>('DELETE', `/custom-certificates/${id}`),

  listStreams: () => request<Stream[]>('GET', '/streams'),
  createStream: (data: StreamInput) => request<{ traefik_restarted: boolean }>('POST', '/streams', data),
  updateStream: (id: number, data: StreamInput) => request<{ traefik_restarted: boolean }>('PUT', `/streams/${id}`, data),
  deleteStream: (id: number) => request<{ traefik_restarted: boolean }>('DELETE', `/streams/${id}`),

  listRedirects: () => request<RedirectHost[]>('GET', '/redirects'),
  createRedirect: (data: RedirectInput) => request<RedirectHost>('POST', '/redirects', data),
  updateRedirect: (id: number, data: RedirectInput) => request<RedirectHost>('PUT', `/redirects/${id}`, data),
  deleteRedirect: (id: number) => request<void>('DELETE', `/redirects/${id}`),

  listAccessLists: () => request<AccessList[]>('GET', '/access-lists'),
  createAccessList: (data: AccessListInput) => request<{ id: number }>('POST', '/access-lists', data),
  updateAccessList: (id: number, data: AccessListInput) => request<{ id: number }>('PUT', `/access-lists/${id}`, data),
  deleteAccessList: (id: number) => request<void>('DELETE', `/access-lists/${id}`),

  listChannels: () => request<NotificationChannel[]>('GET', '/notifications'),
  createChannel: (data: ChannelInput) => request<NotificationChannel>('POST', '/notifications', data),
  updateChannel: (id: number, data: ChannelInput) => request<NotificationChannel>('PUT', `/notifications/${id}`, data),
  deleteChannel: (id: number) => request<void>('DELETE', `/notifications/${id}`),
  testChannel: (id: number) => request<void>('POST', `/notifications/${id}/test`),

  listApps: () => request<App[]>('GET', '/apps'),
  getApp: (id: number) => request<{ app: App; deployments: AppDeployment[] }>('GET', `/apps/${id}`),
  createApp: (data: AppInput) =>
    request<{ app: App; deployment: AppDeployment | null; warning: string }>('POST', '/apps', data),
  updateApp: (id: number, data: AppInput) => request<{ app: App; warning: string }>('PUT', `/apps/${id}`, data),
  deleteApp: (id: number, removeData: boolean) => request<void>('DELETE', `/apps/${id}${removeData ? '?data=1' : ''}`),
  getAppEnv: (id: number) => request<Record<string, string>>('GET', `/apps/${id}/env`),
  deployApp: (id: number) => request<AppDeployment>('POST', `/apps/${id}/deploy`),
  redeploy: (id: number, deploymentId: number) =>
    request<AppDeployment>('POST', `/apps/${id}/deployments/${deploymentId}/redeploy`),
  appAction: (id: number, action: 'stop' | 'start' | 'restart') => request<void>('POST', `/apps/${id}/${action}`),
  cancelAppDeployment: (id: number) => request<void>('POST', `/app-deployments/${id}/cancel`),
  appDeploymentLogsURL: (id: number) => `/api/app-deployments/${id}/logs`,
  appLogsURL: (id: number) => `/api/apps/${id}/logs`,

  getSettings: () => request<Settings>('GET', '/settings'),
  /** Guarda solo los ajustes indicados (preview_access_list_id: 0 = ninguna). */
  putSettings: (data: Partial<Omit<Settings, 'preview_ssl'>>) =>
    request<{ settings: Settings; warnings: string[] | null }>('PUT', '/settings', data),

  listGitApps: () => request<{ github: GitHubApp[]; gitlab: GitLabApp[] }>('GET', '/git/apps'),
  startGitHubManifest: (data: { panel_url: string; organization: string; enterprise_url: string }) =>
    request<{ action: string; manifest: string; webhook: boolean }>('POST', '/git/github/manifest', data),
  githubInstallURL: (appId: number, panelURL: string) =>
    request<{ url: string }>('POST', `/git/github/apps/${appId}/install-url`, { panel_url: panelURL }),
  deleteGitHubApp: (id: number) => request<void>('DELETE', `/git/github/apps/${id}`),
  putGitLabApp: (data: { web_url: string; client_id: string; client_secret: string }) =>
    request<GitLabApp>('PUT', '/git/gitlab/apps', data),
  deleteGitLabApp: (id: number) => request<void>('DELETE', `/git/gitlab/apps/${id}`),
  gitlabAuthorize: (appId: number, panelURL: string) =>
    request<{ url: string }>('POST', '/git/gitlab/authorize', { app_id: appId, panel_url: panelURL }),

  putSiteGit: (id: number, data: SiteGitInput) =>
    request<{ site: Site; webhook_error: string }>('PUT', `/sites/${id}/git`, data),
  deleteSiteGit: (id: number) => request<void>('DELETE', `/sites/${id}/git`),
  getSiteEnv: (id: number) => request<Record<string, string>>('GET', `/sites/${id}/git/env`),
  listBuilds: (id: number) => request<Build[]>('GET', `/sites/${id}/builds`),
  triggerBuild: (id: number) => request<Build>('POST', `/sites/${id}/builds`),
  clearBuildCache: (id: number) => request<void>('DELETE', `/sites/${id}/cache`),
  getBuild: (id: number) => request<Build>('GET', `/builds/${id}`),
  cancelBuild: (id: number) => request<void>('POST', `/builds/${id}/cancel`),
  buildLogsURL: (id: number) => `/api/builds/${id}/logs`,
};

/** Sube un despliegue: un .zip o los archivos de una carpeta. */
function uploadDeployment(siteId: number, payload: UploadPayload, onProgress?: (p: number) => void) {
  const form = new FormData();
  if ('archive' in payload) {
    form.append('archive', payload.archive);
  } else {
    // El servidor lee la ruta del nombre del campo: el nombre del archivo
    // se recorta a su base al viajar en multipart.
    for (const { path, file } of payload.files) form.append(`file:${path}`, file, file.name);
  }
  return upload<Deployment>(`/api/sites/${siteId}/deployments`, form, onProgress);
}

/** Sube una copia de seguridad para restaurarla. */
export function restoreBackup(file: File, onProgress?: (p: number) => void) {
  const form = new FormData();
  form.append('backup', file);
  return upload<{ message: string }>('/api/backup/restore', form, onProgress);
}

/**
 * Envía un formulario multipart. Usa XMLHttpRequest porque fetch todavía no
 * informa del progreso de subida. onProgress recibe un valor entre 0 y 1.
 */
function upload<T>(url: string, form: FormData, onProgress?: (p: number) => void) {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    // Cabecera propia: un formulario de otro sitio no puede añadirla (CSRF).
    xhr.setRequestHeader('X-Dockfly-Upload', '1');
    xhr.responseType = 'json';
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve(xhr.response as T);
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
