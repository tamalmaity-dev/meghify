import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Github, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Link, useNavigate } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Select } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import type { GitHubApp, GitHubInstallation } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

/** github.com unless the source points at a GitHub Enterprise server. */
const webBase = (app: GitHubApp) => app.baseUrl || "https://github.com";

/** GitHub app names are capped at 34 characters and must be globally unique. */
function suggestedName() {
  const host = window.location.hostname.replace(/[^a-z0-9-]/gi, "-").toLowerCase();
  return `meghify-${host}`.slice(0, 34);
}

/**
 * Sources are the Git providers the panel deploys from. A GitHub App created
 * here unlocks private repositories, repository pickers and push-to-deploy
 * webhooks — the same flow Coolify and Dokploy offer.
 */
export function SourcesPage() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [selfHosted, setSelfHosted] = useState(false);
  const [form, setForm] = useState({ name: suggestedName(), organization: "", baseUrl: "" });

  // The manifest and installation flows bounce the browser back here with a
  // result in the query string; surface it once, then clean the URL.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const result = params.get("github");
    const installed = params.get("installed");
    if (result === "connected") {
      toast.success(`GitHub App "${params.get("message") ?? ""}" registered`);
    } else if (result === "error") {
      toast.error(params.get("message") || "GitHub setup failed");
    } else if (installed) {
      toast.success(`App installed on ${installed}`);
    }
    if (result || installed) {
      window.history.replaceState({}, "", "/sources");
    }
  }, []);

  const apps = useQuery({
    queryKey: ["github-apps"],
    queryFn: () => api.get<GitHubApp[]>("/github/apps"),
  });
  const installations = useQuery({
    queryKey: ["github-installations"],
    queryFn: () => api.get<GitHubInstallation[]>("/github/installations"),
  });

  const create = useMutation({
    mutationFn: () =>
      api.post<GitHubApp>("/github/apps", {
        name: form.name.trim(),
        organization: form.organization.trim(),
        baseUrl: selfHosted ? form.baseUrl.trim() : "",
      }),
    onSuccess: async (app) => {
      await queryClient.invalidateQueries({ queryKey: ["github-apps"] });
      setDialogOpen(false);
      setForm({ name: suggestedName(), organization: "", baseUrl: "" });
      setSelfHosted(false);
      navigate({ to: "/sources/$appId", params: { appId: app.id } });
    },
  });

  const sync = useMutation({
    mutationFn: () => api.post<GitHubInstallation[]>("/github/installations/sync"),
    onSuccess: async () => {
      toast.success("Installations refreshed from GitHub");
      await queryClient.invalidateQueries({ queryKey: ["github-installations"] });
      await queryClient.invalidateQueries({ queryKey: ["github-apps"] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/github/apps/${id}`),
    onSuccess: async () => {
      toast.success("Source removed");
      await queryClient.invalidateQueries({ queryKey: ["github-apps"] });
      await queryClient.invalidateQueries({ queryKey: ["github-installations"] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const list = apps.data ?? [];
  const installationsFor = (appId: string) =>
    (installations.data ?? []).filter((installation) => installation.githubAppId === appId);

  return (
    <>
      <PageHeader
        title="Sources"
        description="Git providers Meghify deploys from. A GitHub App unlocks private repositories and deploys on every push."
        actions={
          <>
            <Button
              variant="neutral"
              onClick={() => sync.mutate()}
              disabled={sync.isPending || list.length === 0}
              title="Refresh installations from GitHub"
            >
              <RefreshCw className={sync.isPending ? "h-4 w-4 animate-spin" : "h-4 w-4"} />
              Refresh
            </Button>
            <Button variant="primary" onClick={() => setDialogOpen(true)}>
              <Plus className="h-4 w-4" />
              Add GitHub App
            </Button>
          </>
        }
      />

      {list.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-12 text-center">
            <span className="flex h-10 w-10 items-center justify-center rounded-md bg-black/[0.06] dark:bg-white/[0.06]">
              <Github className="h-5 w-5 opacity-70" />
            </span>
            <div>
              <div className="text-[13px] font-medium">No sources yet</div>
              <p className="mx-auto mt-1 max-w-md text-[12px] leading-relaxed text-black/45 dark:text-white/40">
                Add a GitHub App to deploy private repositories and redeploy automatically when
                code is pushed. Meghify walks you through creating the app on GitHub.
              </p>
            </div>
            <Button variant="primary" onClick={() => setDialogOpen(true)}>
              <Plus className="h-4 w-4" />
              Add GitHub App
            </Button>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {list.map((app) => {
            const attached = installationsFor(app.id);
            return (
              <Card key={app.id}>
                <CardContent className="px-4 py-3">
                  <div className="flex flex-wrap items-center gap-3">
                    <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-black/[0.06] dark:bg-white/[0.06]">
                      <Github className="h-4 w-4" />
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate text-[13px] font-medium">{app.name}</span>
                        {app.configured ? (
                          <span className="rounded-full bg-ok/10 px-1.5 py-0.5 text-[10px] text-ok">
                            App ID {app.appId}
                          </span>
                        ) : (
                          <span className="rounded-full bg-warn/10 px-1.5 py-0.5 text-[10px] text-warn">
                            Setup incomplete
                          </span>
                        )}
                      </div>
                      <div className="mt-0.5 truncate font-mono text-[11px] text-black/45 dark:text-white/40">
                        {app.configured && app.slug
                          ? `${webBase(app).replace("https://", "")}/apps/${app.slug}`
                          : "Register the app with GitHub to finish"}
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      {app.configured && app.slug && (
                        <a
                          href={`${webBase(app)}/apps/${app.slug}/installations/new?state=${app.id}`}
                          target="_blank"
                          rel="noreferrer"
                        >
                          <Button variant="neutral" size="sm">
                            Install
                          </Button>
                        </a>
                      )}
                      <Link to="/sources/$appId" params={{ appId: app.id }}>
                        <Button variant="neutral" size="sm">
                          Setup
                          <ChevronRight className="h-3.5 w-3.5" />
                        </Button>
                      </Link>
                      <Button
                        variant="ghost"
                        size="icon"
                        title="Remove source"
                        disabled={remove.isPending}
                        onClick={() => {
                          if (
                            window.confirm(
                              `Remove "${app.name}" from Meghify? Applications keep deploying, but automatic deploys stop. The app on GitHub is not deleted.`,
                            )
                          ) {
                            remove.mutate(app.id);
                          }
                        }}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </div>

                  <div className="mt-2 border-t border-line/40 pt-2 text-[11px] text-black/50 dark:text-white/45">
                    {attached.length === 0 ? (
                      app.configured ? (
                        "Not installed on any account yet — press Install to pick repositories."
                      ) : (
                        "Finish the setup to install it on your GitHub account."
                      )
                    ) : (
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-black/40 dark:text-white/35">Installed on</span>
                        {attached.map((installation) => (
                          <span
                            key={installation.id}
                            className="rounded-md bg-black/[0.05] px-1.5 py-0.5 font-mono text-[10.5px] dark:bg-white/[0.06]"
                          >
                            {installation.accountLogin}
                            <span className="text-black/40 dark:text-white/35">
                              {installation.repositorySelection === "all"
                                ? " · all repositories"
                                : " · selected repositories"}
                            </span>
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <Modal
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        title="New GitHub App"
        description="Meghify creates the app on GitHub for you with the permissions it needs — nothing is installed until you confirm on GitHub."
        footer={
          <>
            <Button variant="ghost" onClick={() => setDialogOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={
                form.name.trim() === "" ||
                (selfHosted && form.baseUrl.trim() === "") ||
                create.isPending
              }
              onClick={() => create.mutate()}
            >
              Continue
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div>
            <Label htmlFor="github-app-name">Name</Label>
            <Input
              id="github-app-name"
              value={form.name}
              maxLength={34}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
              placeholder="meghify-my-vps"
              autoFocus
            />
            <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
              The app name must be unique on GitHub and cannot be longer than 34 characters.
            </p>
          </div>
          <div>
            <Label htmlFor="github-app-org">Organization (optional)</Label>
            <Input
              id="github-app-org"
              value={form.organization}
              onChange={(event) => setForm({ ...form, organization: event.target.value })}
              placeholder="Leave empty to use your personal account"
            />
          </div>
          <div>
            <Label htmlFor="github-app-host">GitHub host</Label>
            <Select
              id="github-app-host"
              value={selfHosted ? "self" : "github"}
              onChange={(event) => setSelfHosted(event.target.value === "self")}
            >
              <option value="github">github.com</option>
              <option value="self">Self-hosted GitHub (Enterprise)</option>
            </Select>
            {selfHosted && (
              <Input
                className="mt-2"
                value={form.baseUrl}
                onChange={(event) => setForm({ ...form, baseUrl: event.target.value })}
                placeholder="https://git.example.com"
              />
            )}
          </div>
          {create.isError && <p className="text-[11px] text-danger">{errorText(create.error)}</p>}
        </div>
      </Modal>
    </>
  );
}

export const Route = createFileRoute("/sources/")({
  component: SourcesPage,
});
