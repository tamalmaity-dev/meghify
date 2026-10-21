import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import {
  ArrowUpRight,
  Box,
  ChevronLeft,
  Container as ContainerIcon,
  RefreshCw,
  Search,
  Server,
  TerminalSquare,
} from "lucide-react";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { XTerminal } from "@/components/terminal";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { ContainerInfo, Node } from "@/lib/types";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

interface NodeTargets {
  node: Node;
  containers: ContainerInfo[];
  error?: string;
}

type SelectedTarget =
  | { kind: "server"; node: Node }
  | { kind: "container"; node: Node; container: ContainerInfo };

function terminalWebSocketUrl(path: string): string {
  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  return `${protocol}://${window.location.host}${path}`;
}

export function TerminalPage() {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<SelectedTarget | null>(null);

  const targets = useQuery({
    queryKey: ["terminal", "targets"],
    queryFn: async (): Promise<NodeTargets[]> => {
      const nodes = await api.get<Node[]>("/nodes");
      return Promise.all(
        nodes.map(async (node) => {
          try {
            const containers = await api.get<ContainerInfo[]>(
              `/nodes/${node.id}/containers?all=1`,
            );
            return { node, containers };
          } catch (error) {
            return { node, containers: [], error: errorText(error) };
          }
        }),
      );
    },
    refetchOnWindowFocus: true,
  });

  const filtered = useMemo(() => {
    const term = query.trim().toLowerCase();
    const servers = (targets.data ?? []).filter(
      ({ node }) =>
        !term ||
        node.name.toLowerCase().includes(term) ||
        node.address.toLowerCase().includes(term),
    );
    const containers = (targets.data ?? []).flatMap(({ node, containers }) =>
      containers
        .filter((container) => container.state === "running")
        .filter(
          (container) =>
            !term ||
            container.name.toLowerCase().includes(term) ||
            container.image.toLowerCase().includes(term) ||
            node.name.toLowerCase().includes(term),
        )
        .map((container) => ({ node, container })),
    );
    containers.sort((a, b) => a.container.name.localeCompare(b.container.name));
    return { servers, containers };
  }, [targets.data, query]);

  if (selected) {
    const path =
      selected.kind === "server"
        ? `/api/nodes/${selected.node.id}/terminal`
        : `/api/nodes/${selected.node.id}/containers/${selected.container.id}/terminal`;
    const title = selected.kind === "server" ? selected.node.name : selected.container.name;
    const subtitle =
      selected.kind === "server"
        ? selected.node.id === "local"
          ? "local server shell"
          : `${selected.node.sshUser}@${selected.node.address}`
        : `${selected.node.name} · ${selected.container.image}`;
    return (
      <>
        <button
          type="button"
          onClick={() => setSelected(null)}
          className="mb-2 inline-flex items-center gap-1 text-[11px] text-black/45 transition-colors hover:text-current dark:text-white/40"
        >
          <ChevronLeft className="h-3.5 w-3.5" />
          All terminal targets
        </button>
        <PageHeader
          title={selected.kind === "server" ? "Server terminal" : "Container terminal"}
          description={subtitle}
          actions={
            <span className="inline-flex items-center gap-1.5 rounded-full bg-white/[0.04] px-2 py-1 text-[11px] text-black/55 dark:text-white/45">
              <TerminalSquare className="h-3.5 w-3.5" />
              {selected.kind === "server" ? "ssh / helper shell" : "interactive exec"}
            </span>
          }
        />
        <Card className="overflow-hidden">
          <CardContent className="p-0">
            <div className="h-[calc(100vh-13rem)] min-h-[26rem]">
              <XTerminal
                key={`${selected.kind}:${selected.node.id}:${
                  selected.kind === "container" ? selected.container.id : "server"
                }`}
                url={terminalWebSocketUrl(path)}
                className="h-full"
                title={title}
                subtitle={subtitle}
                banner={
                  selected.kind === "server"
                    ? `\r\nOpening a shell on ${selected.node.name}…\r\n`
                    : `\r\nOpening a shell in ${selected.container.name}…\r\n`
                }
              />
            </div>
          </CardContent>
        </Card>
      </>
    );
  }

  return (
    <>
      <PageHeader
        title="Terminal"
        description="Open a shell on a server or inside any running container."
        actions={
          <Button
            variant="ghost"
            size="icon"
            title="Refresh targets"
            onClick={() => targets.refetch()}
            disabled={targets.isFetching}
          >
            <RefreshCw
              className={targets.isFetching ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"}
            />
          </Button>
        }
      />

      <div className="mb-4 flex items-center gap-2">
        <div className="relative max-w-sm flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-black/35 dark:text-white/30" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Filter servers and containers…"
            className="pl-8"
          />
        </div>
        <span className="text-[11px] text-black/40 dark:text-white/35">
          {filtered.servers.length} server{filtered.servers.length === 1 ? "" : "s"} ·{" "}
          {filtered.containers.length} running container
          {filtered.containers.length === 1 ? "" : "s"}
        </span>
      </div>

      {targets.isError && (
        <Card className="mb-3">
          <CardContent className="py-4 text-[12px] text-danger">
            {errorText(targets.error)}
          </CardContent>
        </Card>
      )}

      <div className="space-y-3">
        <Card>
          <CardHeader>
            <CardTitle>Servers</CardTitle>
            <span className="text-[10.5px] text-black/40 dark:text-white/35">
              SSH shell or the local Docker helper
            </span>
          </CardHeader>
          <CardContent className="p-0">
            {targets.isLoading ? (
              <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                Discovering servers…
              </p>
            ) : !filtered.servers.length ? (
              <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                No servers match this filter.
              </p>
            ) : (
              <ul className="divide-y divide-line/40">
                {filtered.servers.map(({ node, containers, error }) => (
                  <li key={node.id}>
                    <button
                      type="button"
                      onClick={() => setSelected({ kind: "server", node })}
                      className="group flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-black/[0.025] dark:hover:bg-white/[0.03]"
                    >
                      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-black/[0.05] text-black/55 dark:bg-white/[0.06] dark:text-white/45">
                        <Server className="h-4 w-4" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-2">
                          <span className="truncate text-[13px] font-medium">{node.name}</span>
                          {node.id === "local" && (
                            <span className="rounded-full bg-brand/10 px-1.5 py-0.5 text-[9.5px] font-medium uppercase tracking-wide text-brand">
                              local
                            </span>
                          )}
                        </span>
                        <span className="mt-0.5 block truncate text-[11px] text-black/45 dark:text-white/35">
                          {node.id === "local"
                            ? "This machine — shell with Docker CLI"
                            : `${node.sshUser}@${node.address}:${node.sshPort}`}
                          {containers.length > 0 &&
                            ` · ${containers.filter((item) => item.state === "running").length} running`}
                          {error && " · container list unavailable"}
                        </span>
                      </span>
                      <StatusBadge status={node.status} />
                      <span className="hidden items-center gap-1 text-[11px] text-black/40 transition-colors group-hover:text-current dark:text-white/35 sm:flex">
                        Open
                        <ArrowUpRight className="h-3.5 w-3.5" />
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Containers</CardTitle>
            <span className="text-[10.5px] text-black/40 dark:text-white/35">
              Running containers across every reachable server
            </span>
          </CardHeader>
          <CardContent className="p-0">
            {targets.isLoading ? (
              <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                Discovering containers…
              </p>
            ) : !filtered.containers.length ? (
              <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                No running containers match this filter.
              </p>
            ) : (
              <ul className="divide-y divide-line/40">
                {filtered.containers.map(({ node, container }) => (
                  <li key={`${node.id}:${container.id}`}>
                    <button
                      type="button"
                      onClick={() => setSelected({ kind: "container", node, container })}
                      className="group flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-black/[0.025] dark:hover:bg-white/[0.03]"
                    >
                      <span className="relative flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-black/[0.05] text-black/55 dark:bg-white/[0.06] dark:text-white/45">
                        <ContainerIcon className="h-4 w-4" />
                        <span className="absolute -right-0.5 -bottom-0.5 h-2 w-2 rounded-full border-2 border-base bg-ok" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-[13px] font-medium">
                          {container.name}
                        </span>
                        <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-black/45 dark:text-white/35">
                          <Box className="h-3 w-3 shrink-0" />
                          <span className="truncate font-mono">{container.image}</span>
                        </span>
                      </span>
                      <span className="hidden shrink-0 rounded-full bg-white/[0.04] px-2 py-0.5 text-[10px] text-black/45 dark:text-white/35 sm:inline">
                        {node.name}
                      </span>
                      <span
                        className={cn(
                          "hidden items-center gap-1 text-[11px] text-black/40 transition-colors group-hover:text-current dark:text-white/35 sm:flex",
                        )}
                      >
                        Open
                        <ArrowUpRight className="h-3.5 w-3.5" />
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}

export const Route = createFileRoute("/terminal")({
  component: TerminalPage,
});
