import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, RefreshCw, Server, ShieldCheck, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { api, formatBytes, formatDate } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import { XTerminal } from "@/components/terminal";
import { SystemMetrics } from "@/components/system-metrics";
import { DockerCleanupPanel } from "@/components/docker-cleanup";
import { ResourceGuardPanel } from "@/components/resource-guard";
import { cn } from "@/lib/utils";
import type { Node } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

const SECTIONS = [
  { key: "general", label: "General" },
  { key: "terminal", label: "Terminal" },
  { key: "metrics", label: "Metrics" },
  { key: "docker", label: "Docker cleanup" },
  { key: "guard", label: "Resource protection" },
  { key: "danger", label: "Danger zone" },
] as const;

type Section = (typeof SECTIONS)[number]["key"];

/** The singleton row that describes the machine the panel runs on. */
const LOCAL_NODE_ID = "local";

/**
 * One server, laid out like Coolify's server page: an overview of the facts
 * the engine reported, the editable connection details, an interactive
 * terminal and the machine's metrics.
 */
export function NodeDetailPage() {
  const { nodeId } = useParams({ from: "/nodes/$nodeId" });
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [section, setSection] = useState<Section>("general");

  const node = useQuery({
    queryKey: ["nodes", nodeId],
    queryFn: () => api.get<Node>(`/nodes/${nodeId}`),
  });

  const [form, setForm] = useState({
    name: "",
    description: "",
    address: "",
    sshUser: "root",
    sshPort: 22,
    hostKeyFingerprint: "",
  });
  useEffect(() => {
    if (!node.data) return;
    setForm({
      name: node.data.name,
      description: node.data.description ?? "",
      address: node.data.address,
      sshUser: node.data.sshUser || "root",
      sshPort: node.data.sshPort || 22,
      hostKeyFingerprint: node.data.hostKeyFingerprint ?? "",
    });
  }, [node.data]);

  // Docker cleanup and the panel-wide resource guard are configured from the
  // local server page. Hide those panes on remote servers rather than showing
  // controls that would silently affect another machine.
  useEffect(() => {
    if (
      (section === "docker" || section === "guard") &&
      node.data &&
      node.data.id !== LOCAL_NODE_ID
    ) {
      setSection("general");
    }
  }, [node.data, section]);

  const save = useMutation({
    mutationFn: () => api.patch<Node>(`/nodes/${nodeId}`, form),
    onSuccess: async (updated) => {
      toast.success("Connection settings saved");
      queryClient.setQueryData(["nodes", nodeId], updated);
      await queryClient.invalidateQueries({ queryKey: ["nodes"] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const validate = useMutation({
    mutationFn: () => api.post<Node>(`/nodes/${nodeId}/validate`),
    onSuccess: async (updated) => {
      toast.success("Server validated — it is reachable");
      queryClient.setQueryData(["nodes", nodeId], updated);
      await queryClient.invalidateQueries({ queryKey: ["nodes"] });
    },
    onError: (error) => {
      toast.error(errorText(error));
      queryClient.invalidateQueries({ queryKey: ["nodes", nodeId] });
    },
  });

  const remove = useMutation({
    mutationFn: () => api.delete(`/nodes/${nodeId}`),
    onSuccess: async () => {
      toast.success("Server removed");
      await queryClient.invalidateQueries({ queryKey: ["nodes"] });
      navigate({ to: "/nodes" });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  if (!node.data) {
    return (
      <>
        <Link
          to="/nodes"
          className="mb-2 inline-flex items-center gap-1 text-[11px] text-black/45 transition-colors hover:text-current dark:text-white/40"
        >
          <ChevronLeft className="h-3.5 w-3.5" />
          All servers
        </Link>
        <Card>
          <CardContent className="py-8 text-center text-[12px] text-black/45 dark:text-white/40">
            {node.isError ? errorText(node.error) : "Loading server…"}
          </CardContent>
        </Card>
      </>
    );
  }

  const server = node.data;
  const isLocal = server.id === LOCAL_NODE_ID;
  // Cleanup runs against the panel's own Docker engine, so it is not offered
  // on remote nodes until their engines are managed by the cleanup service.
  // The resource guard is panel-wide but stays beside it on the local page.
  const sections = isLocal
    ? SECTIONS
    : SECTIONS.filter((item) => item.key !== "docker" && item.key !== "guard");
  const ready = server.status === "online";
  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const terminalUrl = `${protocol}://${window.location.host}/api/nodes/${nodeId}/terminal`;

  return (
    <>
      <Link
        to="/nodes"
        className="mb-2 inline-flex items-center gap-1 text-[11px] text-black/45 transition-colors hover:text-current dark:text-white/40"
      >
        <ChevronLeft className="h-3.5 w-3.5" />
        All servers
      </Link>

      <PageHeader
        title={server.name}
        description={
          isLocal
            ? "The machine Meghify runs on — protected from removal."
            : `${server.sshUser}@${server.address}:${server.sshPort}`
        }
        actions={<StatusBadge status={server.status} />}
      />

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
          {section === "general" && (
            <div className="space-y-3">
              <Card>
                <CardHeader>
                  <CardTitle>Server overview</CardTitle>
                  <div className="flex items-center gap-2">
                    <Button
                      variant="ghost"
                      size="icon"
                      title="Refresh the facts reported by the engine"
                      onClick={() => validate.mutate()}
                      disabled={validate.isPending}
                    >
                      <RefreshCw
                        className={validate.isPending ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"}
                      />
                    </Button>
                    {ready ? (
                      <span className="inline-flex items-center gap-1.5 rounded-full bg-ok/10 px-2 py-0.5 text-[10.5px] font-medium text-ok">
                        <span className="h-1.5 w-1.5 rounded-full bg-ok" />
                        Ready
                      </span>
                    ) : (
                      <StatusBadge status={server.status} />
                    )}
                  </div>
                </CardHeader>
                <CardContent>
                  <div className="flex items-start gap-3 border-b border-line/40 pb-3">
                    <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-black/[0.06] dark:bg-white/[0.06]">
                      <Server className="h-4 w-4" />
                    </span>
                    <div className="min-w-0">
                      <div className="text-[13px] font-medium">{server.name}</div>
                      <p className="mt-0.5 text-[11.5px] leading-relaxed text-black/50 dark:text-white/45">
                        {server.description ||
                          (ready
                            ? "The server is reachable, validated, and ready to host resources."
                            : "The server has not been reached yet — provision it or validate the connection.")}
                      </p>
                    </div>
                  </div>

                  <div className="mt-3 grid gap-x-6 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
                    <Fact label="Operating system" value={server.os} />
                    <Fact label="Architecture" value={server.arch} />
                    <Fact label="Kernel" value={server.kernel} />
                    <Fact label="CPU cores" value={server.cpus ? String(server.cpus) : ""} />
                    <Fact
                      label="Memory"
                      value={server.memoryBytes ? formatBytes(server.memoryBytes) : ""}
                    />
                    <Fact label="Docker version" value={server.dockerVersion} />
                    <Fact label="Up since" value={server.bootTime ? formatDate(server.bootTime) : ""} />
                    <Fact
                      label="Last seen"
                      value={server.lastSeenAt ? formatDate(server.lastSeenAt) : ""}
                    />
                  </div>

                  {server.errorMessage && (
                    <p className="mt-3 rounded-md bg-danger/10 px-3 py-2 text-[11.5px] text-danger">
                      {server.errorMessage}
                    </p>
                  )}
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>Connection</CardTitle>
                  <Button
                    variant="neutral"
                    size="sm"
                    onClick={() => validate.mutate()}
                    disabled={validate.isPending}
                  >
                    <RefreshCw
                      className={validate.isPending ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"}
                    />
                    Validate Connection
                  </Button>
                </CardHeader>
                <CardContent className="space-y-3">
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div>
                      <Label htmlFor="node-name">Name</Label>
                      <Input
                        id="node-name"
                        value={form.name}
                        onChange={(event) => setForm({ ...form, name: event.target.value })}
                      />
                    </div>
                    <div>
                      <Label htmlFor="node-description">Description</Label>
                      <Input
                        id="node-description"
                        value={form.description}
                        onChange={(event) => setForm({ ...form, description: event.target.value })}
                        placeholder={isLocal ? "The machine Meghify runs on" : "what runs here"}
                      />
                    </div>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_120px_120px]">
                    <div>
                      <Label htmlFor="node-address">IP address or domain</Label>
                      <Input
                        id="node-address"
                        value={form.address}
                        disabled={isLocal}
                        onChange={(event) => setForm({ ...form, address: event.target.value })}
                      />
                    </div>
                    <div>
                      <Label htmlFor="node-user">SSH user</Label>
                      <Input
                        id="node-user"
                        value={form.sshUser}
                        disabled={isLocal}
                        onChange={(event) => setForm({ ...form, sshUser: event.target.value })}
                      />
                    </div>
                    <div>
                      <Label htmlFor="node-port">SSH port</Label>
                      <Input
                        id="node-port"
                        type="number"
                        value={form.sshPort}
                        disabled={isLocal}
                        onChange={(event) => setForm({ ...form, sshPort: Number(event.target.value) })}
                      />
                    </div>
                  </div>
                  {!isLocal && (
                    <div>
                      <Label htmlFor="node-host-key">SSH host key fingerprint</Label>
                      <Input
                        id="node-host-key"
                        value={form.hostKeyFingerprint}
                        onChange={(event) =>
                          setForm({ ...form, hostKeyFingerprint: event.target.value })
                        }
                        placeholder="SHA256:... (pinned on first connection)"
                        className="font-mono"
                      />
                      <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
                        Pinned automatically the first time the panel connects. Paste the
                        server&apos;s key first if you want to pin before connecting.
                      </p>
                    </div>
                  )}
                  {isLocal && (
                    <p className="flex items-center gap-1.5 text-[11px] text-black/45 dark:text-white/40">
                      <ShieldCheck className="h-3.5 w-3.5" />
                      This is the server where Meghify is running; only its name and description can
                      be changed, and it cannot be removed.
                    </p>
                  )}
                  <div className="flex justify-end border-t border-line/40 pt-3">
                    <Button
                      variant="primary"
                      size="sm"
                      onClick={() => save.mutate()}
                      disabled={save.isPending || form.name.trim() === ""}
                    >
                      Save changes
                    </Button>
                  </div>
                </CardContent>
              </Card>
            </div>
          )}

          {section === "terminal" && (
            <Card>
              <CardHeader>
                <CardTitle>Terminal</CardTitle>
                <span className="text-[11px] text-black/40 dark:text-white/35">
                  {isLocal
                    ? "shell in a helper container with the docker CLI (engine socket mounted)"
                    : `ssh shell as ${server.sshUser}@${server.address}`}
                </span>
              </CardHeader>
              <CardContent className="p-0">
                <div className="h-[28rem] overflow-hidden rounded-b-[8px] bg-[#0a0a0a]">
                  <XTerminal
                    url={terminalUrl}
                    className="h-full"
                    title={server.name}
                    subtitle={isLocal ? "localhost" : `${server.sshUser}@${server.address}`}
                    banner={
                      isLocal
                        ? "\r\nConnected to a helper container on the server (docker CLI included).\r\n"
                        : `\r\nConnected to ${server.sshUser}@${server.address}.\r\n`
                    }
                  />
                </div>
              </CardContent>
            </Card>
          )}

          {section === "metrics" && <SystemMetrics nodeId={nodeId} />}

          {section === "docker" && isLocal && <DockerCleanupPanel />}

          {section === "guard" && isLocal && <ResourceGuardPanel />}

          {section === "danger" && (
            <Card>
              <CardHeader>
                <CardTitle>Danger zone</CardTitle>
              </CardHeader>
              <CardContent>
                {isLocal ? (
                  <p className="flex items-start gap-2 text-[12px] leading-relaxed text-black/55 dark:text-white/45">
                    <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-ok" />
                    This is the server where Meghify is running on. It is the panel&apos;s default
                    server and cannot be removed — the dashboard, metrics and local deployments all
                    depend on it.
                  </p>
                ) : (
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <div className="text-[12.5px] font-medium">Remove this server</div>
                      <p className="mt-0.5 text-[11px] leading-relaxed text-black/50 dark:text-white/45">
                        Containers keep running on the machine; the panel simply stops managing it.
                        Applications and databases pointing here lose their engine connection.
                      </p>
                    </div>
                    <Button
                      variant="danger"
                      disabled={remove.isPending}
                      onClick={() => {
                        if (
                          window.confirm(
                            `Remove "${server.name}" from Meghify? Containers keep running, but the panel stops managing this server.`,
                          )
                        ) {
                          remove.mutate();
                        }
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                      Remove server
                    </Button>
                  </div>
                )}
              </CardContent>
            </Card>
          )}
        </div>
      </div>
    </>
  );
}

/** One fact row of the Server overview grid. */
function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[10.5px] tracking-wider text-black/40 uppercase dark:text-white/35">
        {label}
      </div>
      <div className="mt-0.5 truncate text-[12.5px] font-medium">
        {value || <span className="text-black/30 dark:text-white/25">unknown</span>}
      </div>
    </div>
  );
}

export const Route = createFileRoute("/nodes/$nodeId")({
  component: NodeDetailPage,
});
