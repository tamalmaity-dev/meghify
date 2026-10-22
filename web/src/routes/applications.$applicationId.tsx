import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ChevronDown, Copy, Download, Github, Globe, Play, Plus, RefreshCw, RotateCcw, Scissors, Search, Settings, Square, Terminal, Trash2, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api, streamEvents } from "@/lib/api";
import { useSession } from "@/lib/session";
import { PageHeader } from "@/components/app-shell";
import { XTerminal } from "@/components/terminal";
import { ApplicationResourceGuardCard } from "@/components/resource-guard";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Select, Textarea } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { StatusBadge } from "@/components/ui/badge";
import { Table, TableToolbar, Td, Th, Tr } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type {
  Application,
  Deployment,
  Domain,
  EnvVar,
  Node as ServerNode,
  ResourceGuardEvent,
  Volume,
} from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const SECTIONS = [
  { key: "general", label: "General" },
  { key: "terminal", label: "Terminal" },
  { key: "domains", label: "Domains" },
  { key: "environment", label: "Environment" },
  { key: "volumes", label: "Persistent storage" },
  { key: "networking", label: "Networking" },
  { key: "runtime", label: "Runtime" },
  { key: "logs", label: "Deployment logs" },
  { key: "runtimeLogs", label: "Runtime logs" },
  { key: "deployments", label: "Deployments" },
  { key: "danger", label: "Danger zone" },
] as const;

type Section = (typeof SECTIONS)[number]["key"];

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

/** Mirrors the deploy pipeline's container and network slugifier. */
function sanitizeName(name: string): string {
  let out = "";
  for (const char of name.trim().toLowerCase()) {
    out += /[a-z0-9_-]/.test(char) ? char : "-";
  }
  out = out.replace(/^-+|-+$/g, "");
  return out || "app";
}

// Engine names are built from the immutable application id on the backend, so
// renames never orphan a container and equal display names cannot collide.
const containerName = (app: Application) => `meghify-app-${sanitizeName(app.id)}`;
const networkName = (app: Application) => `meghify-net-${sanitizeName(app.id)}`;

