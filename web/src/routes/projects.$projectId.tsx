import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronLeft,
  ChevronRight,
  Database as DatabaseIcon,
  FileCode2,
  GitBranch,
  Layers,
  Package,
  Plus,
  Search,
} from "lucide-react";
import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { Link, useParams } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Select, Textarea } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import type { Application, Database, Project } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

type SourceKind = "git" | "dockerfile" | "compose" | "image";
type Category = "all" | "applications" | "databases";

interface SourceCard {
  kind: SourceKind;
  title: string;
  tag: string;
  description: string;
  icon: ReactNode;
  iconClass: string;
}

/**
 * The catalogue behind the "Choose a resource" screen. Each card maps onto one
 * build pack the deploy pipeline already understands.
 */
const APP_SOURCES: SourceCard[] = [
  {
    kind: "git",
    title: "Git Repository",
    tag: "Git source",
    description:
      "Deploy straight from a public or private repository. Nixpacks or Railpack detect the stack automatically.",
    icon: <GitBranch className="h-4 w-4" />,
    iconClass: "bg-brand/15 text-brand",
  },
  {
    kind: "dockerfile",
    title: "Dockerfile",
    tag: "Docker source",
    description:
      "Build an image from a Dockerfile kept in a repository, with its own path inside the tree.",
    icon: <FileCode2 className="h-4 w-4" />,
    iconClass: "bg-sky-500/15 text-sky-600 dark:text-sky-300",
  },
  {
    kind: "compose",
    title: "Docker Compose",
    tag: "Docker source",
    description:
      "Run a multi-container stack from a Compose definition, without keeping it in a repository.",
    icon: <Layers className="h-4 w-4" />,
    iconClass: "bg-amber-500/15 text-amber-600 dark:text-amber-300",
  },
  {
    kind: "image",
    title: "Docker Image",
    tag: "Docker source",
    description:
      "Deploy a prebuilt image from any registry — no repository, no build step.",
    icon: <Package className="h-4 w-4" />,
    iconClass: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-300",
  },
];

interface DatabaseCard {
  kind: string;
  title: string;
  version: string;
  description: string;
  iconClass: string;
}

const DATABASE_CARDS: DatabaseCard[] = [
  {
    kind: "postgres",
    title: "PostgreSQL",
    version: "17",
    description: "Advanced relational database with strong SQL standards compliance.",
    iconClass: "bg-sky-500/15 text-sky-600 dark:text-sky-300",
  },
  {
    kind: "mysql",
    title: "MySQL",
    version: "8.4",
    description: "The most popular open-source relational database.",
    iconClass: "bg-amber-500/15 text-amber-600 dark:text-amber-300",
  },
  {
    kind: "mariadb",
    title: "MariaDB",
    version: "11.8",
    description: "Community relational database, a drop-in replacement for MySQL.",
    iconClass: "bg-violet-500/15 text-violet-600 dark:text-violet-300",
  },
  {
    kind: "redis",
    title: "Redis",
    version: "7",
    description: "In-memory key-value store for caching, queues and sessions.",
    iconClass: "bg-danger/15 text-danger",
  },
  {
    kind: "mongodb",
    title: "MongoDB",
    version: "8",
    description: "Document database for flexible, JSON-like records at any scale.",
    iconClass: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-300",
  },
];

const PACK_CHOICES = [
  { value: "nixpacks", label: "Nixpacks (auto detect)" },
  { value: "railpack", label: "Railpack (auto detect)" },
  { value: "dockerfile", label: "Dockerfile in repository" },
];

const COMPOSE_PLACEHOLDER = `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"`;

interface DatabaseResult {
  database: Database;
  password: string;
  port: number;
  user: string;
}

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

const initials = (name: string) => name.trim().slice(0, 2).toUpperCase() || "AP";

