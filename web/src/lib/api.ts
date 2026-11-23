// Minimal client for the Dockfly API. The types mirror the Go structs
// (internal/store, internal/docker, internal/traefik).

import { i18n } from '@lingui/core';
import { msg, t } from '@lingui/core/macro';

import { apiErrors } from '@/lib/api-errors.gen';

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
  /** An invited user who has not chosen a password yet. */
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

export type DBEngine = 'postgres' | 'mysql' | 'redis';

export interface DBBackupPolicy {
  enabled: boolean;
  interval_hours: number;
  keep: number;
}

export interface DBBackup {
  id: number;
  app_id: number;
  status: 'running' | 'success' | 'failed';
  trigger: 'schedule' | 'manual';
  engine: DBEngine;
  location: string;
  object_key: string;
  size: number;
  error: string;
  created_at: string;
  finished_at: string | null;
}

export interface DBBackups {
  /** '' when the app is not a known database. */
  engine: DBEngine | '';
  can_restore: boolean;
  policy: DBBackupPolicy;
  backups: DBBackup[];
  location: string;
}

export interface BackupStorage {
  type: 'local' | 's3';
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key: string;
  secret_key: string;
}

export interface TrafficPoint {
  t: string;
  requests: number;
  s4xx: number;
  s5xx: number;
  p50_ms: number;
  p95_ms: number;
}

export interface TrafficTotals {
  requests: number;
  s4xx: number;
  s5xx: number;
  p50_ms: number;
  p95_ms: number;
  avg_ms: number;
}

export type TrafficRange = '1h' | '24h' | '7d';

/** The last 24 h of a proxy host, site or app; spark = requests per hour. */
export interface TrafficSummary extends TrafficTotals {
  spark: number[];
}

export interface PasswordLink {
  link: string;
  expires_at: string;
}

/** The login response: the session, or the ticket for the second step. */
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
  /** Custom locations: part of the domain (/api) goes to a different target. */
  locations: ProxyLocation[];
  created_at: string;
  updated_at: string;
}

export type LocationAccess = 'inherit' | 'public' | 'list';

export interface ProxyLocation {
  path: string;
  target_type: TargetType;
  forward_scheme: Scheme;
  forward_host: string;
  forward_port: number;
  /** Strips the path before forwarding (/api/users → /users). */
  strip_prefix: boolean;
  access: LocationAccess;
  access_list_id: number | null;
}

/** The editable fields of a proxy host (what the API accepts on create/update). */
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
  /** The preview's branch ('' = production). */
  preview: string;
}

/** The latest deployment of a branch published as a preview. */
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
  /** There is a wildcard certificate (DNS zone or custom) for the previews. */
  preview_ssl: boolean;
}

/** Build configuration of a site linked to a repository. */
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
  /** null = keep the current variables. */
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
  trigger: 'push' | 'manual' | 'hook';
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
  /** Custom locations: /api → a container; the site serves everything else. */
  locations: ProxyLocation[];
  active_deployment_id: number | null;
  active_deployment?: Deployment;
  created_at: string;
  updated_at: string;
}

export type SiteInput = Pick<
  Site,
  'name' | 'domains' | 'ssl' | 'force_https' | 'hsts' | 'spa' | 'previews' | 'access_list_id' | 'locations'
>;

/** What gets uploaded: a .zip, or a folder's files with their relative paths. */
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
  | 'container_recovered'
  | 'backup_failed'
  | 'server_down'
  | 'server_up'
  | 'resource_high'
  | 'resource_normal'
  | 'job_failed';

export interface NotificationChannel {
  id: number;
  type: ChannelType;
  name: string;
  events: NotificationEvent[];
  enabled: boolean;
  last_error: string;
  last_sent_at: string | null;
  created_at: string;
  /** The secret fields arrive masked (SECRET_MASK). */
  config: Record<string, unknown>;
}

export interface ChannelInput {
  type: ChannelType;
  name: string;
  events: NotificationEvent[];
  enabled: boolean;
  config: Record<string, unknown>;
}

/** The value the server masks secrets with; sending it back keeps them. */
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
  /** The remote server it runs on (null = the panel's own). */
  server_id: number | null;
  /** The container's real state (running, exited, restarting…, offline when its server is not connected). */
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
  /** null = keep the current variables. */
  env: Record<string, string> | null;
  volumes: AppVolume[];
  command: string;
  memory_mb: number;
  cpus: number;
  health_path: string;
  access_list_id: number | null;
  deploy?: boolean;
  /** Only when creating it. */
  server_id?: number | null;
}

