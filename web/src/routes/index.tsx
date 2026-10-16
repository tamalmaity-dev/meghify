import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  Activity,
  ArrowRight,
  Database as DatabaseIcon,
  FolderKanban,
  Server,
} from "lucide-react";
import { api, formatBytes } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { Application, Node, Project, Stats } from "@/lib/types";

/**
 * A compact overview rather than a metrics wall: live deployments first, then
 * the collections that actually need attention.
 */
export function DashboardPage() {
  const stats = useQuery({ queryKey: ["stats"], queryFn: () => api.get<Stats>("/stats") });
  const projects = useQuery({
    queryKey: ["projects"],
    queryFn: () => api.get<Project[]>("/projects"),
  });
  const nodes = useQuery({ queryKey: ["nodes"], queryFn: () => api.get<Node[]>("/nodes") });
  const apps = useQuery({
    queryKey: ["applications"],
    queryFn: () => api.get<Application[]>("/applications"),
  });

  const running = (apps.data ?? []).filter((app) => app.status === "running").length;
  const deploying = (apps.data ?? []).filter((app) => app.status === "deploying");

  return (
    <>
      <PageHeader
        title="Dashboard"
        description={
          stats.data
            ? `${stats.data.applications} applications across ${stats.data.projects} projects`
            : "Loading overview"
        }
        actions={
          <Link to="/projects">
            <span className="inline-flex h-8 items-center gap-1.5 rounded-md bg-brand/10 px-3 text-[13px] font-medium text-brand">
              New project
              <ArrowRight className="h-3.5 w-3.5" />
            </span>
          </Link>
        }
      />

      <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile
          icon={<FolderKanban className="h-4 w-4" />}
          label="Projects"
          value={stats.data?.projects ?? "-"}
          hint={`${stats.data?.applications ?? 0} applications`}
        />
        <StatTile
          icon={<Server className="h-4 w-4" />}
          label="Servers"
          value={stats.data?.onlineNodes ?? 0}
          hint={`${stats.data?.nodes ?? 0} registered`}
        />
        <StatTile
          icon={<DatabaseIcon className="h-4 w-4" />}
          label="Databases"
          value={stats.data?.databases ?? "-"}
          hint={`${stats.data?.services ?? 0} compose stacks`}
        />
        <StatTile
          icon={<Activity className="h-4 w-4" />}
          label="Running"
          value={running}
          hint={
            deploying.length > 0
              ? `${deploying.length} deploying now`
              : `${stats.data?.pendingJobs ?? 0} queued jobs`
          }
        />
      </div>

      {deploying.length > 0 && (
        <Card className="mb-4">
          <CardHeader>
            <CardTitle>Live deployments</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table
              head={
                <>
                  <Th>Application</Th>
                  <Th>Status</Th>
                  <Th align="right">Started</Th>
                </>
              }
            >
              {deploying.map((app) => (
                <Tr key={app.id}>
                  <Td>
                    <Link
                      to="/applications/$applicationId"
                      params={{ applicationId: app.id }}
                      className="hover:underline"
                    >
                      {app.name}
                    </Link>
                  </Td>
                  <Td>
                    <StatusBadge status={app.status} />
                  </Td>
                  <Td align="right" className="text-black/45 dark:text-white/40">
                    in progress
                  </Td>
                </Tr>
              ))}
            </Table>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Servers</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Name</Th>
                <Th>Address</Th>
                <Th>Status</Th>
                <Th align="right">Resources</Th>
              </>
            }
          >
            {(nodes.data ?? []).map((node) => (
              <Tr key={node.id}>
                <Td>{node.name}</Td>
                <Td className="font-mono text-[12px] text-black/55 dark:text-white/45">
                  {node.address}
                </Td>
                <Td>
                  <StatusBadge status={node.status} />
                </Td>
                <Td align="right" className="text-black/55 dark:text-white/45">
                  {node.cpus} vCPU · {formatBytes(node.memoryBytes)}
                </Td>
              </Tr>
            ))}
            {(nodes.data ?? []).length === 0 && (
              <Tr>
                <Td align="center" className="text-black/45 dark:text-white/40">
                  No servers registered yet. The panel manages the machine it runs on.
                </Td>
              </Tr>
            )}
          </Table>
        </CardContent>
      </Card>

      {(projects.data?.length ?? 0) > 0 && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle>Projects</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table
              head={
                <>
                  <Th>Name</Th>
                  <Th>Description</Th>
                  <Th align="right">Created</Th>
                </>
              }
            >
              {(projects.data ?? []).map((project) => (
                <Tr key={project.id}>
                  <Td>
                    <Link
                      to="/projects/$projectId"
                      params={{ projectId: project.id }}
                      className="hover:underline"
                    >
                      {project.name}
                    </Link>
                  </Td>
                  <Td className="text-black/55 dark:text-white/45">
                    {project.description || "-"}
                  </Td>
                  <Td align="right" className="text-black/45 dark:text-white/40">
                    {new Date(project.createdAt).toLocaleDateString()}
                  </Td>
                </Tr>
              ))}
            </Table>
          </CardContent>
        </Card>
      )}
    </>
  );
}

function StatTile({
  icon,
  label,
  value,
  hint,
}: {
  icon: React.ReactNode;
  label: string;
  value: React.ReactNode;
  hint: string;
}) {
  return (
    <Card className="min-h-28">
      <CardHeader className="pb-1">
        <div className="flex h-6 w-6 items-center justify-center rounded-md bg-white/[0.06] text-black/60 dark:text-white/55">
          {icon}
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        <div className="text-[20px] font-semibold tabular-nums">{value}</div>
        <div className="text-[11px] text-black/45 dark:text-white/40">{label}</div>
        <div className="mt-1 text-[11px] text-black/40 dark:text-white/35">{hint}</div>
      </CardContent>
    </Card>
  );
}

export const Route = createFileRoute("/")({
  component: DashboardPage,
});
