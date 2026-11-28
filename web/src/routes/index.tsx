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
import { useSession } from "@/lib/session";
import { PageHeader } from "@/components/app-shell";
import { SystemMetrics } from "@/components/system-metrics";
import { DockerDiskUsage } from "@/components/docker-disk";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { Application, Node, Project, Stats } from "@/lib/types";

/**
 * A compact overview rather than a metrics wall: live deployments first, then
 * the collections that actually need attention.
 */
export function DashboardPage() {
  const { user } = useSession();
  const isAdmin = user?.role === "admin";
  const stats = useQuery({ queryKey: ["stats"], queryFn: () => api.get<Stats>("/stats") });
  const projects = useQuery({
    queryKey: ["projects"],
    queryFn: () => api.get<Project[]>("/projects"),
  });
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.get<Node[]>("/nodes"),
    enabled: isAdmin,
  });
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
            <Button variant="primary">
              New project
              <ArrowRight className="h-3.5 w-3.5" />
            </Button>
          </Link>
        }
      />

      {isAdmin && (
        <>
          <SystemMetrics />
          <DockerDiskUsage />
        </>
      )}

      <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile
          icon={<FolderKanban className="h-4 w-4" />}
          label="Projects"
          value={stats.data?.projects ?? "-"}
          hint={`${stats.data?.applications ?? 0} applications`}
          to="/projects"
        />
        <StatTile
          icon={<Server className="h-4 w-4" />}
          label="Servers"
          value={stats.data?.onlineNodes ?? 0}
          hint={`${stats.data?.nodes ?? 0} registered`}
          to={isAdmin ? "/nodes" : undefined}
        />
        <StatTile
          icon={<DatabaseIcon className="h-4 w-4" />}
          label="Databases"
          value={stats.data?.databases ?? "-"}
          hint={`${stats.data?.services ?? 0} compose stacks`}
          to="/databases"
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
          to={isAdmin ? "/jobs" : undefined}
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
                      className="font-medium text-ink hover:text-brand hover:underline"
                    >
                      {app.name}
                    </Link>
                  </Td>
                  <Td>
                    <StatusBadge status={app.status} />
                  </Td>
                  <Td align="right" className="text-muted">
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
          {isAdmin && (
            <Link to="/nodes" className="text-[11.5px] font-medium text-brand hover:underline">
              Manage servers
            </Link>
          )}
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
                <Td className="font-medium text-ink">{node.name}</Td>
                <Td className="font-mono text-[12px] text-muted">{node.address}</Td>
                <Td>
                  <StatusBadge status={node.status} />
                </Td>
                <Td align="right" className="text-muted">
                  {node.cpus} vCPU · {formatBytes(node.memoryBytes)}
                </Td>
              </Tr>
            ))}
            {(nodes.data ?? []).length === 0 && (
              <tr>
                <td colSpan={4} className="px-4 py-6 text-center text-[12px] text-muted">
                  No servers registered yet. The panel manages the machine it runs on.
                </td>
              </tr>
            )}
          </Table>
        </CardContent>
      </Card>

      {(projects.data?.length ?? 0) > 0 && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle>Projects</CardTitle>
            <Link to="/projects" className="text-[11.5px] font-medium text-brand hover:underline">
              View all
            </Link>
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
                      className="font-medium text-ink hover:text-brand hover:underline"
                    >
                      {project.name}
                    </Link>
                  </Td>
                  <Td className="text-muted">{project.description || "-"}</Td>
                  <Td align="right" className="text-faint">
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
  to,
}: {
  icon: React.ReactNode;
  label: string;
  value: React.ReactNode;
  hint: string;
  to?: string;
}) {
  const inner = (
    <Card
      className={
        to
          ? "min-h-28 transition-[border-color,transform] hover:-translate-y-px hover:border-brand/45"
          : "min-h-28"
      }
    >
      <CardContent className="pt-4">
        <div className="flex items-center justify-between">
          <div className="flex h-7 w-7 items-center justify-center rounded-lg border border-line/60 bg-recessed text-brand">
            {icon}
          </div>
          <span className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            {label}
          </span>
        </div>
        <div className="mt-3 text-[22px] font-semibold tabular-nums text-ink">{value}</div>
        <div className="mt-1 text-[11px] text-faint">{hint}</div>
      </CardContent>
    </Card>
  );

  return to ? <Link to={to}>{inner}</Link> : inner;
}

export const Route = createFileRoute("/")({
  component: DashboardPage,
});
