import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Github, Plus, RefreshCw, Search, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Link } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { displayHost } from "@/lib/utils";
import { PageHeader } from "@/components/app-shell";
import { GitHubAppDialog } from "@/components/github-app-dialog";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { GitHubApp, GitHubInstallation } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

/** github.com unless the source points at a GitHub Enterprise server. */
const webBase = (app: GitHubApp) => app.baseUrl || "https://github.com";

/**
 * Sources are the Git providers the panel deploys from. A GitHub App created
 * here unlocks private repositories, repository pickers and push-to-deploy
 * webhooks — the same flow Coolify and Dokploy offer.
 */
export function SourcesPage() {
  const queryClient = useQueryClient();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [sourceSearch, setSourceSearch] = useState("");

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

  const all = apps.data ?? [];
  const query = sourceSearch.trim().toLowerCase();
  const list = all.filter((app) => query === "" || app.name.toLowerCase().includes(query));
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
              disabled={sync.isPending || all.length === 0}
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

      {all.length === 0 ? (
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
        <>
          <div className="mb-3 flex flex-wrap items-center gap-3">
            <div className="relative min-w-56 flex-1">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-black/35 dark:text-white/30" />
              <Input
                value={sourceSearch}
                onChange={(event) => setSourceSearch(event.target.value)}
                placeholder="Search sources"
                className="pl-8"
              />
            </div>
            <span className="text-[11px] text-black/40 dark:text-white/35">
              {all.length === 1 ? "1 source" : `${all.length} sources`}
            </span>
          </div>

          {list.length === 0 ? (
            <Card>
              <CardContent className="py-8 text-center text-[12px] text-black/45 dark:text-white/40">
                No source matches that search.
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {list.map((app) => {
                const attached = installationsFor(app.id);
                const connected = app.configured && attached.length > 0;
                return (
                  <Card key={app.id}>
                    <CardContent className="flex h-full flex-col px-4 py-3.5">
                      <div className="flex items-start gap-3">
                        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-black/[0.06] dark:bg-white/[0.06]">
                          <Github className="h-4 w-4" />
                        </span>
                        <div className="min-w-0 flex-1">
                          <Link
                            to="/sources/$appId"
                            params={{ appId: app.id }}
                            className="block truncate text-[13px] font-medium hover:underline"
                          >
                            {app.name}
                          </Link>
                          <div className="mt-0.5 truncate text-[11px] text-black/45 dark:text-white/40">
                            GitHub · {displayHost(webBase(app))}
                          </div>
                        </div>
                      </div>

                      <div className="mt-3 flex flex-wrap items-center gap-2">
                        {connected ? (
                          <span className="inline-flex items-center gap-1.5 rounded-full bg-ok/10 px-2 py-0.5 text-[10.5px] font-medium text-ok">
                            <span className="h-1.5 w-1.5 rounded-full bg-ok" />
                            Connected
                          </span>
                        ) : app.configured ? (
                          <span className="inline-flex items-center gap-1.5 rounded-full bg-warn/10 px-2 py-0.5 text-[10.5px] font-medium text-warn">
                            <span className="h-1.5 w-1.5 rounded-full bg-warn" />
                            Not installed
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1.5 rounded-full bg-warn/10 px-2 py-0.5 text-[10.5px] font-medium text-warn">
                            <span className="h-1.5 w-1.5 rounded-full bg-warn" />
                            Setup incomplete
                          </span>
                        )}
                        {app.configured && (
                          <span className="font-mono text-[10px] text-black/40 dark:text-white/30">
                            App ID {app.appId}
                          </span>
                        )}
                      </div>

                      <p className="mt-2 flex-1 text-[11px] leading-relaxed text-black/50 dark:text-white/45">
                        {attached.length > 0
                          ? attached
                              .map(
                                (installation) =>
                                  `${installation.accountLogin} (${
                                    installation.repositorySelection === "all"
                                      ? "all repositories"
                                      : "selected repositories"
                                  })`,
                              )
                              .join(", ")
                          : app.configured
                            ? "Not installed on any account yet — press Install to pick repositories."
                            : "Register the app with GitHub to finish setup."}
                      </p>

                      <div className="mt-3 flex items-center justify-end gap-2 border-t border-line/40 pt-2.5">
                        {app.configured && app.slug && !connected && (
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
                            {app.configured ? "Open" : "Finish setup"}
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
                    </CardContent>
                  </Card>
                );
              })}
            </div>
          )}
        </>
      )}

      <GitHubAppDialog open={dialogOpen} onClose={() => setDialogOpen(false)} />
    </>
  );
}

export const Route = createFileRoute("/sources/")({
  component: SourcesPage,
});
