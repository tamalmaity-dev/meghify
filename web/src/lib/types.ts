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
  /** Fraction of one core; zero keeps the engine default. */
  cpuLimit: number;
  /** Bytes; zero keeps the engine default. */
  memoryLimit: number;
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
  createdAt: string;
}

export interface Volume {
  id: string;
  target: string;
  targetId: string;
  name: string;
  hostPath: string;
  mountPath: string;
  readOnly: boolean;
  createdAt: string;
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
