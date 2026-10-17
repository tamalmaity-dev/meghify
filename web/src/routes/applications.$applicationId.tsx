import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { ChevronDown, Download, Github, Play, RefreshCw, RotateCcw, Search, Square, Terminal, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api, streamEvents } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { XTerminal } from "@/components/terminal";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Textarea } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import { Table, TableToolbar, Td, Th, Tr } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type {
  Application,
  Deployment,
  Domain,
  EnvVar,
  Node as ServerNode,
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

export function ApplicationDetailPage() {
  const { applicationId } = useParams({ from: "/applications/$applicationId" });
  const queryClient = useQueryClient();
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
  const watched =
    (pickedDeployment && deployments.data?.find((d) => d.id === pickedDeployment)) || latest;
  const serverName = application?.nodeId
    ? (nodes.data?.find((node) => node.id === application.nodeId)?.name ?? "remote")
    : "localhost";
  // The automatic sslip.io domain is plain HTTP, so the header link follows
  // the domain's TLS setting rather than assuming HTTPS.
  const primaryDomain = domains.data?.find((domain) => domain.host === application?.fqdn);
  const fqdnScheme = primaryDomain && !primaryDomain.tlsEnabled ? "http" : "https";

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
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setSection("terminal")}
              disabled={application?.status !== "running"}
              title="Open terminal"
            >
              <Terminal className="h-4 w-4" />
            </Button>
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

      <div className="grid gap-4 lg:grid-cols-[200px_minmax(0,1fr)]">
        <nav className="flex gap-1 overflow-x-auto lg:flex-col lg:overflow-visible">
          {SECTIONS.map((item) => (
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
                <ApplicationDetailsCard app={application} applicationId={applicationId} />
                {application.repositoryFullName && (
                  <GitHubCard app={application} applicationId={applicationId} />
                )}
                <AccessCard
                  app={application}
                  domains={domains.data ?? []}
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
            <RuntimeCard app={application} applicationId={applicationId} />
          )}

          {section === "danger" && application && (
            <DangerCard app={application} applicationId={applicationId} />
          )}

          {section === "domains" && (
            <DomainsCard applicationId={applicationId} domains={domains.data ?? []} />
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
                        <Th>Host path</Th>
                        <Th>Mount path</Th>
                        <Th align="right">Read only</Th>
                      </>
                    }
                  >
                    {(volumes.data ?? []).map((volume) => (
                      <Tr key={volume.id}>
                        <Td>{volume.name}</Td>
                        <Td className="font-mono text-[12px] text-black/55 dark:text-white/45">
                          {volume.hostPath || "-"}
                        </Td>
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
function ApplicationDetailsCard({ app, applicationId }: { app: Application; applicationId: string }) {
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
            <Input id="application-name" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          <div>
            <Label htmlFor="application-description">Description</Label>
            <Input
              id="application-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="What does this application do?"
            />
          </div>
        </div>
        {save.isError && <p className="text-[11px] text-danger">{errorText(save.error)}</p>}
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
      api.put<Application>(`/applications/${applicationId}`, {
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
        autoDeploy: enabled,
      }),
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
  onManageDomains,
  onEditNetworking,
}: {
  app: Application;
  domains: Domain[];
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
              <Button variant="neutral" size="sm" onClick={onManageDomains}>
                Manage
              </Button>
            </div>
          ) : (
            <p className="mt-1 text-black/45 dark:text-white/40">
              No public URL yet.{" "}
              <button className="text-brand hover:underline" onClick={onManageDomains}>
                Add a domain
              </button>{" "}
              to expose this application over HTTP.
            </p>
          )}
        </div>

        <div>
          <div className="flex items-center justify-between">
            <div className="text-[11px] tracking-wider text-black/40 uppercase dark:text-white/35">
              Internal access
            </div>
            <Button variant="neutral" size="sm" onClick={onEditNetworking}>
              Edit networking
            </Button>
          </div>
          <div className="mt-1.5 space-y-1.5">
            <Row label="Container name" value={`meghify-${sanitizeName(app.name)}`} />
            <Row label="Docker network" value={`meghify-${sanitizeName(app.name)}`} />
            <Row label="Exposed port" value={String(app.port)} />
            <Row label="Port mappings" value={app.portMappings || "none"} />
          </div>
          <p className="mt-2 text-[10.5px] text-black/35 dark:text-white/30">
            Reachable from other containers on the same network as{" "}
            <span className="font-mono">
              meghify-{sanitizeName(app.name)}:{app.port}
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

function DomainsCard({ applicationId, domains }: { applicationId: string; domains: Domain[] }) {
  const queryClient = useQueryClient();
  const [host, setHost] = useState("");

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ["domains", applicationId] });
    await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
  };

  const add = useMutation({
    mutationFn: () => api.post(`/applications/${applicationId}/domains`, { host: host.trim() }),
    onSuccess: async () => {
      setHost("");
      toast.success("Domain added — re-deploy to attach it to the proxy");
      await invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (domainId: string) => api.delete(`/applications/domains/${domainId}`),
    onSuccess: async () => {
      toast.success("Domain removed");
      await invalidate();
    },
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Domains</CardTitle>
        <span className="text-[11px] text-black/40 dark:text-white/35">
          Point an A record at this server, then re-deploy
        </span>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            value={host}
            onChange={(event) => setHost(event.target.value)}
            placeholder="app.example.com"
            className="max-w-xs flex-1"
          />
          <Button
            variant="primary"
            size="sm"
            disabled={host.trim() === "" || add.isPending}
            onClick={() => add.mutate()}
          >
            Add domain
          </Button>
        </div>
        {add.isError && <p className="text-[11px] text-danger">{errorText(add.error)}</p>}

        {domains.length === 0 ? (
          <p className="rounded-md bg-recessed px-3 py-4 text-center text-[12px] text-black/45 dark:text-white/40">
            No domains yet. Add one above to publish this application.
          </p>
        ) : (
          <div className="space-y-1.5">
            {domains.map((domain) => (
              <div
                key={domain.id}
                className="flex items-center justify-between gap-3 rounded-md bg-recessed px-3 py-2"
              >
                <div className="min-w-0">
                  <div className="truncate font-mono text-[12px]">{domain.host}</div>
                  <div className="text-[10.5px] text-black/40 dark:text-white/30">
                    {domain.tlsEnabled ? `TLS · ${domain.certStatus || "pending"}` : "TLS disabled"}
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(domain.id)}
                >
                  Remove
                </Button>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
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
  const [lines, setLines] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [reload, setReload] = useState(0);
  const bottom = useRef<HTMLDivElement>(null);
  const deploymentId = deployment?.id;

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
    cpuLimit: app.cpuLimit,
    memoryLimit: app.memoryLimit,
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
            <Row label="Container name" value={`meghify-${sanitizeName(app.name)}`} />
            <Row label="Docker network" value={`meghify-${sanitizeName(app.name)}`} />
            <Row label="Network alias" value={sanitizeName(app.name)} />
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
          <CardTitle>Container labels</CardTitle>
          <span className="text-[11px] text-black/40 dark:text-white/35">
            attached on the next deploy
          </span>
        </CardHeader>
        <CardContent>
          {labels.isLoading ? (
            <p className="text-[12px] text-black/45 dark:text-white/40">Loading labels…</p>
          ) : (
            <pre className="max-h-72 overflow-auto rounded-md bg-black/40 p-3 font-mono text-[11px] leading-relaxed text-white/75">
              {(labels.data ?? []).join("\n") || "no labels"}
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
      toast.success("Runtime saved — applied on the next deploy");
      await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
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
            </div>
          </div>

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

          <div className="flex justify-end border-t border-line/40 pt-3">
            <Button variant="primary" size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              Save changes
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

/** Coolify-style Danger zone: delete the application and stop its container. */
function DangerCard({ app, applicationId }: { app: Application; applicationId: string }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const remove = useMutation({
    mutationFn: () => api.delete(`/applications/${applicationId}`),
    onSuccess: async () => {
      toast.success(`${app.name} deleted`);
      await queryClient.invalidateQueries({ queryKey: ["applications"] });
      navigate({ to: "/projects" });
    },
    onError: (error) => toast.error(errorText(error)),
  });

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
              Stops and removes the container, then deletes {app.name} with its configuration,
              domains, variables and deployment history. Volumes and stored data stay on disk.
            </p>
          </div>
          <Button
            variant="danger"
            disabled={remove.isPending}
            onClick={() => {
              if (
                window.confirm(
                  `Delete "${app.name}"? Its container is stopped and removed, and the application cannot be restored. Volumes stay on disk.`,
                )
              ) {
                remove.mutate();
              }
            }}
          >
            <Trash2 className="h-4 w-4" />
            Delete application
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export const Route = createFileRoute("/applications/$applicationId")({
  component: ApplicationDetailPage,
});
