import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { Github, Play, RotateCcw, Square, Terminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api, streamEvents } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { Application, Deployment, Domain, EnvVar, Volume } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const SECTIONS = [
  { key: "general", label: "General" },
  { key: "domains", label: "Domains" },
  { key: "environment", label: "Environment" },
  { key: "volumes", label: "Persistent storage" },
  { key: "logs", label: "Logs" },
  { key: "deployments", label: "Deployments" },
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
  const [terminalOpen, setTerminalOpen] = useState(false);

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
    mutationFn: () => api.post<Deployment>(`/applications/${applicationId}/deploy`, { noCache: false }),
    onSuccess: () => {
      toast.success("Deployment queued");
      queryClient.invalidateQueries({ queryKey: ["deployments", applicationId] });
      queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
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
              onClick={() => setTerminalOpen(true)}
              disabled={application?.status !== "running"}
              title="Open terminal"
            >
              <Terminal className="h-4 w-4" />
            </Button>
            <Button variant="neutral" onClick={runAction(`/applications/${applicationId}/restart`)}>
              <RotateCcw className="h-4 w-4" />
              Restart
            </Button>
            <Button
              variant="primary"
              onClick={() => deploy.mutate()}
              disabled={deploy.isPending || Boolean(active)}
            >
              <Play className="h-4 w-4" />
              Deploy
            </Button>
            <Button variant="ghost" size="icon" onClick={runAction(`/applications/${applicationId}/stop`)}>
              <Square className="h-4 w-4" />
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
            href={`https://${application.fqdn}`}
            target="_blank"
            rel="noreferrer"
            className="text-[12px] text-brand hover:underline"
          >
            {application.fqdn}
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

          {section === "logs" && <DeploymentLogs deploymentId={latest?.id} />}

          {section === "deployments" && (
            <Card>
              <CardHeader>
                <CardTitle>History</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                {(deployments.data ?? []).length === 0 ? (
                  <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                    No deployments yet.
                  </p>
                ) : (
                  <Table
                    head={
                      <>
                        <Th>Status</Th>
                        <Th>Trigger</Th>
                        <Th>Commit</Th>
                        <Th align="right">Finished</Th>
                      </>
                    }
                  >
                    {(deployments.data ?? []).map((deployment) => (
                      <Tr key={deployment.id}>
                        <Td>
                          <StatusBadge status={deployment.status} />
                        </Td>
                        <Td className="text-black/55 dark:text-white/45">{deployment.trigger}</Td>
                        <Td className="font-mono text-[12px] text-black/55 dark:text-white/45">
                          {deployment.commitSha?.slice(0, 8) || "-"}
                        </Td>
                        <Td align="right" className="text-black/45 dark:text-white/40">
                          {deployment.finishedAt
                            ? new Date(deployment.finishedAt).toLocaleString()
                            : "-"}
                        </Td>
                      </Tr>
                    ))}
                  </Table>
                )}
              </CardContent>
            </Card>
          )}
        </div>
      </div>

      {terminalOpen && (
        <TerminalModal applicationId={applicationId} onClose={() => setTerminalOpen(false)} />
      )}
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
      api.put<Application>(`/applications/${applicationId}`, {
        name: name.trim(),
        description,
        gitRepository: app.gitRepository,
        gitBranch: app.gitBranch,
        buildPack: app.buildPack,
        dockerfile: app.dockerfile,
        baseDirectory: app.baseDirectory,
        port: app.port,
        healthCheck: app.healthCheck,
        nodeId: app.nodeId,
        autoDeploy: app.autoDeploy,
      }),
    onSuccess: async () => {
      toast.success("Application updated");
      await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
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
}: {
  app: Application;
  domains: Domain[];
  onManageDomains: () => void;
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
          <div className="text-[11px] tracking-wider text-black/40 uppercase dark:text-white/35">
            Internal access
          </div>
          <div className="mt-1.5 space-y-1.5">
            <Row label="Container name" value={`meghify-${sanitizeName(app.name)}`} />
            <Row label="Docker network" value={`meghify-${sanitizeName(app.name)}`} />
            <Row label="Exposed port" value={String(app.port)} />
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
 * Build and deployment output. Live lines arrive over SSE and are appended to the
 * stored log so a refresh never loses the tail of a build.
 */
function DeploymentLogs({ deploymentId }: { deploymentId?: string }) {
  const [lines, setLines] = useState<string[]>([]);
  const bottom = useRef<HTMLDivElement>(null);

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

        for await (const event of streamEvents(`/api/stream?channel=deployment:${deploymentId}`, controller.signal)) {
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
  }, [deploymentId]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [lines]);

  if (!deploymentId) {
    return (
      <Card>
        <CardContent className="py-8 text-center text-[12px] text-black/45 dark:text-white/40">
          This application has never been deployed, so there are no build logs.
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Build output</CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <pre className="max-h-[26rem] overflow-auto rounded-b-[8px] bg-black/40 p-3 font-mono text-[11px] leading-relaxed text-white/80">
          {lines.join("\n")}
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
 * An interactive container shell over a WebSocket. This is the one place the UI
 * uses a WebSocket, because terminal input has to travel in both directions.
 */
function TerminalModal({
  applicationId,
  onClose,
}: {
  applicationId: string;
  onClose: () => void;
}) {
  const host = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!host.current) return;
    let disposed = false;
    let socket: WebSocket | undefined;

    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (disposed || !host.current) return;

      const term = new Terminal({
        cursorBlink: true,
        fontSize: 12,
        fontFamily: "var(--font-mono)",
        theme: { background: "#0a0a0a", foreground: "#e5e5e5" },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(host.current);
      fit.fit();

      const protocol = window.location.protocol === "https:" ? "wss" : "ws";
      socket = new WebSocket(
        `${protocol}://${window.location.host}/api/applications/${applicationId}/terminal`,
      );
      socket.binaryType = "arraybuffer";

      socket.addEventListener("open", () => term.writeln("\r\nConnected to container.\r\n"));
      socket.addEventListener("message", (event) => {
        if (event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data));
          return;
        }
        term.writeln(String(event.data));
      });
      socket.addEventListener("close", () => term.writeln("\r\nDisconnected."));

      term.onData((data) => {
        if (socket?.readyState === WebSocket.OPEN) socket.send(data);
      });
      term.onResize(({ rows, cols }) => {
        if (socket?.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "resize", rows, cols }));
        }
      });

      const onResize = () => fit.fit();
      window.addEventListener("resize", onResize);
      return () => window.removeEventListener("resize", onResize);
    })();

    return () => {
      disposed = true;
      socket?.close();
    };
  }, [applicationId]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div className="w-full max-w-4xl overflow-hidden rounded-[8px] border border-line/70 bg-[#0a0a0a]">
        <div className="flex items-center justify-between border-b border-white/10 px-3 py-2">
          <span className="text-[12px] text-white/60">Container terminal</span>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
        <div ref={host} className="h-[24rem]" />
      </div>
    </div>
  );
}

export const Route = createFileRoute("/applications/$applicationId")({
  component: ApplicationDetailPage,
});