export interface AppJobInput {
  command: string;
  /** A five-field cron expression, in UTC. */
  schedule: string;
  enabled: boolean;
}

/** An app's scheduled task. last_exit_code: null = it has never finished; -1 = it never ran. */
export interface AppJob extends AppJobInput {
  id: number;
  last_run_at: string | null;
  last_exit_code: number | null;
  last_output: string;
}

/** An app container's usage; cpu_percent is across all of the server's cores. */
export interface AppUsage {
  cpu_percent: number;
  mem_used: number;
  mem_limit: number;
}

/** A server's resources; sizes are in bytes. */
export interface ServerStats {
  cpu_percent: number;
  cores: number;
  load1: number;
  load5: number;
  load15: number;
  mem_total: number;
  mem_used: number;
  swap_total: number;
  swap_used: number;
  disk_total: number;
  disk_used: number;
  uptime_seconds: number;
}

export interface RemoteServer {
  id: number;
  name: string;
  online: boolean;
  apps: number;
  last_seen_at: string | null;
  last_addr: string;
  version: string;
  created_at: string;
}

/** Updating Dockfly from the panel. */
export interface UpdateInfo {
  current: string;
  latest: { version: string; url: string; notes: string } | null;
  available: boolean;
  can_update: boolean;
  reason?: 'not_in_docker' | 'dev_version' | 'pinned_tag' | 'no_socket';
  state: '' | 'backup' | 'pulling' | 'restarting' | 'failed';
  error?: string;
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
  /** An empty password when editing = keep the current one. */
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
  /** null when it cannot be known (Dockfly is not running in Docker). */
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
  /** Masked values (SECRET_MASK); sending them back keeps them. */
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

/**
 * An API error. code identifies the error (the text for each language lives
 * in api-errors.gen.ts, generated from Go) and params carries its data;
 * message is the fallback text the server sends, in English.
 */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code = '',
    public params: Record<string, unknown> = {},
  ) {
    super(message);
  }
}

/** A coded warning the API returns alongside a successful response (translated like an error). */
export interface Problem {
  error: string;
  code: string;
  params?: Record<string, unknown>;
}

/** Builds the ApiError from the body of an error response. */
function apiError(status: number, data: { error?: string; code?: string; params?: Record<string, unknown> } | null, fallback: string) {
  return new ApiError(status, data?.error || fallback, data?.code ?? '', data?.params ?? {});
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const opts: RequestInit = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }
  const res = await fetch('/api' + path, opts);
  // If the session expires while the panel is in use, we go back to the login
  // (and from there back to the same page).
  if (res.status === 401 && !path.startsWith('/auth/') && !path.startsWith('/setup')) {
    const here = window.location.pathname + window.location.search;
    window.location.href = `/login?redirect=${encodeURIComponent(here)}`;
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw apiError(res.status, data, res.statusText);
  return data as T;
}