export function ProjectDetailPage() {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  const queryClient = useQueryClient();

  const [view, setView] = useState<"resources" | "choose">("resources");
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState<Category>("all");
  const [envId, setEnvId] = useState("");

  const [appSource, setAppSource] = useState<SourceKind | null>(null);
  const [appForm, setAppForm] = useState({
    name: "",
    repository: "",
    branch: "main",
    buildPack: "nixpacks",
    dockerfilePath: "Dockerfile",
    compose: "",
    port: 3000,
    healthCheck: "/",
  });

  const [dbForm, setDbForm] = useState<{ name: string; kind: string; version: string } | null>(null);
  const [credentials, setCredentials] = useState<{ user: string; password: string; port: number } | null>(null);

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
  const environments = useQuery({
    queryKey: ["environments", projectId],
    queryFn: () => api.get<{ id: string; name: string }[]>(`/projects/${projectId}/environments`),
  });

  const environmentId = envId || environments.data?.[0]?.id || "";
  const appList = (apps.data ?? []).filter((app) => app.environmentId === environmentId);
  const dbList = (databases.data ?? []).filter((db) => db.environmentId === environmentId);

  const needsDockerfilePath =
    appSource === "dockerfile" || (appSource === "git" && appForm.buildPack === "dockerfile");
  const appValid =
    appSource !== null &&
    appForm.name.trim() !== "" &&
    (appSource === "compose" ? appForm.compose.trim() !== "" : appForm.repository.trim() !== "") &&
    (!needsDockerfilePath || appForm.dockerfilePath.trim() !== "") &&
    environmentId !== "";

  const openApplication = (kind: SourceKind) => {
    setAppForm({
      name: "",
      repository: "",
      branch: "main",
      buildPack: kind === "dockerfile" ? "dockerfile" : "nixpacks",
      dockerfilePath: "Dockerfile",
      compose: "",
      port: 3000,
      healthCheck: "/",
    });
    setAppSource(kind);
  };

  const createApplication = useMutation({
    // The environment rides on the query string: POST /applications?environmentId=…
    mutationFn: (input: { environmentId: string; body: Record<string, unknown> }) =>
      api.post<Application>(
        `/applications?environmentId=${encodeURIComponent(input.environmentId)}`,
        input.body,
      ),
    onSuccess: async (created) => {
      await queryClient.invalidateQueries({ queryKey: ["applications"] });
      setAppSource(null);
      setView("resources");
      toast.success(`${created.name} created — open it to deploy`);
    },
  });

  const createDatabase = useMutation({
    mutationFn: (payload: Record<string, unknown>) => api.post<DatabaseResult>("/databases", payload),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: ["databases"] });
      setCredentials({ user: result.user, password: result.password, port: result.port });
    },
  });

  const submitApplication = () => {
    const body = {
      name: appForm.name.trim(),
      port: appForm.port,
      healthCheck: appForm.healthCheck,
    };
    const send = (fields: Record<string, unknown>) =>
      createApplication.mutate({ environmentId, body: { ...body, ...fields } });

    switch (appSource) {
      case "git":
        send({
          gitRepository: appForm.repository.trim(),
          gitBranch: appForm.branch,
          buildPack: appForm.buildPack,
          dockerfile: appForm.buildPack === "dockerfile" ? appForm.dockerfilePath : "Dockerfile",
        });
        break;
      case "dockerfile":
        send({
          gitRepository: appForm.repository.trim(),
          gitBranch: appForm.branch,
          buildPack: "dockerfile",
          dockerfile: appForm.dockerfilePath,
        });
        break;
      case "compose":
        // The pipeline stores a standalone compose definition in the dockerfile field.
        send({ buildPack: "compose", dockerfile: appForm.compose });
        break;
      case "image":
        // For image deployments the repository field carries the image reference.
        send({ buildPack: "image", gitRepository: appForm.repository.trim() });
        break;
      default:
        break;
    }
  };

  const query = search.trim().toLowerCase();
  const matches = (title: string, description: string) =>
    query === "" ||
    title.toLowerCase().includes(query) ||
    description.toLowerCase().includes(query);
  const filteredSources = useMemo(
    () => APP_SOURCES.filter((source) => matches(source.title, source.description)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [query],
  );
  const filteredDatabases = useMemo(
    () => DATABASE_CARDS.filter((card) => matches(card.title, card.description)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [query],
  );

  const sourceMeta = APP_SOURCES.find((source) => source.kind === appSource);

  return (
    <>
      {view === "resources" ? (
        <>
          <Link
            to="/projects"
            className="mb-2 inline-flex items-center gap-1 text-[11px] text-black/45 transition-colors hover:text-current dark:text-white/40"
          >
            <ChevronLeft className="h-3.5 w-3.5" />
            All projects
          </Link>

          <PageHeader
            title={project.data?.name ?? "Project"}
            description={project.data?.description || "No description"}
            actions={
              <Button variant="primary" onClick={() => setView("choose")}>
                <Plus className="h-4 w-4" />
                New resource
              </Button>
            }
          />

          {(environments.data?.length ?? 0) > 1 && (
            <div className="mb-3">
              <Select
                value={environmentId}
                onChange={(event) => setEnvId(event.target.value)}
                className="w-44"
              >
                {(environments.data ?? []).map((env) => (
                  <option key={env.id} value={env.id}>
                    {env.name}
                  </option>
                ))}
              </Select>
            </div>
          )}

          <Card className="mb-4">
            <CardHeader>
              <CardTitle>Applications</CardTitle>
              <span className="text-[11px] text-black/40 dark:text-white/35">
                {appList.length === 1 ? "1 application" : `${appList.length} applications`}
              </span>
            </CardHeader>
            <CardContent className={appList.length === 0 ? "p-0" : "p-3 pt-3"}>
              {appList.length === 0 ? (
                <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                  No applications yet —{" "}
                  <button className="text-brand hover:underline" onClick={() => setView("choose")}>
                    add a resource
                  </button>
                  .
                </p>
              ) : (
                <div className="grid gap-2 sm:grid-cols-2">
                  {appList.map((app) => (
                    <Link
                      key={app.id}
                      to="/applications/$applicationId"
                      params={{ applicationId: app.id }}
                      className="group flex items-center gap-3 rounded-[8px] border border-line/60 bg-base px-3.5 py-3 transition-colors hover:border-brand/40"
                    >
                      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-brand/15 text-[10px] font-semibold text-brand uppercase">
                        {initials(app.name)}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-2">
                          <span className="truncate text-[13px] font-medium">{app.name}</span>
                          <StatusBadge status={app.status} />
                        </span>
                        <span className="mt-0.5 block truncate font-mono text-[11px] text-black/45 dark:text-white/40">
                          {app.gitRepository || "no repository"}
                          {app.gitBranch && app.buildPack !== "image" ? ` @ ${app.gitBranch}` : ""}
                        </span>
                      </span>
                      <ChevronRight className="h-4 w-4 shrink-0 text-black/25 transition-transform group-hover:translate-x-0.5 dark:text-white/25" />
                    </Link>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Databases</CardTitle>
              <span className="text-[11px] text-black/40 dark:text-white/35">
                {dbList.length === 1 ? "1 database" : `${dbList.length} databases`}
              </span>
            </CardHeader>
            <CardContent className={dbList.length === 0 ? "p-0" : "p-3 pt-3"}>
              {dbList.length === 0 ? (
                <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
                  No databases yet —{" "}
                  <button className="text-brand hover:underline" onClick={() => setView("choose")}>
                    add a resource
                  </button>
                  .
                </p>
              ) : (
                <div className="grid gap-2 sm:grid-cols-2">
                  {dbList.map((db) => (
                    <div
                      key={db.id}
                      className="flex items-center gap-3 rounded-[8px] border border-line/60 bg-base px-3.5 py-3"
                    >
                      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-ok/15 text-ok">
                        <DatabaseIcon className="h-4 w-4" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-2">
                          <span className="truncate text-[13px] font-medium">{db.name}</span>
                          <StatusBadge status={db.status} />
                        </span>
                        <span className="mt-0.5 block truncate font-mono text-[11px] text-black/45 dark:text-white/40">
                          {db.kind} {db.version}
                          {db.publicPort ? ` · port ${db.publicPort}` : ""}
                        </span>
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </>
      ) : (
        <>
          <button
            className="mb-2 inline-flex items-center gap-1 text-[11px] text-black/45 transition-colors hover:text-current dark:text-white/40"
            onClick={() => setView("resources")}
          >
            <ChevronLeft className="h-3.5 w-3.5" />
            {project.data?.name ?? "Project"}
          </button>

          <PageHeader
            title="Choose a resource"
            description="Deploy an application or provision a database in this environment."
          />

          <div className="mb-3 flex flex-wrap items-center gap-2">
            <div className="relative min-w-56 flex-1">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-black/35 dark:text-white/30" />
              <Input
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Search resources"
                className="pl-8"
              />
            </div>
            <Select
              value={category}
              onChange={(event) => setCategory(event.target.value as Category)}
              className="w-44"
            >
              <option value="all">All categories</option>
              <option value="applications">Applications</option>
              <option value="databases">Databases</option>
            </Select>
          </div>

          {category !== "databases" && filteredSources.length > 0 && (
            <Card className="mb-4">
              <CardHeader>
                <CardTitle>Applications</CardTitle>
                <span className="text-[11px] text-black/40 dark:text-white/35">
                  Deploy from a repository or an image
                </span>
              </CardHeader>
              <CardContent>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {filteredSources.map((source) => (
                    <div
                      key={source.kind}
                      className="flex flex-col rounded-[8px] border border-line/60 bg-base p-3.5 transition-colors hover:border-brand/40"
                    >
                      <div className="flex items-center gap-2.5">
                        <span
                          className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md ${source.iconClass}`}
                        >
                          {source.icon}
                        </span>
                        <div className="min-w-0">
                          <div className="truncate text-[13px] font-semibold">{source.title}</div>
                          <div className="text-[10px] tracking-wider text-black/40 uppercase dark:text-white/30">
                            {source.tag}
                          </div>
                        </div>
                      </div>
                      <p className="mt-3 flex-1 text-[11px] leading-relaxed text-black/50 dark:text-white/45">
                        {source.description}
                      </p>
                      <div className="mt-3 flex justify-end border-t border-line/40 pt-3">
                        <Button
                          variant="primary"
                          size="sm"
                          onClick={() => openApplication(source.kind)}
                          title={environmentId === "" ? "No environment available yet" : undefined}
                        >
                          Deploy
                          <ChevronRight className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          )}

          {category !== "applications" && filteredDatabases.length > 0 && (
            <Card>
              <CardHeader>
                <CardTitle>Databases</CardTitle>
                <span className="text-[11px] text-black/40 dark:text-white/35">
                  One-click managed services with generated credentials
                </span>
              </CardHeader>
              <CardContent>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {filteredDatabases.map((card) => (
                    <div
                      key={card.kind}
                      className="flex flex-col rounded-[8px] border border-line/60 bg-base p-3.5 transition-colors hover:border-brand/40"
                    >
                      <div className="flex items-center gap-2.5">
                        <span
                          className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md ${card.iconClass}`}
                        >
                          <DatabaseIcon className="h-4 w-4" />
                        </span>
                        <div className="min-w-0">
                          <div className="truncate text-[13px] font-semibold">{card.title}</div>
                          <div className="text-[10px] tracking-wider text-black/40 uppercase dark:text-white/30">
                            version {card.version}
                          </div>
                        </div>
                      </div>
                      <p className="mt-3 flex-1 text-[11px] leading-relaxed text-black/50 dark:text-white/45">
                        {card.description}
                      </p>
                      <div className="mt-3 flex justify-end border-t border-line/40 pt-3">
                        <Button
                          variant="primary"
                          size="sm"
                          onClick={() => {
                            setCredentials(null);
                            setDbForm({ name: "", kind: card.kind, version: "" });
                          }}
                        >
                          Create
                          <ChevronRight className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          )}
        </>
      )}

      <Modal
        open={appSource !== null}
        onClose={() => setAppSource(null)}
        title={sourceMeta ? `Deploy ${sourceMeta.title}` : "Deploy an application"}
        description={sourceMeta?.description}
        footer={
          <>
            <Button variant="ghost" onClick={() => setAppSource(null)}>
              Cancel
            </Button>
            <Button variant="primary" disabled={!appValid || createApplication.isPending} onClick={submitApplication}>
              Create
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div>
            <Label htmlFor="app-name">Name</Label>
            <Input
              id="app-name"
              value={appForm.name}
              onChange={(event) => setAppForm({ ...appForm, name: event.target.value })}
              placeholder="api"
              autoFocus
            />
          </div>

          {(appSource === "git" || appSource === "dockerfile") && (
            <>
              <div>
                <Label htmlFor="app-repo">
                  {appSource === "git" ? "Repository URL" : "Repository URL (holding the Dockerfile)"}
                </Label>
                <Input
                  id="app-repo"
                  value={appForm.repository}
                  onChange={(event) => setAppForm({ ...appForm, repository: event.target.value })}
                  placeholder="https://github.com/acme/api.git"
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label htmlFor="app-branch">Branch</Label>
                  <Input
                    id="app-branch"
                    value={appForm.branch}
                    onChange={(event) => setAppForm({ ...appForm, branch: event.target.value })}
                  />
                </div>
                {appSource === "git" ? (
                  <div>
                    <Label htmlFor="app-pack">Build pack</Label>
                    <Select
                      id="app-pack"
                      value={appForm.buildPack}
                      onChange={(event) => setAppForm({ ...appForm, buildPack: event.target.value })}
                    >
                      {PACK_CHOICES.map((pack) => (
                        <option key={pack.value} value={pack.value}>
                          {pack.label}
                        </option>
                      ))}
                    </Select>
                  </div>
                ) : (
                  <div>
                    <Label htmlFor="app-dockerfile">Dockerfile path</Label>
                    <Input
                      id="app-dockerfile"
                      value={appForm.dockerfilePath}
                      onChange={(event) =>
                        setAppForm({ ...appForm, dockerfilePath: event.target.value })
                      }
                      placeholder="Dockerfile"
                    />
                  </div>
                )}
              </div>
              {appSource === "git" && appForm.buildPack === "dockerfile" && (
                <div>
                  <Label htmlFor="app-dockerfile-git">Dockerfile path</Label>
                  <Input
                    id="app-dockerfile-git"
                    value={appForm.dockerfilePath}
                    onChange={(event) =>
                      setAppForm({ ...appForm, dockerfilePath: event.target.value })
                    }
                    placeholder="Dockerfile"
                  />
                </div>
              )}
            </>
          )}

          {appSource === "compose" && (
            <div>
              <Label htmlFor="app-compose">Compose definition</Label>
              <Textarea
                id="app-compose"
                rows={9}
                value={appForm.compose}
                onChange={(event) => setAppForm({ ...appForm, compose: event.target.value })}
                placeholder={COMPOSE_PLACEHOLDER}
              />
            </div>
          )}

          {appSource === "image" && (
            <div>
              <Label htmlFor="app-image">Image reference</Label>
              <Input
                id="app-image"
                value={appForm.repository}
                onChange={(event) => setAppForm({ ...appForm, repository: event.target.value })}
                placeholder="nginx:alpine"
              />
            </div>
          )}

          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label htmlFor="app-port">Container port</Label>
              <Input
                id="app-port"
                type="number"
                value={appForm.port}
                onChange={(event) => setAppForm({ ...appForm, port: Number(event.target.value) })}
              />
            </div>
            <div>
              <Label htmlFor="app-health">Health check path</Label>
              <Input
                id="app-health"
                value={appForm.healthCheck}
                onChange={(event) => setAppForm({ ...appForm, healthCheck: event.target.value })}
                placeholder="/"
              />
            </div>
          </div>

          {environmentId === "" && (
            <p className="text-[11px] text-warn">
              This project has no environment yet — reload once one exists.
            </p>
          )}
          {createApplication.isError && (
            <p className="text-[11px] text-danger">{errorText(createApplication.error)}</p>
          )}
        </div>
      </Modal>

      <Modal
        open={dbForm !== null}
        onClose={() => setDbForm(null)}
        title={credentials ? "Database created" : "Create a database"}
        description={
          credentials
            ? undefined
            : "A root password is generated and stored encrypted; it is shown once, right after creation."
        }
        footer={
          credentials ? (
            <Button variant="primary" onClick={() => setDbForm(null)}>
              Done
            </Button>
          ) : (
            <>
              <Button variant="ghost" onClick={() => setDbForm(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                disabled={!dbForm || dbForm.name.trim() === "" || environmentId === "" || createDatabase.isPending}
                onClick={() =>
                  dbForm &&
                  createDatabase.mutate({
                    name: dbForm.name.trim(),
                    kind: dbForm.kind,
                    version: dbForm.version.trim() || undefined,
                    environmentId,
                  })
                }
              >
                Create
              </Button>
            </>
          )
        }
      >
        {credentials ? (
          <div className="space-y-3">
            <p className="text-[12px] text-black/60 dark:text-white/55">
              Store these credentials now — the password is not shown again. They are already
              attached to this database&apos;s environment variables for linked apps.
            </p>
            <div className="space-y-2 rounded-md bg-black/25 p-3 font-mono text-[12px]">
              <div className="flex justify-between gap-4">
                <span className="text-white/45">user</span>
                <span className="select-all">{credentials.user || "default"}</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-white/45">password</span>
                <span className="select-all">{credentials.password}</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-white/45">port</span>
                <span>{credentials.port}</span>
              </div>
            </div>
          </div>
        ) : (
          <div className="space-y-3">
            <div>
              <Label htmlFor="db-name">Name</Label>
              <Input
                id="db-name"
                value={dbForm?.name ?? ""}
                onChange={(event) => dbForm && setDbForm({ ...dbForm, name: event.target.value })}
                placeholder={`${dbForm?.kind ?? "app"}-db`}
                autoFocus
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label htmlFor="db-kind">Kind</Label>
                <Select
                  id="db-kind"
                  value={dbForm?.kind ?? "postgres"}
                  onChange={(event) => dbForm && setDbForm({ ...dbForm, kind: event.target.value })}
                >
                  {DATABASE_CARDS.map((card) => (
                    <option key={card.kind} value={card.kind}>
                      {card.title}
                    </option>
                  ))}
                </Select>
              </div>
              <div>
                <Label htmlFor="db-version">Version</Label>
                <Input
                  id="db-version"
                  value={dbForm?.version ?? ""}
                  onChange={(event) => dbForm && setDbForm({ ...dbForm, version: event.target.value })}
                  placeholder="default"
                />
              </div>
            </div>
            {createDatabase.isError && (
              <p className="text-[11px] text-danger">{errorText(createDatabase.error)}</p>
            )}
          </div>
        )}
      </Modal>
    </>
  );
}

export const Route = createFileRoute("/projects/$projectId")({
  component: ProjectDetailPage,
});
