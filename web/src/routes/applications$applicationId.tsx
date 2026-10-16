import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { Play, RotateCcw, Square, Terminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api, streamEvents } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { Application, Deployment, Domain, EnvVar, Volume } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

export function ApplicationDetailPage() {
  const { applicationId } = useParams({ from: "/applications/$applicationId" });
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<"overview" | "logs" | "env" | "deployments" | "volumes">("overview");
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

  return (
    <>
      <PageHeader
        title={app.data?.name ?? "Application"}
        description={app.data?.gitRepository || "Image deployment"}
        actions={
          <>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setTerminalOpen(true)}
              disabled={app.data?.status !== "running"}
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
        {app.data && <StatusBadge status={app.data.status} />}
        <span className="font-mono text-[12px] text-black/55 dark:text-white/45">
          {app.data?.imageTag || "no image yet"}
        </span>
        {app.data?.fqdn && (
          <a
            href={`https://${app.data.fqdn}`}
            target="_blank"
            rel="noreferrer"
            className="text-[12px] text-brand hover:underline"
          >
            {app.data.fqdn}
          </a>
        )}
      </div>

      <div className="mb-4 flex gap-2 border-b border-line/40">
        {(["overview", "logs", "deployments", "env", "volumes"] as const).map((key) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={`-mb-px h-8 border-b-2 px-2.5 text-[12px] capitalize transition-colors ${
              tab === key
                ? "border-brand text-current"
                : "border-transparent text-black/55 hover:text-current dark:text-white/45"
            }`}
          >
            {key === "env" ? "Environment" : key}
          </button>
        ))}
      </div>

      {tab === "overview" && (
        <div className="grid gap-3 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Configuration</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-[12px]">
              <Row label="Build pack" value={app.data?.buildPack} />
              <Row label="Branch" value={app.data?.gitBranch} />
              <Row label="Container port" value={String(app.data?.port ?? "-")} />
              <Row label="Health check" value={app.data?.healthCheck || "-"} />
              <Row label="Base directory" value={app.data?.baseDirectory || "/"} />
              <Row label="Domains" value={(domains.data ?? []).map((d) => d.host).join(", ") || "none"} />
            </CardContent>
          </Card>

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
        </div>
      )}

      {tab === "logs" && <DeploymentLogs deploymentId={latest?.id} />}

      {tab === "deployments" && (
        <Card>
          <CardHeader>
            <CardTitle>History</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
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
                    {deployment.finishedAt ? new Date(deployment.finishedAt).toLocaleString() : "-"}
                  </Td>
                </Tr>
              ))}
            </Table>
          </CardContent>
        </Card>
      )}

      {tab === "env" && <EnvPanel applicationId={applicationId} vars={envVars.data ?? []} />}

      {tab === "volumes" && (
        <Card>
          <CardHeader>
            <CardTitle>Volumes</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
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
          </CardContent>
        </Card>
      )}

      {terminalOpen && (
        <TerminalModal
          applicationId={applicationId}
          onClose={() => setTerminalOpen(false)}
        />
      )}
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
            setLines((current) => [...current, event.line]);
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

export const Route = createFileRoute("/applications$applicationId")({
  component: ApplicationDetailPage,
});
