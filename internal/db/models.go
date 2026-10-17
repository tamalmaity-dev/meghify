package db

import "time"

// Roles control what a member may do in the panel.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Node lifecycle states.
const (
	NodePending = "pending"
	NodeOnline  = "online"
	NodeOffline = "offline"
	NodeError   = "error"
	NodeInstall = "installing"
)

// Resource lifecycle states shared by applications, services and databases.
const (
	StatusIdle      = "idle"
	StatusDeploying = "deploying"
	StatusRunning   = "running"
	StatusStopped   = "stopped"
	StatusError     = "error"
	StatusUnhealthy = "unhealthy"
)

// Build packs supported by the deploy pipeline.
const (
	BuildDockerfile = "dockerfile"
	BuildCompose    = "compose"
	BuildNixpacks   = "nixpacks"
	BuildRailpack   = "railpack"
	BuildStatic     = "static"
	BuildImage      = "image"
)

// Database kinds available for one-click provisioning.
const (
	DBPostgres = "postgres"
	DBMySQL    = "mysql"
	DBMariaDB  = "mariadb"
	DBRedis    = "redis"
	DBMongo    = "mongodb"
)

// Deployment lifecycle states.
const (
	DeployQueued    = "queued"
	DeployBuilding  = "building"
	DeployPushing   = "pushing"
	DeployDeploying = "deploying"
	DeploySuccess   = "success"
	DeployFailed    = "failed"
	DeployCanceled  = "canceled"
)

// Deployment triggers recorded for the history view.
const (
	TriggerManual   = "manual"
	TriggerWebhook  = "webhook"
	TriggerAPI      = "api"
	TriggerRollback = "rollback"
)

// Job kinds handled by the durable worker.
const (
	JobDeploy      = "deploy"
	JobBuildImage  = "build_image"
	JobProvisionDB = "provision_db"
	JobBackup      = "backup"
	JobNodeMetrics = "node_metrics"
	JobGC          = "gc"
)

// Job states.
const (
	JobPending  = "pending"
	JobRunning  = "running"
	JobDone     = "done"
	JobFailed   = "failed"
	JobRetrying = "retrying"
)

// Env var targeting.
const (
	TargetApplication = "application"
	TargetService     = "service"
	TargetDatabase    = "database"
)

// User is an authenticated panel member.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	TOTPSecret   string    `json:"-"`
	TOTPEnabled  bool      `json:"totpEnabled"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Session is a browser session backed by an opaque token.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	TokenHash string    `json:"-"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"userAgent"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// APIToken is a long-lived credential for automation.
type APIToken struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// SSHKey is a credential pair used to reach remote nodes.
type SSHKey struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	PublicKey     string    `json:"publicKey"`
	PrivateKeyEnc string    `json:"-"`
	Fingerprint   string    `json:"fingerprint"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Node is a server the panel controls through the Docker Engine API. The
// singleton "local" row describes the machine the panel itself runs on.
type Node struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Address       string     `json:"address"`
	SSHPort       int        `json:"sshPort"`
	SSHUser       string     `json:"sshUser"`
	SSHKeyID      string     `json:"sshKeyId,omitempty"`
	Status        string     `json:"status"`
	OS            string     `json:"os"`
	Kernel        string     `json:"kernel"`
	Arch          string     `json:"arch"`
	CPUs          int        `json:"cpus"`
	MemoryBytes   int64      `json:"memoryBytes"`
	DockerVersion string     `json:"dockerVersion"`
	BootTime      *time.Time `json:"bootTime,omitempty"`
	LastSeenAt    *time.Time `json:"lastSeenAt"`
	ErrorMessage  string     `json:"errorMessage,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// NodeMetrics is a resource sample.
type NodeMetrics struct {
	ID         string    `json:"id"`
	NodeID     string    `json:"nodeId"`
	CPUPercent float64   `json:"cpuPercent"`
	MemUsed    int64     `json:"memUsed"`
	MemTotal   int64     `json:"memTotal"`
	DiskUsed   int64     `json:"diskUsed"`
	DiskTotal  int64     `json:"diskTotal"`
	NetRx      int64     `json:"netRx"`
	NetTx      int64     `json:"netTx"`
	RecordedAt time.Time `json:"recordedAt"`
}

// Project groups environments.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Environment is a deployment target inside a project (production, staging...).
type Environment struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// Application is a deployable unit built from a Git repository.
type Application struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environmentId"`
	NodeID        string `json:"nodeId,omitempty"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	GitRepository string `json:"gitRepository"`
	GitBranch     string `json:"gitBranch"`
	BuildPack     string `json:"buildPack"`
	Dockerfile    string `json:"dockerfile"`
	BaseDirectory string `json:"baseDirectory"`
	Port          int    `json:"port"`
	HealthCheck   string `json:"healthCheck"`
	Status        string `json:"status"`
	FQDN          string `json:"fqdn"`
	ImageTag      string `json:"imageTag"`
	// GitHub linkage. GitHubAppID is empty for repositories cloned over plain
	// HTTPS; when it is set the repository can be private and pushes trigger
	// automatic deployments.
	GitHubAppID          string    `json:"githubAppId,omitempty"`
	GitHubInstallationID int64     `json:"githubInstallationId,omitempty"`
	RepositoryFullName   string    `json:"repositoryFullName,omitempty"`
	AutoDeploy           bool      `json:"autoDeploy"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// Service is a Docker Compose stack managed by the panel.
