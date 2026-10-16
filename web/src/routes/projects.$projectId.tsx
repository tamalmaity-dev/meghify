import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Select } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import { BUILD_PACKS, DATABASE_KINDS } from "@/lib/types";
import type { Application, Database, Project } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

export function ProjectDetailPage() {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<"apps" | "databases" | "services">("apps");
  const [createOpen, setCreateOpen] = useState(false);
  const [form, setForm] = useState({
    name: "",
    kind: "postgres",
    gitRepository: "",
    gitBranch: "main",
    buildPack: "dockerfile",
    port: 3000,
    healthCheck: "/",
  });

  const project = useQuery({
    queryKey: ["projects", projectId],
    queryFn: () => api.get<Project>(`/projects/${projectId}`),
  });

  const apps = useQuery({
    queryKey: ["applications"],
    queryFn: () => api.get<Application[]>("/applications"),
  });
  const databases = useQuery({
    queryKey: ["databases"],
    queryFn: () => api.get<Database[]>("/databases"),
  });

  const [envId, setEnvId] = useState("");

  const environments = useQuery({
    queryKey: ["environments", projectId],
    queryFn: () => api.get<{ id: string; name: string }[]>(`/projects/${projectId}/environments`),
  });

  const environmentId = envId || environments.data?.[0]?.id || "";

  const appList = (apps.data ?? []).filter((app) => app.environmentId === environmentId);
  const dbList = (databases.data ?? []).filter((db) => db.environmentId === environmentId);

  const createApplication = useMutation({
    mutationFn: () =>
      api.post<Application>("/applications", { ...form, environmentId: environmentId }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["applications"] });
      setCreateOpen(false);
    },
  });

  const createDatabase = useMutation({
    mutationFn: () => api.post<Database>("/databases", { name: form.name, kind: form.kind, environmentId: environmentId }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["databases"] });
      setCreateOpen(false);
    },
  });

  const isDatabase = tab === "databases";

  return (
    <>
      <PageHeader
        title={project.data?.name ?? "Project"}
        description={project.data?.description || "No description"}
        actions={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" />
            {isDatabase ? "New database" : "New application"}
          </Button>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div className="flex rounded-md bg-white/[0.04] p-0.5">
          {(["apps", "databases", "services"] as const).map((key) => (
            <button
              key={key}
              onClick={() => setTab(key)}
              className={`h-7 rounded px-2.5 text-[12px] capitalize transition-colors ${
                tab === key ? "bg-white/[0.08] font-medium" : "text-black/55 dark:text-white/45"
              }`}
            >
              {key === "apps" ? "Applications" : key}
            </button>
          ))}
        </div>

        {(environments.data?.length ?? 0) > 1 && (
          <Select
            value={environmentId}
            onChange={(event) => setEnvId(event.target.value)}
            className="w-40"
          >
            {(environments.data ?? []).map((env) => (
              <option key={env.id} value={env.id}>
                {env.name}
              </option>
            ))}
          </Select>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>
            {tab === "apps" ? "Applications" : tab === "databases" ? "Databases" : "Compose stacks"}
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Name</Th>
                <Th>Status</Th>
                <Th>Details</Th>
                <Th align="right">Created</Th>
              </>
            }
          >
            {tab === "apps" &&
              appList.map((app) => (
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
                  <Td className="font-mono text-[12px] text-black/55 dark:text-white/45">
                    {app.imageTag || `${app.gitRepository}@${app.gitBranch}`}
                  </Td>
                  <Td align="right" className="text-black/45 dark:text-white/40">
                    {new Date(app.createdAt).toLocaleDateString()}
                  </Td>
                </Tr>
              ))}

            {tab === "databases" &&
              dbList.map((db) => (
                <Tr key={db.id}>
                  <Td>{db.name}</Td>
                  <Td>
                    <StatusBadge status={db.status} />
                  </Td>
                  <Td className="text-black/55 dark:text-white/45">
                    {db.kind} {db.version}
                  </Td>
                  <Td align="right" className="text-black/45 dark:text-white/40">
                    {new Date(db.createdAt).toLocaleDateString()}
                  </Td>
                </Tr>
              ))}

            {tab === "services" && (
              <Tr>
                <Td className="text-center text-black/45 dark:text-white/40" align="center">
                  Compose stacks are configured per application in v1.
                </Td>
              </Tr>
            )}

            {((tab === "apps" && appList.length === 0) ||
              (tab === "databases" && dbList.length === 0)) && (
              <Tr>
                <Td className="text-center text-black/45 dark:text-white/40" align="center">
                  Nothing here yet.
                </Td>
              </Tr>
            )}
          </Table>
        </CardContent>
      </Card>

      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title={isDatabase ? "Provision a database" : "Create an application"}
        description={
          isDatabase
            ? "A generated root password is returned once. Store it with your application's environment variables."
            : "The panel clones the repository on the target server and builds it with the pack you choose."
        }
        footer={
          <>
            <Button variant="ghost" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={!form.name.trim() || !environmentId}
              onClick={() => (isDatabase ? createDatabase.mutate() : createApplication.mutate())}
            >
              Create
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div>
            <Label htmlFor="resource-name">Name</Label>
            <Input
              id="resource-name"
              value={form.name}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
              placeholder={isDatabase ? "orders-db" : "api"}
              autoFocus
            />
          </div>

          {isDatabase ? (
            <div>
              <Label htmlFor="db-kind">Kind</Label>
              <Select
                id="db-kind"
                value={form.kind}
                onChange={(event) => setForm({ ...form, kind: event.target.value })}
              >
                {DATABASE_KINDS.map((kind) => (
                  <option key={kind.value} value={kind.value}>
                    {kind.label}
                  </option>
                ))}
              </Select>
            </div>
          ) : (
            <>
              <div>
                <Label htmlFor="git-repo">Git repository</Label>
                <Input
                  id="git-repo"
                  value={form.gitRepository}
                  onChange={(event) => setForm({ ...form, gitRepository: event.target.value })}
                  placeholder="https://github.com/acme/api.git"
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label htmlFor="git-branch">Branch</Label>
                  <Input
                    id="git-branch"
                    value={form.gitBranch}
                    onChange={(event) => setForm({ ...form, gitBranch: event.target.value })}
                  />
                </div>
                <div>
                  <Label htmlFor="build-pack">Build pack</Label>
                  <Select
                    id="build-pack"
                    value={form.buildPack}
                    onChange={(event) => setForm({ ...form, buildPack: event.target.value })}
                  >
                    {BUILD_PACKS.map((pack) => (
                      <option key={pack.value} value={pack.value}>
                        {pack.label}
                      </option>
                    ))}
                  </Select>
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label htmlFor="app-port">Container port</Label>
                  <Input
                    id="app-port"
                    type="number"
                    value={form.port}
                    onChange={(event) => setForm({ ...form, port: Number(event.target.value) })}
                  />
                </div>
                <div>
                  <Label htmlFor="health-check">Health check path</Label>
                  <Input
                    id="health-check"
                    value={form.healthCheck}
                    onChange={(event) => setForm({ ...form, healthCheck: event.target.value })}
                  />
                </div>
              </div>
            </>
          )}
        </div>
      </Modal>
    </>
  );
}

export const Route = createFileRoute("/projects/$projectId")({
  component: ProjectDetailPage,
});