export function ApplicationDetailPage() {
  const { applicationId } = useParams({ from: "/applications/$applicationId" });
  const queryClient = useQueryClient();
  const { user } = useSession();
  const isAdmin = user?.role === "admin";
  // Members can inspect the runtime and operate deployments, but editing the
  // build, domains, environment, volumes or networking is an admin action.
  const memberSections: Section[] = [
    "general",
    "volumes",
    "logs",
    "runtimeLogs",
    "deployments",
  ];
  const sections = SECTIONS.filter(
    (item) => isAdmin || memberSections.includes(item.key),
  );
  const [section, setSection] = useState<Section>("general");
  // Set when a history row is picked; cleared when the section changes to Logs
  // through the nav, which then falls back to the latest deployment.
  const [pickedDeployment, setPickedDeployment] = useState<string>();

  const app = useQuery({
    queryKey: ["applications", applicationId],
    queryFn: () => api.get<Application>(`/applications/${applicationId}`),
  });
  const deployments = useQuery({
    queryKey: ["deployments", applicationId],
    queryFn: () => api.get<Deployment[]>(`/applications/${applicationId}/deployments`),
    refetchInterval: (query) =>
      query.state.data?.some((d) => ["queued", "building", "pushing", "deploying"].includes(d.status))
        ? 2000
        : false,
  });
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.get<ServerNode[]>("/nodes"),
  });
  const domains = useQuery({
    queryKey: ["domains", applicationId],
    queryFn: () => api.get<Domain[]>(`/applications/${applicationId}/domains`),
  });
  const envVars = useQuery({
    queryKey: ["env", applicationId],
    queryFn: () => api.get<EnvVar[]>(`/applications/${applicationId}/env`),
  });
  const volumes = useQuery({
    queryKey: ["volumes", applicationId],
    queryFn: () => api.get<Volume[]>(`/applications/${applicationId}/volumes`),
  });
  const guardEvents = useQuery({
    queryKey: ["resource-guard", "events", applicationId, "latest"],
    queryFn: () => api.get<ResourceGuardEvent[]>(
      `/resource-guard/events?applicationId=${encodeURIComponent(applicationId)}&limit=1`,
    ),
    enabled: isAdmin,
    refetchInterval: 20_000,
  });

  // The guard writes an application event when it notifies, throttles or
  // stops a container. Keep the page in sync even when it was already open.
  useEffect(() => {
    if (!isAdmin) return;
    const controller = new AbortController();
    (async () => {
      try {
        for await (const event of streamEvents(
          `/api/stream?channel=application:${applicationId}&replay=0`,
          controller.signal,
        )) {
          if (event.event !== "resource_guard") continue;
          const action = String(event.action ?? "notify");
          const message = String(event.message ?? "Resource protection acted on this application");
          toast.warning(`Resource guard: ${message}`);
          if (action === "stop") {
            queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
          }
          queryClient.invalidateQueries({
            queryKey: ["resource-guard", "events", applicationId, "latest"],
          });
        }
      } catch {
        // Abort on unmount is expected; the stream helper reconnects on
        // transient network failures by itself.
      }
    })();
    return () => controller.abort();
  }, [applicationId, isAdmin, queryClient]);

  const deploy = useMutation({
    mutationFn: (options: { noCache: boolean }) =>
      api.post<Deployment>(`/applications/${applicationId}/deploy`, options),
    onSuccess: (_created, options) => {
      toast.success(options.noCache ? "Redeploy without cache queued" : "Deployment queued");
      queryClient.invalidateQueries({ queryKey: ["deployments", applicationId] });
      queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const runAction = (path: string) => async () => {
    await api.post(path);
    queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
  };

  const active = deployments.data?.find((d) =>
    ["queued", "building", "pushing", "deploying"].includes(d.status),
  );
  const latest = deployments.data?.[0];
  const application = app.data;

  // Only the deployment list is polled while a build runs. Its terminal
  // transition also updates the application row (status and image tag), so
  // refresh that separately. Without this the header keeps showing the idle
  // status and empty image it cached when the deployment was queued.
  useEffect(() => {
    if (!latest || isDeploymentInFlight(latest.status)) return;
    void queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
  }, [latest?.id, latest?.status, applicationId, queryClient]);

  const watched =
    (pickedDeployment && deployments.data?.find((d) => d.id === pickedDeployment)) || latest;
  const serverName = application?.nodeId
    ? (nodes.data?.find((node) => node.id === application.nodeId)?.name ?? "remote")
    : "localhost";
  // The automatic sslip.io domain is plain HTTP, so the header link follows
  // the domain's TLS setting rather than assuming HTTPS.
  const primaryDomain = domains.data?.find((domain) => domain.host === application?.fqdn);
  const fqdnScheme = primaryDomain && !primaryDomain.tlsEnabled ? "http" : "https";
  const guardStop = guardEvents.data?.[0];
  const guardStopRecent = Boolean(
    guardStop &&
      guardStop.action === "stop" &&
      application?.status === "stopped" &&
      Date.now() - new Date(guardStop.createdAt).getTime() < 24 * 60 * 60 * 1000,
  );

  const openLogs = (deploymentID: string) => {
    setPickedDeployment(deploymentID);
    setSection("logs");
  };

  return (
    <>
      <PageHeader
        title={application?.name ?? "Application"}
        description={application?.gitRepository || "Image deployment"}
        actions={
          <>
            {isAdmin && (
              <Button
                variant="ghost"
                size="icon"
                onClick={() => setSection("terminal")}
                disabled={application?.status !== "running"}
                title="Open terminal"
              >
                <Terminal className="h-4 w-4" />
              </Button>
            )}
            <ActionsMenu
              deployPending={deploy.isPending}
              busy={Boolean(active)}
              onRedeploy={() => deploy.mutate({ noCache: false })}
              onRedeployNoCache={() => deploy.mutate({ noCache: true })}
              onRestart={runAction(`/applications/${applicationId}/restart`)}
              onStop={runAction(`/applications/${applicationId}/stop`)}
            />
            <Button
              variant="primary"
              onClick={() => deploy.mutate({ noCache: false })}
              disabled={deploy.isPending || Boolean(active)}
            >
              <Play className="h-4 w-4" />
              {latest ? "Redeploy" : "Deploy"}
            </Button>
          </>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-3">
        {application && <StatusBadge status={application.status} />}
        <span className="font-mono text-[12px] text-black/55 dark:text-white/45">
          {application?.imageTag || "no image yet"}
        </span>
        {application?.fqdn && (
          <a
            href={`${fqdnScheme}://${application.fqdn}`}
            target="_blank"
            rel="noreferrer"
            className="text-[12px] text-brand hover:underline"
          >
            {fqdnScheme}://{application.fqdn}
          </a>
        )}
        {active && <span className="text-[11px] text-warn">{active.status}…</span>}
      </div>

      {guardStopRecent && guardStop && (
        <div className="mb-4 flex items-start gap-2 rounded-md bg-warn/10 px-3 py-2 text-[11.5px] leading-relaxed text-warn">
          <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>
            <strong className="font-medium">Stopped by Resource Guard.</strong> {guardStop.message}{" "}
            Start the application again only after fixing the resource usage, or turn protection
            off from Runtime.
          </span>
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-[200px_minmax(0,1fr)]">
        <nav className="flex gap-1 overflow-x-auto lg:flex-col lg:overflow-visible">
          {sections.map((item) => (
            <button
              key={item.key}
              onClick={() => setSection(item.key)}
              className={cn(
                "h-8 rounded-md px-3 text-left text-[12px] whitespace-nowrap transition-colors",
                section === item.key
                  ? "bg-black/[0.06] font-medium text-current dark:bg-white/[0.08]"
                  : "text-black/55 hover:bg-black/[0.03] hover:text-current dark:text-white/45 dark:hover:bg-white/[0.04]",
                item.key === "danger" && section !== "danger" && "text-danger/80 hover:text-danger",
              )}
            >
              {item.label}
            </button>
          ))}
        </nav>

        <div className="min-w-0">
          {section === "general" &&
            (application ? (
              <div className="space-y-3">
                <ApplicationDetailsCard
                  app={application}
                  applicationId={applicationId}
                  readOnly={!isAdmin}
                />
                {isAdmin && application.repositoryFullName && (
                  <GitHubCard app={application} applicationId={applicationId} />
                )}
                <AccessCard
                  app={application}
                  domains={domains.data ?? []}
                  canEdit={isAdmin}
                  onManageDomains={() => setSection("domains")}
                  onEditNetworking={() => setSection("networking")}
                />
                <div className="grid gap-3 lg:grid-cols-2">
                  <BuildCard app={application} />
                  <LatestDeploymentCard latest={latest} />
                </div>
              </div>
            ) : (
              <Card>
                <CardContent className="py-8 text-center text-[12px] text-black/45 dark:text-white/40">
                  Loading application…
                </CardContent>
              </Card>
            ))}

          {section === "terminal" && (
            <Card>
              <CardHeader>
                <CardTitle>Terminal</CardTitle>
                <span className="text-[11px] text-black/40 dark:text-white/35">
                  {application?.status === "running"
                    ? "shell inside the running container"
                    : "container is not running"}
                </span>
              </CardHeader>
              <CardContent className="p-0">
                {application?.status === "running" ? (
                  <div className="h-[26rem] overflow-hidden rounded-b-[8px] bg-[#0a0a0a]">
                    <XTerminal
                      url={`${
                        window.location.protocol === "https:" ? "wss" : "ws"
                      }://${window.location.host}/api/applications/${applicationId}/terminal`}
                      className="h-full"
                      title={application?.name ?? "Application"}
                      subtitle="container shell"
                      banner="\r\nConnected to container.\r\n"
                    />
                  </div>
                ) : (
                  <p className="px-4 py-10 text-center text-[12px] text-black/45 dark:text-white/40">
                    The container is not running — deploy the application to open a shell.
                  </p>
                )}
              </CardContent>
            </Card>
          )}

          {section === "networking" && application && (
            <NetworkingCard app={application} applicationId={applicationId} />
          )}

          {section === "runtime" && application && (
            <div className="space-y-3">
              <RuntimeCard app={application} applicationId={applicationId} />
              <ApplicationResourceGuardCard app={application} applicationId={applicationId} />
            </div>
          )}

          {section === "danger" && application && (
            <DangerCard app={application} applicationId={applicationId} />
          )}

          {section === "domains" && application && (
            <DomainsCard applicationId={applicationId} app={application} domains={domains.data ?? []} />
          )}

          {section === "environment" && (
            <EnvPanel applicationId={applicationId} vars={envVars.data ?? []} />
          )}

          {section === "volumes" && (
            <Card>
              <CardHeader>
                <CardTitle>Persistent storage</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                {(volumes.data ?? []).length === 0 ? (
                  <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                    No volumes mounted.
                  </p>
                ) : (
                  <Table
                    head={
                      <>
                        <Th>Name</Th>
                        <Th>Mount path</Th>
                        <Th align="right">Read only</Th>
                      </>
                    }
                  >
                    {(volumes.data ?? []).map((volume) => (
                      <Tr key={volume.id}>
                        <Td>{volume.name}</Td>
                        <Td className="font-mono text-[12px] text-black/55 dark:text-white/45">
                          {volume.mountPath}
                        </Td>
                        <Td align="right">{volume.readOnly ? "yes" : "no"}</Td>
                      </Tr>
                    ))}
                  </Table>
                )}
              </CardContent>
            </Card>
          )}

          {section === "logs" && (
            <DeploymentLogs deployment={watched} serverName={serverName} />
          )}

          {section === "runtime" && (
            <RuntimeLogs
              applicationId={applicationId}
              running={application?.status === "running"}
            />
          )}

          {section === "deployments" && (
            <DeploymentHistory
              deployments={deployments.data ?? []}
              serverName={serverName}
              onOpen={openLogs}
            />
          )}
        </div>
      </div>
    </>
  );
}

/** Name and description are editable in place, mirroring every other settings card. */
function ApplicationDetailsCard({
  app,
  applicationId,
  readOnly = false,
}: {
  app: Application;
  applicationId: string;
  readOnly?: boolean;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(app.name);
  const [description, setDescription] = useState(app.description);

  useEffect(() => {
    setName(app.name);
    setDescription(app.description);
  }, [app.id, app.name, app.description]);

  const save = useMutation({
    mutationFn: () =>
      api.put<Application>(
        `/applications/${applicationId}`,
        applicationPayload(app, { name: name.trim(), description }),
      ),
    onSuccess: async () => {
      toast.success("Application updated");
      await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const dirty = name.trim() !== app.name || description !== app.description;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Application details</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <Label htmlFor="application-name">Name</Label>
            <Input
              id="application-name"
              value={name}
              disabled={readOnly}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div>
            <Label htmlFor="application-description">Description</Label>
            <Input
              id="application-description"
              value={description}
              disabled={readOnly}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="What does this application do?"
            />
          </div>
        </div>
        {!readOnly && save.isError && (
          <p className="text-[11px] text-danger">{errorText(save.error)}</p>
        )}
        {!readOnly && (
          <div className="flex justify-end">
            <Button
              variant="primary"
              size="sm"
              disabled={!dirty || name.trim() === "" || save.isPending}
              onClick={() => save.mutate()}
            >
              Save
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/**
 * The GitHub App link: which repository this application deploys from and
 * whether a push to the tracked branch redeploys it automatically.
 */
function GitHubCard({ app, applicationId }: { app: Application; applicationId: string }) {
  const queryClient = useQueryClient();
  const [autoDeploy, setAutoDeploy] = useState(app.autoDeploy);

  useEffect(() => {
    setAutoDeploy(app.autoDeploy);
  }, [app.id, app.autoDeploy]);

  const toggle = useMutation({
    mutationFn: (enabled: boolean) =>
      api.put<Application>(
        `/applications/${applicationId}`,
        applicationPayload(app, { autoDeploy: enabled }),
      ),
    onSuccess: async (_updated, enabled) => {
      toast.success(
        enabled ? "Automatic deploys enabled for this branch" : "Automatic deploys disabled",
      );
      await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
    onError: () => setAutoDeploy(app.autoDeploy),
  });

  const webUrl = app.gitRepository.replace(/\.git$/, "");

  return (
    <Card>
      <CardHeader>
        <CardTitle>Source</CardTitle>
        <span className="flex items-center gap-1.5 text-[11px] text-black/40 dark:text-white/35">
          <Github className="h-3.5 w-3.5" />
          GitHub App
        </span>
      </CardHeader>
      <CardContent className="space-y-3 text-[12px]">
        <div className="space-y-1.5">
          <div className="flex items-center justify-between gap-3">
            <span className="text-black/50 dark:text-white/45">Repository</span>
            {webUrl ? (
              <a
                href={webUrl}
                target="_blank"
                rel="noreferrer"
                className="truncate font-mono text-[11.5px] text-brand hover:underline"
              >
                {app.repositoryFullName || webUrl}
              </a>
            ) : (
              <span className="truncate font-mono text-[11.5px]">
                {app.repositoryFullName || "-"}
              </span>
            )}
          </div>
          <Row label="Branch" value={app.gitBranch || "main"} />
        </div>

        <div className="flex items-center justify-between gap-3 rounded-md bg-recessed px-3 py-2">
          <div className="min-w-0">
            <div className="text-[12px] font-medium">Deploy on every push</div>
            <div className="text-[10.5px] text-black/40 dark:text-white/30">
              {autoDeploy
                ? `Pushes to ${app.gitBranch || "main"} redeploy this application through the GitHub webhook.`
                : "Pushes are ignored; deploys only run when you press Deploy."}
            </div>
          </div>
          <Button
            variant={autoDeploy ? "neutral" : "primary"}
            size="sm"
            disabled={toggle.isPending}
            onClick={() => {
              setAutoDeploy(!autoDeploy);
              toggle.mutate(!autoDeploy);
            }}
          >
            {autoDeploy ? "Disable" : "Enable"}
          </Button>
        </div>
        {toggle.isError && <p className="text-[11px] text-danger">{errorText(toggle.error)}</p>}
      </CardContent>
    </Card>
  );
}

/**
 * The public URL, its TLS state, and the in-network coordinates other containers
 * use to reach this application.
 */
function AccessCard({
  app,
  domains,
  canEdit,
  onManageDomains,
  onEditNetworking,
}: {
  app: Application;
  domains: Domain[];
  canEdit: boolean;
  onManageDomains: () => void;
  onEditNetworking: () => void;
}) {
  const primary = domains[0];
  const publicHost = primary?.host ?? app.fqdn;
  const scheme = primary?.tlsEnabled ? "https" : "http";

  return (
    <Card>
      <CardHeader>
        <CardTitle>Access</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 text-[12px]">
        <div>
          <div className="text-[11px] tracking-wider text-black/40 uppercase dark:text-white/35">
            Public access
          </div>
          {publicHost ? (
            <div className="mt-1.5 flex items-center justify-between gap-3 rounded-md bg-recessed px-3 py-2">
              <div className="min-w-0">
                <a
                  href={`${scheme}://${publicHost}`}
                  target="_blank"
                  rel="noreferrer"
                  className="block truncate font-mono text-[12px] text-brand hover:underline"
                >
                  {publicHost}
                </a>
                <div className="text-[10.5px] text-black/40 dark:text-white/30">
                  {domains.length > 1
                    ? `${domains.length} domains attached`
                    : primary
                      ? primary.tlsEnabled
                        ? `TLS · ${primary.certStatus || "pending"}`
                        : "TLS disabled"
                      : "domain managed elsewhere"}
                </div>
              </div>
              {canEdit && (
                <Button variant="neutral" size="sm" onClick={onManageDomains}>
                  Manage
                </Button>
              )}
            </div>
          ) : (
            <p className="mt-1 text-black/45 dark:text-white/40">
              {canEdit ? (
                <>
                  No public URL yet.{" "}
                  <button className="text-brand hover:underline" onClick={onManageDomains}>
                    Add a domain
                  </button>{" "}
                  to expose this application over HTTP.
                </>
              ) : (
                "No public URL yet."
              )}
            </p>
          )}
        </div>

        <div>
          <div className="flex items-center justify-between">
            <div className="text-[11px] tracking-wider text-black/40 uppercase dark:text-white/35">
              Internal access
            </div>
            {canEdit && (
              <Button variant="neutral" size="sm" onClick={onEditNetworking}>
                Edit networking
              </Button>
            )}
          </div>
          <div className="mt-1.5 space-y-1.5">
            <Row label="Container name" value={containerName(app)} />
            <Row label="Docker network" value={networkName(app)} />
            <Row label="Exposed port" value={String(app.port)} />
            <Row label="Port mappings" value={app.portMappings || "none"} />
          </div>
          <p className="mt-2 text-[10.5px] text-black/35 dark:text-white/30">
            Reachable from other containers on the same network as{" "}
            <span className="font-mono">
              {containerName(app)}:{app.port}
            </span>
            .
          </p>
        </div>
      </CardContent>
    </Card>
  );
}

function BuildCard({ app }: { app: Application }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Build &amp; runtime</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 text-[12px]">
        <Row label="Build pack" value={app.buildPack || "dockerfile"} />
        {app.buildPack === "image" ? (
          <Row label="Image" value={app.gitRepository || "-"} />
        ) : (
          <>
            <Row label="Repository" value={app.gitRepository || "-"} />
            <Row label="Branch" value={app.gitBranch || "-"} />
          </>
        )}
        {app.buildPack === "dockerfile" && (
          <Row label="Dockerfile path" value={app.dockerfile || "Dockerfile"} />
        )}
        {app.buildPack === "compose" && <Row label="Compose file" value="stored inline" />}
        <Row label="Container port" value={String(app.port)} />
        <Row label="Health check" value={app.healthCheck || "-"} />
        <Row label="Base directory" value={app.baseDirectory || "/"} />
      </CardContent>
    </Card>
  );
}

function LatestDeploymentCard({ latest }: { latest?: Deployment }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Latest deployment</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 text-[12px]">
        <Row label="Status" value={latest?.status ?? "never deployed"} />
        <Row label="Trigger" value={latest?.trigger ?? "-"} />
        <Row label="Commit" value={latest?.commitSha?.slice(0, 8) || "-"} />
        <Row label="Queued" value={latest ? new Date(latest.queuedAt).toLocaleString() : "-"} />
        {latest?.error && <p className="text-[11px] text-danger">{latest.error}</p>}
      </CardContent>
    </Card>
  );
}

/** Coolify-style Domains card: per-domain protocol, port, path, DNS checks. */
function DomainsCard({
  applicationId,
  app,
  domains,
}: {
  applicationId: string;
  app: Application;
  domains: Domain[];
}) {
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<Domain | null>(null);
  const [form, setForm] = useState({ protocol: "https", host: "", port: "", path: "" });

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ["domains", applicationId] });
    await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
  };

  const openAdd = () => {
    setEditing(null);
    setForm({ protocol: "https", host: "", port: "", path: "" });
    setDialogOpen(true);
  };
  const openEdit = (domain: Domain) => {
    setEditing(domain);
    setForm({
      protocol: domain.tlsEnabled ? "https" : "http",
      host: domain.host,
      port: domain.port ? String(domain.port) : "",
      path: domain.path ?? "",
    });
    setDialogOpen(true);
  };

  const save = useMutation({
    mutationFn: () => {
      const body = {
        host: form.host.trim(),
        tlsEnabled: form.protocol === "https",
        port: Number(form.port) || 0,
        path: form.path.trim(),
      };
      return editing
        ? api.patch(`/applications/domains/${editing.id}`, body)
        : api.post(`/applications/${applicationId}/domains`, body);
    },
    onSuccess: async () => {
      setDialogOpen(false);
      toast.success(
        editing ? "Domain updated — applied to the proxy immediately" : "Domain added — already routed",
      );
      await invalidate();
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const generate = useMutation({
    mutationFn: () => api.get<{ host: string }>(`/applications/${applicationId}/suggest-domain`),
    onSuccess: (suggestion) => setForm((current) => ({ ...current, host: suggestion.host })),
    onError: (error) => toast.error(errorText(error)),
  });

  const toggleRedirect = useMutation({
    mutationFn: (enabled: boolean) =>
      api.put<Application>(`/applications/${applicationId}`, applicationPayload(app, { redirectHttps: enabled })),
    onSuccess: async () => {
      toast.success("Redirect setting saved — applied to the proxy immediately");
      await invalidate();
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const toggleIndexing = useMutation({
    mutationFn: (domain: Domain) =>
      api.patch(`/applications/domains/${domain.id}`, {
        host: domain.host,
        tlsEnabled: domain.tlsEnabled,
        port: domain.port,
        path: domain.path,
        searchIndexing: !domain.searchIndexing,
      }),
    onSuccess: invalidate,
    onError: (error) => toast.error(errorText(error)),
  });

  const checkOne = useMutation({
    mutationFn: (domainId: string) => api.post(`/applications/domains/${domainId}/check`),
    onSuccess: invalidate,
    onError: (error) => toast.error(errorText(error)),
  });

  const checkAll = useMutation({
    mutationFn: () => api.post(`/applications/${applicationId}/domains/check`),
    onSuccess: async () => {
      toast.success("DNS checked");
      await invalidate();
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const remove = useMutation({
    mutationFn: (domainId: string) => api.delete(`/applications/domains/${domainId}`),
    onSuccess: async () => {
      toast.success("Domain removed — redeploy to detach it");
      await invalidate();
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const needle = query.trim().toLowerCase();
  const list = needle ? domains.filter((d) => d.host.toLowerCase().includes(needle)) : domains;

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Domains</CardTitle>
          <span className="text-[11px] text-black/40 dark:text-white/35">
            {domains.length === 1 ? "1 domain" : `${domains.length} domains`}
          </span>
        </CardHeader>
        <CardContent className="p-0">
          <TableToolbar
            left={
              <div className="relative max-w-sm">
                <Search className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-black/35 dark:text-white/30" />
                <Input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Search domains"
                  className="pl-8"
                />
              </div>
            }
            right={
              <>
                <Button
                  variant="neutral"
                  size="sm"
                  onClick={() => checkAll.mutate()}
                  disabled={checkAll.isPending || domains.length === 0}
                >
                  <RefreshCw className={checkAll.isPending ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
                  Check All DNS
                </Button>
                <Button variant="primary" size="sm" onClick={openAdd}>
                  <Plus className="h-3.5 w-3.5" />
                  Add Domain
                </Button>
              </>
            }
          />
          <div className="flex items-center justify-end gap-2 border-b border-line/40 px-3 py-2">
            <span className="text-[11px] text-black/50 dark:text-white/45">
              Redirect HTTP to HTTPS
            </span>
            <Select
              value={app.redirectHttps ? "enabled" : "disabled"}
              onChange={(event) => toggleRedirect.mutate(event.target.value === "enabled")}
              className="w-36"
            >
              <option value="enabled">Enabled</option>
              <option value="disabled">Disabled</option>
            </Select>
          </div>

          {domains.length === 0 ? (
            <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
              No domains yet — add one, or redeploy to get an automatic sslip.io address.
            </p>
          ) : list.length === 0 ? (
            <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
              No domain matches that search.
            </p>
          ) : (
            <Table
              head={
                <>
                  <Th>Domain</Th>
                  <Th>Protocol redirect</Th>
                  <Th>Domain redirect</Th>
                  <Th>Internal port</Th>
                  <Th>Search indexing</Th>
                  <Th>DNS status</Th>
                  <Th align="right">Actions</Th>
                </>
              }
            >
              {list.map((domain) => (
                <Tr key={domain.id}>
                  <Td>
                    <span className="flex min-w-0 items-center gap-2">
                      <Globe className="h-3.5 w-3.5 shrink-0 text-black/35 dark:text-white/30" />
                      <a
                        href={`${domain.tlsEnabled ? "https" : "http"}://${domain.host}${domain.path ?? ""}`}
                        target="_blank"
                        rel="noreferrer"
                        className="truncate font-mono text-[12px] text-brand hover:underline"
                      >
                        {domain.tlsEnabled ? "https" : "http"}://{domain.host}
                        {domain.path ?? ""}
                      </a>
                    </span>
                  </Td>
                  <Td>
                    {domain.tlsEnabled ? (
                      domain.redirectHttps ? (
                        <span className="text-[11.5px] text-ok">HTTP → HTTPS</span>
                      ) : (
                        <span className="text-[11.5px] text-black/45 dark:text-white/40">Disabled</span>
                      )
                    ) : (
                      <span className="text-[11.5px] text-black/45 dark:text-white/40">Disabled</span>
                    )}
                  </Td>
                  <Td className="text-[11.5px] text-black/45 dark:text-white/40">Disabled</Td>
                  <Td className="font-mono text-[12px]">{domain.port || app.port}</Td>
                  <Td>
                    <input
                      type="checkbox"
                      checked={domain.searchIndexing}
                      onChange={() => toggleIndexing.mutate(domain)}
                      title="Allow crawlers to index this domain"
                    />
                  </Td>
                  <Td>
                    {domain.dnsStatus === "matches" ? (
                      <span className="inline-flex items-center gap-1.5 rounded-full bg-ok/10 px-2 py-0.5 text-[10.5px] font-medium text-ok">
                        <span className="h-1.5 w-1.5 rounded-full bg-ok" />
                        DNS matches
                      </span>
                    ) : domain.dnsStatus === "mismatch" ? (
                      <span className="inline-flex items-center gap-1.5 rounded-full bg-danger/10 px-2 py-0.5 text-[10.5px] font-medium text-danger">
                        <span className="h-1.5 w-1.5 rounded-full bg-danger" />
                        DNS mismatch
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1.5 rounded-full bg-black/[0.06] px-2 py-0.5 text-[10.5px] font-medium text-black/50 dark:bg-white/[0.08] dark:text-white/45">
                        <span className="h-1.5 w-1.5 rounded-full bg-black/25 dark:bg-white/25" />
                        Not checked
                      </span>
                    )}
                  </Td>
                  <Td align="right">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon"
                        title="Check DNS"
                        onClick={() => checkOne.mutate(domain.id)}
                        disabled={checkOne.isPending}
                      >
                        <RefreshCw className="h-3.5 w-3.5" />
                      </Button>
                      <Button variant="ghost" size="icon" title="Edit domain" onClick={() => openEdit(domain)}>
                        <Settings className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        title="Remove domain"
                        disabled={remove.isPending}
                        onClick={() => {
                          if (window.confirm(`Remove ${domain.host}? Redeploy to detach it from the proxy.`)) {
                            remove.mutate(domain.id);
                          }
                        }}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </Td>
                </Tr>
              ))}
            </Table>
          )}
        </CardContent>
      </Card>

      <Modal
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        title={editing ? "Edit domain" : "Add domain"}
        description="Traffic for this address routes to the application after the next deploy."
        footer={
          <>
            {!editing && (
              <Button
                variant="neutral"
                onClick={() => generate.mutate()}
                disabled={generate.isPending}
                className="mr-auto"
              >
                Generate Domain
              </Button>
            )}
            <Button variant="ghost" onClick={() => setDialogOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={form.host.trim() === "" || save.isPending}
              onClick={() => save.mutate()}
            >
              Save
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div className="grid grid-cols-[110px_minmax(0,1fr)_96px] gap-3">
            <div>
              <Label htmlFor="domain-protocol">Protocol</Label>
              <Select
                id="domain-protocol"
                value={form.protocol}
                onChange={(event) => setForm({ ...form, protocol: event.target.value })}
              >
                <option value="https">https</option>
                <option value="http">http</option>
              </Select>
            </div>
            <div>
              <Label htmlFor="domain-host">Domain</Label>
              <Input
                id="domain-host"
                value={form.host}
                onChange={(event) => setForm({ ...form, host: event.target.value })}
                placeholder="app.example.com"
                autoFocus
              />
            </div>
            <div>
              <Label htmlFor="domain-port">Port</Label>
              <Input
                id="domain-port"
                value={form.port}
                onChange={(event) => setForm({ ...form, port: event.target.value })}
                placeholder={String(app.port)}
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                Leave blank to follow the application's container port.
              </p>
            </div>
          </div>
          <div>
            <Label htmlFor="domain-path">Path</Label>
            <Input
              id="domain-path"
              value={form.path}
              onChange={(event) => setForm({ ...form, path: event.target.value })}
              placeholder="/api/v3"
            />
            <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
              Optional path prefix routed to this application; leave empty for every path.
            </p>
          </div>
          {save.isError && <p className="text-[11px] text-danger">{errorText(save.error)}</p>}
        </div>
      </Modal>
    </>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <span className="text-black/45 dark:text-white/40">{label}</span>
      <span className="truncate font-mono text-[12px]">{value}</span>
    </div>
  );
}

/**
 * Deployment history, Coolify-style: status, source, commit, timing and the
 * server each build ran on. Picking a row opens its log.
 */
function DeploymentHistory({
  deployments,
  serverName,
  onOpen,
}: {
  deployments: Deployment[];
  serverName: string;
  onOpen: (deploymentID: string) => void;
}) {
  const [query, setQuery] = useState("");
  const needle = query.trim().toLowerCase();
  const list =
    needle === ""
      ? deployments
      : deployments.filter((deployment) =>
          [deployment.status, deployment.trigger, deployment.commitSha, deployment.commitMessage]
            .filter(Boolean)
            .some((value) => String(value).toLowerCase().includes(needle)),
        );

  return (
    <Card>
      <CardHeader>
        <CardTitle>Deployment history</CardTitle>
        <span className="text-[11px] text-black/40 dark:text-white/35">
          {deployments.length === 1 ? "1 deployment" : `${deployments.length} deployments`}
        </span>
      </CardHeader>
      <CardContent className="p-0">
        <TableToolbar
          left={
            <div className="relative max-w-sm">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-black/35 dark:text-white/30" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search deployments"
                className="pl-8"
              />
            </div>
          }
        />
        {deployments.length === 0 ? (
          <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
            No deployments yet.
          </p>
        ) : list.length === 0 ? (
          <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
            No deployment matches that search.
          </p>
        ) : (
          <Table
            head={
              <>
                <Th>Status</Th>
                <Th>Source</Th>
                <Th>Commit</Th>
                <Th>Started</Th>
                <Th>Duration</Th>
                <Th>Server</Th>
              </>
            }
          >
            {list.map((deployment) => (
              <Tr key={deployment.id} onClick={() => onOpen(deployment.id)}>
                <Td>
                  <DeploymentStatus status={deployment.status} />
                </Td>
                <Td className="text-black/55 dark:text-white/45">
                  {deployment.trigger === "webhook"
                    ? "Webhook"
                    : deployment.trigger.charAt(0).toUpperCase() + deployment.trigger.slice(1)}
                </Td>
                <Td>
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="shrink-0 font-mono text-[11.5px]">
                      {deployment.commitSha ? deployment.commitSha.slice(0, 7) : "—"}
                    </span>
                    <span className="truncate text-[11.5px] text-black/55 dark:text-white/45">
                      {deployment.commitMessage || ""}
                    </span>
                  </span>
                </Td>
                <Td className="text-black/55 dark:text-white/45">
                  {relativeTime(deployment.startedAt ?? deployment.queuedAt)}
                </Td>
                <Td className="font-mono text-[11.5px] text-black/55 dark:text-white/45">
                  {deploymentDuration(deployment)}
                </Td>
                <Td className="text-black/55 dark:text-white/45">{serverName}</Td>
              </Tr>
            ))}
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

/** A coloured status dot with its label, mirroring Coolify's history rows. */
const DEPLOYMENT_TONES: Record<string, { label: string; dot: string; text: string; pulse?: boolean }> = {
  success: { label: "Success", dot: "bg-ok", text: "text-ok" },
  failed: { label: "Failed", dot: "bg-danger", text: "text-danger" },
  canceled: { label: "Canceled", dot: "bg-black/30 dark:bg-white/25", text: "text-black/50 dark:text-white/45" },
  queued: { label: "Queued", dot: "bg-warn", text: "text-warn", pulse: true },
  building: { label: "Building", dot: "bg-brand", text: "text-brand", pulse: true },
  pushing: { label: "Pushing", dot: "bg-brand", text: "text-brand", pulse: true },
  deploying: { label: "Deploying", dot: "bg-brand", text: "text-brand", pulse: true },
};

function DeploymentStatus({ status }: { status: string }) {
  const tone = DEPLOYMENT_TONES[status] ?? {
    label: status,
    dot: "bg-black/30 dark:bg-white/25",
    text: "text-black/60 dark:text-white/50",
  };
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-[12px] font-medium", tone.text)}>
      <span className={cn("h-1.5 w-1.5 rounded-full", tone.dot, tone.pulse && "animate-pulse")} />
      {tone.label}
    </span>
  );
}

/** In-flight deployments can be killed; terminal ones cannot. */
function isDeploymentInFlight(status: string): boolean {
  return status === "queued" || status === "building" || status === "pushing" || status === "deploying";
}

/** "1 week ago"-style timestamps for the history table. */
function relativeTime(value?: string): string {
  if (!value) return "—";
  const seconds = Math.max(0, (Date.now() - Date.parse(value)) / 1000);
  if (seconds < 60) return "just now";
  const minutes = seconds / 60;
  if (minutes < 60) return `${Math.round(minutes)}m ago`;
  const hours = minutes / 60;
  if (hours < 24) return `${Math.round(hours)}h ago`;
  const days = hours / 24;
  if (days < 7) return `${Math.round(days)}d ago`;
  return new Date(value).toLocaleDateString();
}

/** Build duration, live while the deployment is still running. */
function deploymentDuration(deployment: Deployment): string {
  const start = deployment.startedAt ?? deployment.queuedAt;
  if (!start) return "—";
  const end = deployment.finishedAt ? Date.parse(deployment.finishedAt) : Date.now();
  const seconds = Math.max(0, Math.round((end - Date.parse(start)) / 1000));
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  if (minutes === 0) return `${rest}s`;
  const hours = Math.floor(minutes / 60);
  if (hours === 0) return `${minutes}m ${String(rest).padStart(2, "0")}s`;
  return `${hours}h ${String(minutes % 60).padStart(2, "0")}m`;
}

/**
 * Build and deployment output. Live lines arrive over SSE and are appended to
 * the stored log so a refresh never loses the tail of a build. The header
 * carries refresh, download and find-in-logs controls like Coolify's viewer.
 */
function DeploymentLogs({
  deployment,
  serverName,
}: {
  deployment?: Deployment;
  serverName: string;
}) {
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [reload, setReload] = useState(0);
  const bottom = useRef<HTMLDivElement>(null);
  const deploymentId = deployment?.id;

  const cancel = useMutation({
    mutationFn: () => api.delete(`/deployments/${deploymentId}`),
    onSuccess: async () => {
      toast.success("Deployment canceled — the build was stopped");
      await queryClient.invalidateQueries({ queryKey: ["deployments"] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  useEffect(() => {
    setLines([]);
    if (!deploymentId) return;

    const controller = new AbortController();
    (async () => {
      try {
        const response = await fetch(`/api/deployments/${deploymentId}/logs`, {
          credentials: "same-origin",
          signal: controller.signal,
        });
        if (response.ok) setLines((await response.text()).split("\n"));

        for await (const event of streamEvents(`/api/stream?channel=deployment:${deploymentId}&replay=0`, controller.signal)) {
          if (event.event === "log" && typeof event.line === "string") {
            const line = event.line;
            setLines((current) => [...current, line]);
          }
        }
      } catch {
        // Streams are abandoned silently; a reconnect attempt follows.
      }
    })();

    return () => controller.abort();
  }, [deploymentId, reload]);

  const needle = query.trim().toLowerCase();
  const matches = needle ? lines.filter((line) => line.toLowerCase().includes(needle)).length : 0;

  useEffect(() => {
    if (!needle) bottom.current?.scrollIntoView({ block: "end" });
  }, [lines, needle]);

  if (!deployment) {
    return (
      <Card>
        <CardContent className="py-8 text-center text-[12px] text-black/45 dark:text-white/40">
          This application has never been deployed, so there are no build logs.
        </CardContent>
      </Card>
    );
  }

  const download = () => {
    const blob = new Blob([lines.join("\n")], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `deployment-${deployment.id.slice(0, 8)}.log`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Build output</CardTitle>
        <div className="flex flex-wrap items-center gap-3">
          <span className="hidden font-mono text-[11px] text-black/40 sm:block dark:text-white/35">
            {deployment.commitSha ? deployment.commitSha.slice(0, 7) : deployment.id.slice(0, 8)} ·{" "}
            {serverName}
          </span>
          <DeploymentStatus status={deployment.status} />
          {isDeploymentInFlight(deployment.status) && (
            <Button
              variant="danger"
              size="sm"
              disabled={cancel.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    "Kill this build? The build process is stopped, its helper container removed, and the deployment is canceled. The previous release keeps running.",
                  )
                ) {
                  cancel.mutate();
                }
              }}
            >
              <Scissors className="h-3.5 w-3.5" />
              Kill build
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="p-0">
        <TableToolbar
          left={
            <div className="relative max-w-sm">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-black/35 dark:text-white/30" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Find in logs"
                className="pl-8"
              />
            </div>
          }
          right={
            <>
              {needle && (
                <span className="text-[11px] text-black/45 dark:text-white/40">
                  {matches === 1 ? "1 match" : `${matches} matches`}
                </span>
              )}
              <Button
                variant="ghost"
                size="icon"
                title="Reload the log"
                onClick={() => setReload((value) => value + 1)}
              >
                <RefreshCw className="h-3.5 w-3.5" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                title="Download the log"
                onClick={download}
                disabled={lines.length === 0}
              >
                <Download className="h-3.5 w-3.5" />
              </Button>
            </>
          }
        />
        <pre className="max-h-[26rem] overflow-auto rounded-b-[8px] bg-black/40 p-3 font-mono text-[11px] leading-relaxed">
          {lines.map((line, index) => (
            <LogLine key={index} line={line} needle={needle} />
          ))}
          <div ref={bottom} />
        </pre>
      </CardContent>
    </Card>
  );
}

/** One log line with Coolify-style tones and search highlighting. */
function LogLine({ line, needle }: { line: string; needle: string }) {
  const match = needle !== "" && line.toLowerCase().includes(needle);
  const parts = match ? highlight(line, needle) : [line];
  return (
    <div className={cn(logTone(line), needle !== "" && !match && "opacity-40")}>
      {parts.map((part, index) =>
        index % 2 === 1 ? (
          <mark key={index} className="rounded-sm bg-warn/40 px-0.5 text-inherit">
            {part}
          </mark>
        ) : (
          <span key={index}>{part}</span>
        ),
      )}
    </div>
  );
}

const logTone = (line: string): string => {
  if (line.startsWith("[x]")) return "text-danger";
  if (line.startsWith("[ok]")) return "text-ok";
  if (line.startsWith("[!]")) return "text-warn";
  if (line.startsWith("[*]")) return "text-brand";
  return "text-white/75";
};

/** Splits a line so every occurrence of the needle can be highlighted. */
function highlight(line: string, needle: string): string[] {
  const lower = line.toLowerCase();
  const parts: string[] = [];
  let index = 0;
  for (;;) {
    const found = lower.indexOf(needle, index);
    if (found < 0) {
      parts.push(line.slice(index));
      return parts;
    }
    parts.push(line.slice(index, found), line.slice(found, found + needle.length));
    index = found + needle.length;
  }
}

/** The header's Actions dropdown, mirroring Coolify's redeploy menu. */
function ActionsMenu({
  deployPending,
  busy,
  onRedeploy,
  onRedeployNoCache,
  onRestart,
  onStop,
}: {
  deployPending: boolean;
  busy: boolean;
  onRedeploy: () => void;
  onRedeployNoCache: () => void;
  onRestart: () => void;
  onStop: () => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDocumentClick = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDocumentClick);
    return () => document.removeEventListener("mousedown", onDocumentClick);
  }, [open]);

  const item =
    "flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12px] transition-colors hover:bg-black/[0.04] disabled:opacity-50 dark:hover:bg-white/[0.05]";
  const run = (action: () => void) => () => {
    setOpen(false);
    action();
  };

  return (
    <div className="relative" ref={ref}>
      <Button variant="neutral" onClick={() => setOpen((value) => !value)}>
        Actions
        <ChevronDown className={cn("h-3.5 w-3.5 transition-transform", open && "rotate-180")} />
      </Button>
      {open && (
        <div className="absolute right-0 z-40 mt-1 w-56 overflow-hidden rounded-md border border-line/70 bg-base py-1 shadow-lg">
          <button className={item} onClick={run(onRedeploy)} disabled={deployPending || busy}>
            <Play className="h-3.5 w-3.5" />
            Redeploy
          </button>
          <button className={item} onClick={run(onRedeployNoCache)} disabled={deployPending || busy}>
            <Play className="h-3.5 w-3.5" />
            Redeploy (without cache)
          </button>
          <div className="my-1 border-t border-line/50" />
          <button className={item} onClick={run(onRestart)}>
            <RotateCcw className="h-3.5 w-3.5" />
            Restart
          </button>
          <button className={item} onClick={run(onStop)}>
            <Square className="h-3.5 w-3.5" />
            Stop
          </button>
        </div>
      )}
    </div>
  );
}

/**
 * Container stdout/stderr. The endpoint streams plain text as the process
 * writes, so "Stream live" follows the application in real time — the
 * counterpart to Coolify's Runtime Logs view.
 */
function RuntimeLogs({ applicationId, running }: { applicationId: string; running: boolean }) {
  const [lines, setLines] = useState<string[]>([]);
  const [tail, setTail] = useState("500");
  const [follow, setFollow] = useState(true);
  const [query, setQuery] = useState("");
  const [reload, setReload] = useState(0);
  const [error, setError] = useState("");
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setLines([]);
    setError("");
    const controller = new AbortController();
    (async () => {
      try {
        const response = await fetch(
          `/api/applications/${applicationId}/logs?tail=${encodeURIComponent(tail)}${
            follow ? "&follow=1" : ""
          }`,
          { credentials: "same-origin", signal: controller.signal },
        );
        if (!response.ok) {
          setError(`request failed with ${response.status}`);
          return;
        }
        if (!response.body) return;
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const parts = buffer.split("\n");
          buffer = parts.pop() ?? "";
          if (parts.length > 0) setLines((current) => [...current, ...parts]);
        }
        if (buffer !== "") setLines((current) => [...current, buffer]);
      } catch {
        // Aborted on unmount or by a control change; the next effect retries.
      }
    })();
    return () => controller.abort();
  }, [applicationId, tail, follow, reload]);

  const needle = query.trim().toLowerCase();
  const matches = needle ? lines.filter((line) => line.toLowerCase().includes(needle)).length : 0;

  useEffect(() => {
    if (!needle) bottom.current?.scrollIntoView({ block: "end" });
  }, [lines, needle]);

  const download = () => {
    const blob = new Blob([lines.join("\n")], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "runtime.log";
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Runtime logs</CardTitle>
        {!running && <span className="text-[11px] text-warn">container is not running</span>}
      </CardHeader>
      <CardContent className="p-0">
        <TableToolbar
          left={
            <div className="relative max-w-sm">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-black/35 dark:text-white/30" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Find in logs"
                className="pl-8"
              />
            </div>
          }
          right={
            <>
              {needle && (
                <span className="text-[11px] text-black/45 dark:text-white/40">
                  {matches === 1 ? "1 match" : `${matches} matches`}
                </span>
              )}
              <label className="flex items-center gap-1.5 text-[11px] text-black/55 dark:text-white/50">
                <input
                  type="checkbox"
                  checked={follow}
                  onChange={(event) => setFollow(event.target.checked)}
                />
                Stream live
              </label>
              <select
                value={tail}
                onChange={(event) => setTail(event.target.value)}
                className="h-7 rounded-md bg-recessed px-1.5 text-[11px] ring-1 ring-line/70 outline-none focus:ring-2 focus:ring-brand"
              >
                <option value="100">100 lines</option>
                <option value="500">500 lines</option>
                <option value="2000">2000 lines</option>
                <option value="all">All</option>
              </select>
              <Button
                variant="ghost"
                size="icon"
                title="Reload the log"
                onClick={() => setReload((value) => value + 1)}
              >
                <RefreshCw className="h-3.5 w-3.5" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                title="Download the log"
                onClick={download}
                disabled={lines.length === 0}
              >
                <Download className="h-3.5 w-3.5" />
              </Button>
            </>
          }
        />
        <pre className="max-h-[26rem] overflow-auto rounded-b-[8px] bg-black/40 p-3 font-mono text-[11px] leading-relaxed">
          {error !== "" && <div className="text-danger">{error}</div>}
          {lines.length === 0 && error === "" && (
            <div className="text-white/40">
              {running
                ? follow
                  ? "Streaming — output appears as the application writes it."
                  : "No output in the selected window."
                : "Start the application to see runtime logs."}
            </div>
          )}
          {lines.map((line, index) => (
            <LogLine key={index} line={line} needle={needle} />
          ))}
          <div ref={bottom} />
        </pre>
      </CardContent>
    </Card>
  );
}

function EnvPanel({ applicationId, vars }: { applicationId: string; vars: EnvVar[] }) {
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [isSecret, setIsSecret] = useState(false);
  const queryClient = useQueryClient();

  const save = useMutation({
    mutationFn: () => api.put(`/applications/${applicationId}/env`, { key, value, isSecret }),
    onSuccess: async () => {
      setKey("");
      setValue("");
      await queryClient.invalidateQueries({ queryKey: ["env", applicationId] });
    },
  });

  return (
    <div className="grid gap-3 lg:grid-cols-[1fr_320px]">
      <Card>
        <CardHeader>
          <CardTitle>Variables</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Key</Th>
                <Th>Secret</Th>
                <Th>Scope</Th>
              </>
            }
          >
            {vars.map((variable) => (
              <Tr key={variable.id}>
                <Td className="font-mono text-[12px]">{variable.key}</Td>
                <Td>{variable.isSecret ? "yes" : "no"}</Td>
                <Td className="text-black/55 dark:text-white/45">
                  {variable.isBuild ? "build" : "runtime"}
                </Td>
              </Tr>
            ))}
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Add variable</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <div>
            <label className="mb-1 block text-[11px] text-black/60 dark:text-white/55">Key</label>
            <input
              value={key}
              onChange={(event) => setKey(event.target.value)}
              placeholder="DATABASE_URL"
              className="h-8 w-full rounded-md bg-recessed px-2.5 font-mono text-[12px] ring-1 ring-line/70 outline-none focus:ring-2 focus:ring-brand"
            />
          </div>
          <div>
            <label className="mb-1 block text-[11px] text-black/60 dark:text-white/55">Value</label>
            <input
              value={value}
              onChange={(event) => setValue(event.target.value)}
              type={isSecret ? "password" : "text"}
              className="h-8 w-full rounded-md bg-recessed px-2.5 font-mono text-[12px] ring-1 ring-line/70 outline-none focus:ring-2 focus:ring-brand"
            />
          </div>
          <label className="flex items-center gap-2 text-[12px]">
            <input
              type="checkbox"
              checked={isSecret}
              onChange={(event) => setIsSecret(event.target.checked)}
            />
            Store encrypted
          </label>
          <Button
            variant="primary"
            className="w-full"
            disabled={!key.trim()}
            onClick={() => save.mutate()}
          >
            Save variable
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

/**
 * The full update payload for an application. PUT replaces every field, so the
 * stored record supplies the defaults and the card supplies its overrides.
 */
function applicationPayload(app: Application, overrides: Record<string, unknown>) {
  return {
    name: app.name,
    description: app.description,
    gitRepository: app.gitRepository,
    gitBranch: app.gitBranch,
    buildPack: app.buildPack,
    dockerfile: app.dockerfile,
    baseDirectory: app.baseDirectory,
    port: app.port,
    healthCheck: app.healthCheck,
    nodeId: app.nodeId,
    autoDeploy: app.autoDeploy,
    portMappings: app.portMappings,
    redirectHttps: app.redirectHttps,
    cpuLimit: app.cpuLimit,
    memoryLimit: app.memoryLimit,
    resourceGuardEnabled: app.resourceGuardEnabled,
    resourceGuardCpuAction: app.resourceGuardCpuAction,
    resourceGuardMemoryAction: app.resourceGuardMemoryAction,
    resourceGuardCpuPercent: app.resourceGuardCpuPercent,
    resourceGuardMemoryPercent: app.resourceGuardMemoryPercent,
    preDeployCommand: app.preDeployCommand,
    postDeployCommand: app.postDeployCommand,
    ...overrides,
  };
}

/** "512m" / "2g" / plain bytes → bytes; junk means "leave the engine default". */
function parseMemoryLimit(value: string): number {
  const raw = value.trim().toLowerCase();
  if (raw === "") return 0;
  let multiplier = 1;
  let number = raw;
  const suffix = raw.slice(-1);
  if (suffix === "k") {
    multiplier = 1024;
    number = raw.slice(0, -1);
  } else if (suffix === "m") {
    multiplier = 1024 * 1024;
    number = raw.slice(0, -1);
  } else if (suffix === "g") {
    multiplier = 1024 * 1024 * 1024;
    number = raw.slice(0, -1);
  }
  const parsed = Number(number.trim());
  if (!Number.isFinite(parsed) || parsed < 0) return 0;
  return Math.round(parsed * multiplier);
}

/** Coolify-style Networking card: the port the app listens on and what is published. */
function NetworkingCard({ app, applicationId }: { app: Application; applicationId: string }) {
  const queryClient = useQueryClient();
  const [port, setPort] = useState(String(app.port));
  const [mappings, setMappings] = useState(app.portMappings);

  useEffect(() => {
    setPort(String(app.port));
    setMappings(app.portMappings);
  }, [app.id, app.port, app.portMappings]);

  const labels = useQuery({
    queryKey: ["application-labels", applicationId],
    queryFn: () => api.get<string[]>(`/applications/${applicationId}/labels`),
  });

  const save = useMutation({
    mutationFn: () =>
      api.put<Application>(
        `/applications/${applicationId}`,
        applicationPayload(app, { port: Number(port) || 0, portMappings: mappings.trim() }),
      ),
    onSuccess: async () => {
      toast.success("Networking saved — redeploy to apply");
      await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
      await queryClient.invalidateQueries({ queryKey: ["application-labels", applicationId] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  return (
    <div className="space-y-3">
      <Card>
        <CardHeader>
          <CardTitle>Networking</CardTitle>
          <Link
            to="/help"
            className="text-[11px] font-medium text-brand hover:underline"
          >
            Port binding help
          </Link>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <Label htmlFor="net-port">Container port</Label>
              <Input
                id="net-port"
                type="number"
                value={port}
                onChange={(event) => setPort(event.target.value)}
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                The port the application listens on inside the container; the proxy routes to it.
                Meghify passes this as <code>PORT</code> unless the app already defines it.
                Containers have isolated networks, so different apps can both use 3000.
              </p>
            </div>
            <div>
              <Label htmlFor="net-mappings">Port mappings</Label>
              <Input
                id="net-mappings"
                value={mappings}
                onChange={(event) => setMappings(event.target.value)}
                placeholder="8085:3000"
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                Publish container ports on the host: hostPort:containerPort, comma separated.
              </p>
            </div>
          </div>
          <div className="space-y-1.5">
            <Row label="Container name" value={containerName(app)} />
            <Row label="Docker network" value={networkName(app)} />
            <Row label="Network alias" value={containerName(app)} />
          </div>
          <div className="flex justify-end border-t border-line/40 pt-3">
            <Button variant="primary" size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              Save changes
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Proxy routing</CardTitle>
          <span className="text-[11px] text-black/40 dark:text-white/35">
            the config Traefik serves now — domain edits apply instantly
          </span>
        </CardHeader>
        <CardContent>
          {labels.isLoading ? (
            <p className="text-[12px] text-black/45 dark:text-white/40">Loading routing…</p>
          ) : (
            <pre className="max-h-72 overflow-auto rounded-md bg-black/40 p-3 font-mono text-[11px] leading-relaxed text-white/75">
              {(labels.data ?? []).join("\n") || "no routing"}
            </pre>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

/** Coolify-style Runtime card: container limits and deployment lifecycle hooks. */
function RuntimeCard({ app, applicationId }: { app: Application; applicationId: string }) {
  const queryClient = useQueryClient();
  const [cpu, setCpu] = useState(app.cpuLimit ? String(app.cpuLimit) : "");
  const [memory, setMemory] = useState(app.memoryLimit ? String(app.memoryLimit) : "");
  const [pre, setPre] = useState(app.preDeployCommand);
  const [post, setPost] = useState(app.postDeployCommand);

  useEffect(() => {
    setCpu(app.cpuLimit ? String(app.cpuLimit) : "");
    setMemory(app.memoryLimit ? String(app.memoryLimit) : "");
    setPre(app.preDeployCommand);
    setPost(app.postDeployCommand);
  }, [app.id, app.cpuLimit, app.memoryLimit, app.preDeployCommand, app.postDeployCommand]);

  const save = useMutation({
    mutationFn: () =>
      api.put<Application>(
        `/applications/${applicationId}`,
        applicationPayload(app, {
          cpuLimit: Number(cpu) || 0,
          memoryLimit: parseMemoryLimit(memory),
          preDeployCommand: pre.trim(),
          postDeployCommand: post.trim(),
        }),
      ),
    onSuccess: async () => {
      toast.success("Runtime saved — live limits updated where the container is running");
      await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
    onError: (error) => toast.error(error.message),
  });

  const apply = useMutation({
    mutationFn: () => api.post<Application>(`/applications/${applicationId}/apply-limits`),
    onSuccess: () => toast.success("Saved CPU and memory limits applied to the running container"),
    onError: (error) => toast.error(error.message),
  });

  return (
    <div className="space-y-3">
      <Card>
        <CardHeader>
          <CardTitle>Runtime</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <Label htmlFor="rt-cpu">CPU limit</Label>
              <Input
                id="rt-cpu"
                value={cpu}
                onChange={(event) => setCpu(event.target.value)}
                placeholder="unlimited"
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                Fraction of one core, e.g. 1.5. Empty keeps the engine default.
              </p>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {["0.5", "1", "2"].map((preset) => (
                  <button
                    key={preset}
                    type="button"
                    onClick={() => setCpu(preset)}
                    className="rounded-full border border-line/60 px-2 py-0.5 text-[10.5px] text-black/50 transition-colors hover:bg-black/[0.04] hover:text-current dark:text-white/45 dark:hover:bg-white/[0.05]"
                  >
                    {preset} core{preset === "1" ? "" : "s"}
                  </button>
                ))}
              </div>
            </div>
            <div>
              <Label htmlFor="rt-memory">Memory limit</Label>
              <Input
                id="rt-memory"
                value={memory}
                onChange={(event) => setMemory(event.target.value)}
                placeholder="512m · 2g · bytes"
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                Accepts 512m, 2g or a plain byte count. Empty keeps the engine default.
              </p>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {["256m", "512m", "1g", "2g"].map((preset) => (
                  <button
                    key={preset}
                    type="button"
                    onClick={() => setMemory(preset)}
                    className="rounded-full border border-line/60 px-2 py-0.5 text-[10.5px] text-black/50 transition-colors hover:bg-black/[0.04] hover:text-current dark:text-white/45 dark:hover:bg-white/[0.05]"
                  >
                    {preset}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <p className="rounded-md bg-white/[0.02] px-3 py-2 text-[11px] leading-relaxed text-black/45 dark:text-white/40">
            Saved limits are pushed to the running container immediately when the engine is reachable. They also apply to every future deployment.
          </p>

          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <Label htmlFor="rt-pre">Pre-deployment command</Label>
              <Textarea
                id="rt-pre"
                rows={2}
                value={pre}
                onChange={(event) => setPre(event.target.value)}
                placeholder="php artisan down"
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                Runs in the previous container before the swap; a failure aborts the deployment.
              </p>
            </div>
            <div>
              <Label htmlFor="rt-post">Post-deployment command</Label>
              <Textarea
                id="rt-post"
                rows={2}
                value={post}
                onChange={(event) => setPost(event.target.value)}
                placeholder="php artisan migrate --force"
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                Runs in the new container once it is live; a failure marks the deployment failed.
              </p>
            </div>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line/40 pt-3">
            <p className="text-[10.5px] text-black/40 dark:text-white/35">
              Applying limits also clears a temporary resource-guard CPU throttle.
            </p>
            <div className="flex items-center gap-2">
              <Button
                variant="neutral"
                size="sm"
                disabled={apply.isPending}
                onClick={() => apply.mutate()}
              >
                {apply.isPending ? "Applying…" : "Apply limits now"}
              </Button>
              <Button variant="primary" size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
                Save changes
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

/** Coolify-style Danger zone: a two-step delete with cleanup options. */
function DangerCard({ app, applicationId }: { app: Application; applicationId: string }) {
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <Card className="border-danger/25">
      <CardHeader>
        <CardTitle>Danger zone</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-danger/25 bg-danger/5 p-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-[12.5px] font-medium">
              Delete application
              <span className="rounded-full bg-danger/15 px-1.5 py-0.5 text-[10px] font-medium text-danger">
                Permanent
              </span>
            </div>
            <p className="mt-1 max-w-xl text-[11px] leading-relaxed text-black/50 dark:text-white/45">
              Permanently delete {app.name}, stop its containers, and remove the selected Docker
              resources and configuration. Active deployments stop, and the application cannot be
              restored.
            </p>
          </div>
          <Button variant="danger" onClick={() => setDeleteOpen(true)}>
            <Trash2 className="h-4 w-4" />
            Delete application
          </Button>
        </div>
      </CardContent>

      <DeleteApplicationDialog
        app={app}
        applicationId={applicationId}
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
      />
    </Card>
  );
}

/**
 * The two-step delete: pick the cleanup options first, then type the resource
 * name to confirm — the same guard rails Coolify uses.
 */
function DeleteApplicationDialog({
  app,
  applicationId,
  open,
  onClose,
}: {
  app: Application;
  applicationId: string;
  open: boolean;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [step, setStep] = useState<1 | 2>(1);
  const [options, setOptions] = useState({ volumes: true, networks: true, config: true, cleanup: true });
  const [confirmName, setConfirmName] = useState("");

  useEffect(() => {
    if (!open) return;
    setStep(1);
    setConfirmName("");
    setOptions({ volumes: true, networks: true, config: true, cleanup: true });
  }, [open]);

  const remove = useMutation({
    mutationFn: () =>
      api.delete<{
        volumesRemoved: number;
        networkRemoved: boolean;
        configRemoved: boolean;
        spaceReclaimed: number;
      }>(
        `/applications/${applicationId}?volumes=${options.volumes ? 1 : 0}&networks=${
          options.networks ? 1 : 0
        }&config=${options.config ? 1 : 0}&cleanup=${options.cleanup ? 1 : 0}`,
      ),
    onSuccess: async () => {
      toast.success(`${app.name} deleted`);
      await queryClient.invalidateQueries({ queryKey: ["applications"] });
      navigate({ to: "/projects" });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const toggle = (key: keyof typeof options, label: string) => (
    <label className="flex items-center justify-between gap-3 rounded-md bg-recessed px-3 py-2 text-[12px]">
      {label}
      <input
        type="checkbox"
        checked={options[key]}
        onChange={(event) => setOptions({ ...options, [key]: event.target.checked })}
      />
    </label>
  );

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Delete application?"
      footer={
        step === 1 ? (
          <>
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => setStep(2)}>
              Continue
            </Button>
          </>
        ) : (
          <>
            <Button variant="neutral" onClick={() => setStep(1)}>
              Back
            </Button>
            <Button
              variant="danger"
              disabled={confirmName !== app.name || remove.isPending}
              onClick={() => remove.mutate()}
            >
              Continue
            </Button>
          </>
        )
      }
    >
      {step === 1 ? (
        <div className="space-y-2">
          {toggle("volumes", "Permanently delete all volumes associated with this resource.")}
          {toggle("networks", "Permanently delete all non-predefined networks associated with this resource.")}
          {toggle("config", "Permanently delete all configuration files from the server.")}
          {toggle("cleanup", "Run Docker Cleanup (remove unused images and builder cache).")}
        </div>
      ) : (
        <div className="space-y-3">
          <div className="rounded-md border border-danger/40 bg-danger/10 px-3 py-2.5">
            <div className="flex items-center gap-2 text-[12px] font-semibold text-danger">
              <TriangleAlert className="h-3.5 w-3.5" />
              Warning
            </div>
            <p className="mt-1 text-[11.5px] leading-relaxed text-danger/90">
              This operation is permanent and cannot be undone. Please think again before
              proceeding!
            </p>
          </div>

          <div>
            <div className="text-[12px] font-semibold">The following actions will be performed:</div>
            <ul className="mt-1.5 space-y-1 text-[11.5px] text-danger/90">
              <li>- Permanently delete this resource and its selected Docker resources.</li>
              {options.volumes && (
                <li>- Permanently delete all volumes associated with this resource.</li>
              )}
              {options.networks && (
                <li>- Permanently delete all non-predefined networks associated with this resource.</li>
              )}
              {options.config && <li>- Permanently delete all configuration files from the server.</li>}
              {options.cleanup && (
                <li>- Run Docker Cleanup (remove unused images and builder cache).</li>
              )}
            </ul>
          </div>

          <div>
            <div className="text-[12px] font-semibold">Confirm actions</div>
            <p className="mt-0.5 text-[11px] text-black/50 dark:text-white/45">
              Enter the resource name to confirm permanent deletion
            </p>
            <div className="mt-1.5 flex items-center justify-between gap-3 rounded-md border border-line/60 bg-recessed px-3 py-2 font-mono text-[12px]">
              <span className="truncate">{app.name}</span>
              <button
                type="button"
                title="Copy the name"
                className="shrink-0 text-black/45 transition-colors hover:text-current dark:text-white/40"
                onClick={() => navigator.clipboard?.writeText(app.name)}
              >
                <Copy className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          <div>
            <Label htmlFor="confirm-delete-name">Resource name</Label>
            <Input
              id="confirm-delete-name"
              value={confirmName}
              onChange={(event) => setConfirmName(event.target.value)}
              autoFocus
            />
          </div>
        </div>
      )}
    </Modal>
  );
}

export const Route = createFileRoute("/applications/$applicationId")({
  component: ApplicationDetailPage,
});