export const api = {
  setupStatus: () => request<{ needs_setup: boolean; setup_code_required: boolean }>('GET', '/setup'),
  setup: (data: { name: string; email: string; password: string; setup_token?: string }) =>
    request<User>('POST', '/setup', data),
  login: (data: { email: string; password: string }) => request<LoginResult>('POST', '/auth/login', data),
  login2FA: (data: { ticket: string; code: string }) =>
    request<{ user: User; used_recovery_code: boolean; recovery_codes_left: number }>('POST', '/auth/login/2fa', data),
  changePassword: (current: string, next: string) => request<void>('PUT', '/auth/password', { current, new: next }),
  getInvite: (token: string) =>
    request<{ name: string; email: string; new: boolean }>('GET', `/invite/${encodeURIComponent(token)}`),
  acceptInvite: (token: string, password: string) =>
    request<LoginResult>('POST', `/invite/${encodeURIComponent(token)}`, { password }),
  getBackupStorage: () => request<{ config: BackupStorage; location: string }>('GET', '/backup-storage'),
  putBackupStorage: (data: BackupStorage) =>
    request<{ config: BackupStorage; location: string }>('PUT', '/backup-storage', data),
  listDBBackups: (appId: number) => request<DBBackups>('GET', `/apps/${appId}/backups`),
  startDBBackup: (appId: number) => request<DBBackup>('POST', `/apps/${appId}/backups`),
  putDBBackupPolicy: (appId: number, policy: DBBackupPolicy) =>
    request<DBBackupPolicy>('PUT', `/apps/${appId}/backups/policy`, policy),
  restoreDBBackup: (appId: number, id: number) => request<void>('POST', `/apps/${appId}/backups/${id}/restore`),
  deleteDBBackup: (appId: number, id: number) => request<void>('DELETE', `/apps/${appId}/backups/${id}`),
  dbBackupURL: (appId: number, id: number) => `/api/apps/${appId}/backups/${id}/download`,
  traffic: (owner: string, range: TrafficRange) =>
    request<{ points: TrafficPoint[]; totals: TrafficTotals; step_seconds: number }>(
      'GET',
      `/traffic/${owner}?range=${range}`,
    ),
  trafficSummary: () => request<Record<string, TrafficSummary>>('GET', '/traffic'),
  listServers: () => request<RemoteServer[]>('GET', '/servers'),
  /** id 0 = the panel's own server. */
  serverStats: (id: number) => request<ServerStats>('GET', `/servers/${id}/stats`),
  createServer: (name: string) =>
    request<{ server: RemoteServer; token: string; command: string; warning: string }>('POST', '/servers', { name }),
  rotateServerToken: (id: number) =>
    request<{ token: string; command: string; warning: string }>('POST', `/servers/${id}/token`),
  deleteServer: (id: number) => request<void>('DELETE', `/servers/${id}`),
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
  disable2FA: (password: string, code: string) =>
    request<void>('POST', '/auth/2fa/disable', { password, code }),
  regenerateRecoveryCodes: (password: string) =>
    request<{ recovery_codes: string[] }>('POST', '/auth/2fa/recovery-codes', { password }),
  logout: () => request<void>('POST', '/auth/logout'),
  me: () => request<User>('GET', '/auth/me'),
  status: () => request<Status>('GET', '/status'),
  getUpdate: () => request<UpdateInfo>('GET', '/system/update'),
  startUpdate: (password: string) => request<UpdateInfo>('POST', '/system/update', { password }),
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
    request<{ app: App; deployment: AppDeployment | null; deploy_error: string; webhook_warning: Problem | null }>(
      'POST',
      '/apps',
      data,
    ),
  updateApp: (id: number, data: AppInput) =>
    request<{ app: App; webhook_warning: Problem | null }>('PUT', `/apps/${id}`, data),
  deleteApp: (id: number, removeData: boolean) => request<void>('DELETE', `/apps/${id}${removeData ? '?data=1' : ''}`),
  getAppEnv: (id: number) => request<Record<string, string>>('GET', `/apps/${id}/env`),
  deployApp: (id: number) => request<AppDeployment>('POST', `/apps/${id}/deploy`),
  redeploy: (id: number, deploymentId: number) =>
    request<AppDeployment>('POST', `/apps/${id}/deployments/${deploymentId}/redeploy`),
  appExec: (id: number, command: string) =>
    request<{ exit_code: number; output: string; truncated: boolean; no_shell?: boolean }>('POST', `/apps/${id}/exec`, { command }),
  listAppJobs: (id: number) => request<AppJob[]>('GET', `/apps/${id}/jobs`),
  saveAppJob: (id: number, job: AppJobInput, jobId?: number) =>
    request<AppJob>(jobId ? 'PUT' : 'POST', `/apps/${id}/jobs${jobId ? `/${jobId}` : ''}`, job),
  deleteAppJob: (id: number, jobId: number) => request<void>('DELETE', `/apps/${id}/jobs/${jobId}`),
  runAppJob: (id: number, jobId: number) => request<void>('POST', `/apps/${id}/jobs/${jobId}/run`),
  appStats: (id: number) => request<AppUsage>('GET', `/apps/${id}/stats`),
  appAction: (id: number, action: 'stop' | 'start' | 'restart') => request<void>('POST', `/apps/${id}/${action}`),
  cancelAppDeployment: (id: number) => request<void>('POST', `/app-deployments/${id}/cancel`),
  appDeploymentLogsURL: (id: number) => `/api/app-deployments/${id}/logs`,
  appLogsURL: (id: number) => `/api/apps/${id}/logs`,

  getSettings: () => request<Settings>('GET', '/settings'),
  /** Saves only the settings given (preview_access_list_id: 0 = none). */
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
    request<{ site: Site; webhook_warning: Problem | null }>('PUT', `/sites/${id}/git`, data),
  deleteSiteGit: (id: number) => request<void>('DELETE', `/sites/${id}/git`),
  getSiteEnv: (id: number) => request<Record<string, string>>('GET', `/sites/${id}/git/env`),
  listBuilds: (id: number) => request<Build[]>('GET', `/sites/${id}/builds`),
  triggerBuild: (id: number) => request<Build>('POST', `/sites/${id}/builds`),
  clearBuildCache: (id: number) => request<void>('DELETE', `/sites/${id}/cache`),
  /** token "" = the site has no deploy hook. */
  getDeployHook: (id: number) => request<{ token: string }>('GET', `/sites/${id}/deploy-hook`),
  /** Creates the deploy hook, or regenerates it (the previous URL stops working). */
  createDeployHook: (id: number) => request<{ token: string }>('POST', `/sites/${id}/deploy-hook`),
  deleteDeployHook: (id: number) => request<{ token: string }>('DELETE', `/sites/${id}/deploy-hook`),
  cancelBuild: (id: number) => request<void>('POST', `/builds/${id}/cancel`),
  buildLogsURL: (id: number) => `/api/builds/${id}/logs`,
};