type Service struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	NodeID        string    `json:"nodeId,omitempty"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Compose       string    `json:"compose"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Database is a provisioned database instance.
type Database struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	NodeID        string    `json:"nodeId,omitempty"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Version       string    `json:"version"`
	Status        string    `json:"status"`
	PublicPort    int       `json:"publicPort"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Deployment is one execution of the pipeline against an application.
type Deployment struct {
	ID            string     `json:"id"`
	ApplicationID string     `json:"applicationId"`
	Status        string     `json:"status"`
	Trigger       string     `json:"trigger"`
	CommitSHA     string     `json:"commitSha"`
	CommitMessage string     `json:"commitMessage"`
	ImageTag      string     `json:"imageTag"`
	LogPath       string     `json:"logPath"`
	Error         string     `json:"error"`
	QueuedAt      time.Time  `json:"queuedAt"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
}

// EnvVar is a build or runtime variable attached to a resource.
type EnvVar struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	TargetID  string    `json:"targetId"`
	Key       string    `json:"key"`
	ValueEnc  string    `json:"-"`
	IsSecret  bool      `json:"isSecret"`
	IsBuild   bool      `json:"isBuild"`
	CreatedAt time.Time `json:"createdAt"`
}

// Domain routes traffic to an application.
type Domain struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"applicationId"`
	Host          string    `json:"host"`
	TLSEnabled    bool      `json:"tlsEnabled"`
	CertStatus    string    `json:"certStatus"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Volume binds persistent storage to a resource.
type Volume struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	TargetID  string    `json:"targetId"`
	Name      string    `json:"name"`
	HostPath  string    `json:"hostPath"`
	MountPath string    `json:"mountPath"`
	ReadOnly  bool      `json:"readOnly"`
	CreatedAt time.Time `json:"createdAt"`
}

// Job is a unit of durable background work.
type Job struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Payload     string     `json:"payload"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"maxAttempts"`
	RunAt       time.Time  `json:"runAt"`
	LockedAt    *time.Time `json:"lockedAt"`
	LastError   string     `json:"lastError,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
}

// Backup is a scheduled or ad-hoc snapshot of a resource.
type Backup struct {
	ID          string     `json:"id"`
	Target      string     `json:"target"`
	TargetID    string     `json:"targetId"`
	Destination string     `json:"destination"`
	Status      string     `json:"status"`
	SizeBytes   int64      `json:"sizeBytes"`
	Error       string     `json:"error,omitempty"`
	Schedule    string     `json:"schedule"`
	LastRunAt   *time.Time `json:"lastRunAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Setting is an instance level key/value pair.
type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AuditLog records a privileged action.
type AuditLog struct {
	ID         string    `json:"id"`
	UserID     string    `json:"userId"`
	Action     string    `json:"action"`
	EntityType string    `json:"entityType"`
	EntityID   string    `json:"entityId"`
	Metadata   string    `json:"metadata"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Webhook is an inbound deploy trigger for a Git provider.
type Webhook struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"applicationId"`
	Provider      string    `json:"provider"`
	Secret        string    `json:"-"`
	CreatedAt     time.Time `json:"createdAt"`
}

// GitHubApp is a GitHub App registration owned by the panel. It is created
// through GitHub's manifest flow: the row is written first (so the redirect can
// find it again) and filled with credentials when GitHub hands them back.
type GitHubApp struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Slug             string    `json:"slug"`
	AppID            int64     `json:"appId"`
	ClientID         string    `json:"clientId"`
	ClientSecretEnc  string    `json:"-"`
	PrivateKeyEnc    string    `json:"-"`
	WebhookSecretEnc string    `json:"-"`
	HTMLURL          string    `json:"htmlUrl"`
	BaseURL          string    `json:"baseUrl"`
	Organization     string    `json:"organization"`
	State            string    `json:"-"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// Configured reports whether GitHub has issued the app its credentials. An app
// is briefly unconfigured between "Register With GitHub" and the callback that
// exchanges the manifest code.
func (a *GitHubApp) Configured() bool { return a.AppID > 0 && a.PrivateKeyEnc != "" }

// GitHubInstallation is one account (a user or an organization) that installed
// a GitHub App. AppName is joined in for list responses.
type GitHubInstallation struct {
	ID                  string    `json:"id"`
	GitHubAppID         string    `json:"githubAppId"`
	AppName             string    `json:"appName"`
	InstallationID      int64     `json:"installationId"`
	AccountLogin        string    `json:"accountLogin"`
	AccountType         string    `json:"accountType"`
	RepositorySelection string    `json:"repositorySelection"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}
