// Types mirroring the Go API responses. The panel returns camelCase JSON, so
// every field is spelled the way the UI consumes it.

export type Role = "admin" | "member";

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
  totpEnabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface Session {
  authenticated: boolean;
  user?: User;
  version?: string;
  /** Effective Let's Encrypt registration address. */
  acmeEmail?: string;
  /** True while the instance has no accounts, so the UI can offer sign-up. */
  needsBootstrap?: boolean;
}

export interface Project {
  id: string;
  name: string;
  description: string;
  createdAt: string;
  updatedAt: string;
}

export interface Environment {
  id: string;
  projectId: string;
  name: string;
  createdAt: string;
}

export interface Node {
  id: string;
  name: string;
  description: string;
  address: string;
  sshPort: number;
  sshUser: string;
  sshKeyId?: string;
  /** SHA256 SSH host key pinned on first use; empty means trust-on-first-use. */
  hostKeyFingerprint?: string;
  status: "pending" | "online" | "offline" | "error" | "installing";
  os: string;
  kernel: string;
  arch: string;
  cpus: number;
  memoryBytes: number;
  dockerVersion: string;
  bootTime?: string;
  lastSeenAt?: string;
  errorMessage?: string;
  createdAt: string;
  updatedAt: string;
}

export interface Application {
  id: string;
  environmentId: string;
  nodeId?: string;
  name: string;
  description: string;
  gitRepository: string;
  gitBranch: string;
  buildPack: string;
  dockerfile: string;
  baseDirectory: string;
  port: number;
  healthCheck: string;
  status: string;
  fqdn: string;
  imageTag: string;
  /** "hostPort:containerPort" pairs, comma separated. */
  portMappings: string;
  /** Default for TLS domains: plain HTTP redirects to https. */
  redirectHttps: boolean;
  /** Fraction of one core; zero keeps the engine default. */
  cpuLimit: number;
  /** Bytes; zero keeps the engine default. */
  memoryLimit: number;
  /** Panel-wide protection is inherited unless this is switched off. */
  resourceGuardEnabled: boolean;
  /** Empty string inherits the panel policy. */
  resourceGuardCpuAction: "" | "notify" | "throttle" | "stop";
  resourceGuardMemoryAction: "" | "notify" | "stop";
  /** 0 inherits the panel threshold; otherwise a percentage. */
  resourceGuardCpuPercent: number;
  resourceGuardMemoryPercent: number;
  preDeployCommand: string;
  postDeployCommand: string;
  /** Set when the repository is deployed through a GitHub App installation. */
  githubAppId?: string;
  githubInstallationId?: number;
  repositoryFullName?: string;
  autoDeploy: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface GitHubApp {
  id: string;
  name: string;
  slug: string;
  appId: number;
  clientId: string;
  htmlUrl: string;
  baseUrl: string;
  organization: string;
  configured: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface GitHubInstallation {
  id: string;
  githubAppId: string;
  appName: string;
  installationId: number;
  accountLogin: string;
  accountType: string;
  repositorySelection: string;
  createdAt: string;
  updatedAt: string;
}

export interface GitHubRepository {
  id: number;
  fullName: string;
  private: boolean;
  archived: boolean;
  defaultBranch: string;
  cloneUrl: string;
  htmlUrl: string;
  updatedAt: string;
}

export interface GitHubRepositoryList {
  totalCount: number;
  repositories: GitHubRepository[];
}

export interface GitHubManifest {
  postUrl: string;
  manifest: Record<string, unknown>;
  webhookUrl: string;
  panelUrl: string;
}

export interface Deployment {
  id: string;
  applicationId: string;
  status: "queued" | "building" | "pushing" | "deploying" | "success" | "failed" | "canceled";
  trigger: string;
  commitSha: string;
  commitMessage: string;
  imageTag: string;
  logPath: string;
  error: string;
  queuedAt: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface Database {
  id: string;
  environmentId: string;
  nodeId?: string;
  name: string;
  kind: string;
  version: string;
  status: string;
  publicPort: number;
  createdAt: string;
  updatedAt: string;
}

export interface Service {
  id: string;
  environmentId: string;
  nodeId?: string;
  name: string;
  kind: string;
  compose: string;
  status: string;
  createdAt: string;
  updatedAt: string;
}

export interface EnvVar {
  id: string;
  target: string;
  targetId: string;
  key: string;
  isSecret: boolean;
  isBuild: boolean;
  createdAt: string;
}

export interface Domain {
  id: string;
  applicationId: string;
  host: string;
  tlsEnabled: boolean;
  certStatus: string;
  /** Router path prefix, e.g. /api/v3. */
  path: string;
  /** Internal port override; zero uses the application's port. */
  port: number;
  redirectHttps: boolean;
  /** "", "www" or "non-www": canonical host redirect. */
  wwwRedirect: string;
  searchIndexing: boolean;
  /** "matches", "mismatch" or "" when never checked. */
  dnsStatus: string;
  dnsCheckedAt?: string;
  createdAt: string;
}

/** Result of the read-only live TLS inspection endpoint. */
export interface DomainCertificate {
  host: string;
  valid?: boolean;
  trusted?: boolean;
  subject?: string;
  issuer?: string;
  issuerCommonName?: string;
  notBefore?: string;
  notAfter?: string;
  dnsNames?: string[];
  error?: string;
  verifyError?: string;
}

export interface Volume {
  id: string;
  target: string;
  targetId: string;
  name: string;
  mountPath: string;
  readOnly: boolean;
  createdAt: string;
}

export interface ContainerInfo {
  id: string;
  name: string;
  image: string;
  state: "created" | "running" | "paused" | "restarting" | "removing" | "exited" | "dead" | string;
  status: string;
  labels: Record<string, string>;
  ports: string[];
  created: number;
}

export interface NodeMetrics {
  id: string;
  nodeId: string;
  cpuPercent: number;
  memUsed: number;
  memTotal: number;
  diskUsed: number;
  diskTotal: number;
  netRx: number;
  netTx: number;
  recordedAt: string;
}

/** One of the four Docker disk-usage categories, in bytes. */
export interface DockerUsageReport {
  total: number;
  images: number;
  containers: number;
  volumes: number;
  buildCache: number;
}

/** A large object behind the categories (an image, a volume, a container). */
export interface DockerUsageItem {
  name: string;
  kind: "image" | "container" | "volume";
  bytes: number;
}

/** What the build sandbox's own Docker daemon stores, when it is reachable. */
export interface DockerSandboxUsage {
  total: number;
  images: number;
  containers: number;
  volumes: number;
  buildCache: number;
}

export interface DockerDiskUsage {
  engine: DockerUsageReport;
  top: DockerUsageItem[];
  sandbox?: DockerSandboxUsage;
  sampledAt: string;
}

/** What the built-in cleanup pass reclaimed, in bytes. */
export interface DockerCleanupResult {
  reclaimed: number;
  imagesRemoved: number;
  orphanedContainers?: number;
  volumesRemoved?: number;
  networksRemoved?: number;
  sandbox: boolean;
}

export type DockerCleanupTrigger = "schedule" | "threshold";
export type DockerCleanupImages = "keep" | "remove";

/** Server-scoped Docker cleanup schedule shown on the local server page. */
export interface DockerCleanupSettings {
  enabled: boolean;
  schedule: string;
  trigger: DockerCleanupTrigger;
  thresholdPercent: number;
  unusedVolumes: boolean;
  unusedNetworks: boolean;
  applicationImages: DockerCleanupImages;
  updatedAt: string;
}

export interface DockerCleanupRun {
  id: string;
  trigger: "manual" | "schedule";
  status: "running" | "success" | "failed";
  startedAt: string;
  finishedAt?: string;
  durationMs: number;
  reclaimedBytes: number;
  imagesRemoved: number;
  orphanedContainers: number;
  volumesRemoved: number;
  networksRemoved: number;
  error?: string;
}

export type ResourceGuardAction = "notify" | "throttle" | "stop";
export type ResourceGuardMetric = "cpu" | "memory" | "host_memory";

/** Panel-wide protection policy; per-app overrides live on Application. */
export interface ResourceGuardSettings {
  enabled: boolean;
  cpuEnabled: boolean;
  cpuPercent: number;
  cpuDurationSeconds: number;
  cpuAction: "notify" | "throttle" | "stop";
  cpuThrottlePercent: number;
  memoryEnabled: boolean;
  memoryPercent: number;
  memoryDurationSeconds: number;
  memoryAction: "notify" | "stop";
  hostMemoryEnabled: boolean;
  hostMemoryPercent: number;
  hostMemoryDurationSeconds: number;
  graceSeconds: number;
  cooldownSeconds: number;
  updatedAt: string;
}

export interface ResourceGuardEvent {
  id: string;
  applicationId: string;
  applicationName: string;
  nodeId: string;
  containerId: string;
  containerName: string;
  metric: ResourceGuardMetric;
  action: ResourceGuardAction;
  status: "success" | "failed";
  observedPercent: number;
  thresholdPercent: number;
  durationSeconds: number;
  usageBytes?: number;
  limitBytes?: number;
  message: string;
  error?: string;
  createdAt: string;
}

export interface APIToken {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  createdAt: string;
}

export interface Stats {
  projects: number;
  applications: number;
  databases: number;
  services: number;
  nodes: number;
  onlineNodes: number;
  deployments: number;
  pendingJobs: number;
  users: number;
}

export interface Job {
  id: string;
  kind: string;
  status: string;
  attempts: number;
  maxAttempts: number;
  runAt: string;
  lastError?: string;
  createdAt: string;
  finishedAt?: string;
}

export const STATUS_TONE: Record<string, "ok" | "warn" | "danger" | "neutral"> = {
  running: "ok",
  online: "ok",
  success: "ok",
  healthy: "ok",
  active: "ok",
  deploying: "warn",
  building: "warn",
  pushing: "warn",
  queued: "warn",
  installing: "warn",
  pending: "warn",
  retrying: "warn",
  idle: "neutral",
  stopped: "neutral",
  canceled: "neutral",
  done: "ok",
  error: "danger",
  failed: "danger",
  unhealthy: "danger",
  offline: "danger",
};

export const toneFor = (status: string): "ok" | "warn" | "danger" | "neutral" =>
  STATUS_TONE[status] ?? "neutral";

export const BUILD_PACKS = [
  { value: "dockerfile", label: "Dockerfile" },
  { value: "compose", label: "Docker Compose" },
  { value: "nixpacks", label: "Nixpacks (auto detect)" },
  { value: "railpack", label: "Railpack (auto detect)" },
  { value: "image", label: "Prebuilt image" },
];

export const DATABASE_KINDS = [
  { value: "postgres", label: "PostgreSQL" },
  { value: "mysql", label: "MySQL" },
  { value: "mariadb", label: "MariaDB" },
  { value: "redis", label: "Redis" },
  { value: "mongodb", label: "MongoDB" },
];