/** Uploads a deployment: a .zip, or the files of a folder. */
function uploadDeployment(siteId: number, payload: UploadPayload, onProgress?: (p: number) => void) {
  const form = new FormData();
  if ('archive' in payload) {
    form.append('archive', payload.archive);
  } else {
    // The server reads the path from the field name: the file name itself is
    // trimmed to its basename while travelling in a multipart body.
    for (const { path, file } of payload.files) form.append(`file:${path}`, file, file.name);
  }
  return upload<Deployment>(`/api/sites/${siteId}/deployments`, form, onProgress);
}

/**
 * Uploads a backup in order to restore it. It is confirmed with the
 * administrator's password or, on a fresh install, with the setup code.
 */
export function restoreBackup(
  file: File,
  confirm: { password?: string; setup_token?: string; encryption_password?: string },
  onProgress?: (p: number) => void,
) {
  const form = new FormData();
  // The confirmation fields go before the file (the server reads them in order).
  if (confirm.password) form.append('password', confirm.password);
  if (confirm.setup_token) form.append('setup_token', confirm.setup_token);
  if (confirm.encryption_password) form.append('encryption_password', confirm.encryption_password);
  form.append('backup', file);
  return upload<{ message: string }>('/api/backup/restore', form, onProgress);
}

/**
 * Downloads the full backup. With a password, the server encrypts it (the
 * password travels in a header, not in the URL). Returns the blob to save.
 */
export async function downloadBackup(password?: string): Promise<Blob> {
  const res = await fetch('/api/backup', {
    headers: password ? { 'X-Backup-Password': password } : undefined,
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.blob();
}

/**
 * Sends a multipart form. It uses XMLHttpRequest because fetch still does not
 * report upload progress. onProgress receives a value between 0 and 1.
 */
function upload<T>(url: string, form: FormData, onProgress?: (p: number) => void) {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    // A custom header: a form from another site cannot add it (CSRF).
    xhr.setRequestHeader('X-Dockfly-Upload', '1');
    xhr.responseType = 'json';
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve(xhr.response as T);
      else reject(apiError(xhr.status, xhr.response, xhr.statusText));
    };
    xhr.onerror = () => reject(new ApiError(0, t`network error during upload`));
    xhr.send(form);
  });
}

/** A readable message for any error caught in a `catch`. */
// Parameter values that are translated as well: the resource kind in
// domain_in_use ("…already used by another {kind}"; in Spanish it takes a
// determiner: "otra app").
const paramLabels: Record<string, Record<string, ReturnType<typeof msg>>> = {
  kind: {
    proxy_host: msg({ context: 'domain owner', message: 'proxy host' }),
    site: msg({ context: 'domain owner', message: 'site' }),
    app: msg({ context: 'domain owner', message: 'app' }),
    redirect: msg({ context: 'domain owner', message: 'redirect' }),
  },
};

/** An error's message, in the active language when the API identifies it with a code. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError && e.code && apiErrors[e.code]) {
    const values: Record<string, unknown> = { ...e.params };
    for (const [key, labels] of Object.entries(paramLabels)) {
      const label = labels[String(values[key])];
      if (label) values[key] = i18n._(label);
    }
    return i18n._({ ...apiErrors[e.code], values });
  }
  return e instanceof Error ? e.message : String(e);
}

/** The translated text of an API warning ("" when there is no warning). */
export function problemMessage(p: Problem | null | undefined): string {
  return p ? errorMessage(new ApiError(0, p.error, p.code, p.params ?? {})) : '';
}
