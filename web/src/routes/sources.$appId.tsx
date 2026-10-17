import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ExternalLink, Github, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Select, Textarea } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import type { GitHubApp, GitHubInstallation, GitHubManifest } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

/** github.com unless the source points at a GitHub Enterprise server. */
const webBase = (app: GitHubApp) => app.baseUrl || "https://github.com";

const PERMISSIONS = [
  { name: "Contents", access: "read", why: "clone the repository, including private ones" },
  { name: "Metadata", access: "read", why: "read basic repository information" },
  { name: "Email addresses", access: "read", why: "attribute commits to their authors" },
];

/**
 * One GitHub App: the Coolify-style setup screen until GitHub issues the
 * credentials, and the source's home afterwards (installations, install link).
 */
export function SourceDetailPage() {
  const { appId } = useParams({ from: "/sources/$appId" });
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const [manualOpen, setManualOpen] = useState(false);
  const [manual, setManual] = useState({
    appId: "",
    slug: "",
    privateKey: "",
    webhookSecret: "",
    clientId: "",
    clientSecret: "",
  });

  const app = useQuery({
    queryKey: ["github-apps", appId],
    queryFn: () => api.get<GitHubApp>(`/github/apps/${appId}`),
  });
  const manifest = useQuery({
    queryKey: ["github-manifest", appId],
    queryFn: () => api.get<GitHubManifest>(`/github/apps/${appId}/manifest`),
    enabled: app.data ? !app.data.configured : false,
  });
  const installations = useQuery({
    queryKey: ["github-installations"],
    queryFn: () => api.get<GitHubInstallation[]>("/github/installations"),
    enabled: app.data?.configured ?? false,
  });

  const saveManual = useMutation({
    mutationFn: () =>
      api.post<GitHubApp>(`/github/apps/${appId}/credentials`, {
        appId: Number(manual.appId),
        slug: manual.slug.trim(),
        privateKey: manual.privateKey,
        webhookSecret: manual.webhookSecret.trim(),
        clientId: manual.clientId.trim(),
        clientSecret: manual.clientSecret,
      }),
    onSuccess: async () => {
      toast.success("GitHub App connected");
      setManualOpen(false);
      await queryClient.invalidateQueries({ queryKey: ["github-apps", appId] });
      await queryClient.invalidateQueries({ queryKey: ["github-apps"] });
    },
  });

  const sync = useMutation({
    mutationFn: () => api.post<GitHubInstallation[]>(`/github/apps/${appId}/sync`),
    onSuccess: async () => {
      toast.success("Installations refreshed");
      await queryClient.invalidateQueries({ queryKey: ["github-installations"] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const remove = useMutation({
    mutationFn: () => api.delete(`/github/apps/${appId}`),
    onSuccess: async () => {
      toast.success("Source removed");
      await queryClient.invalidateQueries({ queryKey: ["github-apps"] });
      navigate({ to: "/sources" });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  if (!app.data) {
    return (
      <>
        <Link className="mb-2 inline-flex items-center gap-1 text-[11px] text-black/45 dark:text-white/40" to="/sources">
          <ChevronLeft className="h-3.5 w-3.5" />
          All sources
        </Link>
        <Card>
          <CardContent className="py-8 text-center text-[12px] text-black/45 dark:text-white/40">
            {app.isError ? errorText(app.error) : "Loading source…"}
          </CardContent>
        </Card>
      </>
    );
  }

  const source = app.data;
  const base = webBase(source);
  const installUrl = `${base}/apps/${source.slug}/installations/new?state=${source.id}`;
  const attached = (installations.data ?? []).filter(
    (installation) => installation.githubAppId === source.id,
  );

  return (
    <>
      <button
        className="mb-2 inline-flex items-center gap-1 text-[11px] text-black/45 transition-colors hover:text-current dark:text-white/40"
        onClick={() => navigate({ to: "/sources" })}
      >
        <ChevronLeft className="h-3.5 w-3.5" />
        All sources
      </button>

      <PageHeader
        title={source.name}
        description={
          source.configured
            ? `GitHub App ${source.appId} · ${base.replace("https://", "")}/apps/${source.slug}`
            : "Setup incomplete — confirm the app on GitHub to finish connecting it."
        }
        actions={
          source.configured ? (
            <>
              <Button variant="neutral" onClick={() => sync.mutate()} disabled={sync.isPending}>
                <RefreshCw className={sync.isPending ? "h-4 w-4 animate-spin" : "h-4 w-4"} />
                Refresh
              </Button>
              <Button
                variant="danger"
                disabled={remove.isPending}
                onClick={() => {
                  if (
                    window.confirm(
                      `Remove "${source.name}" from Meghify? Applications keep deploying, but automatic deploys stop. The app on GitHub is not deleted.`,
                    )
                  ) {
                    remove.mutate();
                  }
                }}
              >
                <Trash2 className="h-4 w-4" />
                Remove
              </Button>
            </>
          ) : undefined
        }
      />

      {!source.configured ? (
        <div className="grid gap-3 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Automated installation</CardTitle>
              <span className="rounded-full bg-ok/10 px-2 py-0.5 text-[10px] font-medium text-ok">
                Recommended
              </span>
            </CardHeader>
            <CardContent className="space-y-4 text-[12px]">
              <div>
                <Label>Webhook endpoint</Label>
                <Select value="instance" onChange={() => undefined} disabled>
                  <option value="instance">
                    {manifest.data?.webhookUrl ?? "Loading endpoint…"}
                  </option>
                </Select>
                <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
                  GitHub sends a push event here every time code lands on a tracked branch.
                </p>
              </div>

              <div>
                <Label>Mandatory permissions</Label>
                <div className="mt-1 overflow-hidden rounded-md border border-line/60">
                  {PERMISSIONS.map((permission) => (
                    <div
                      key={permission.name}
                      className="flex items-center justify-between gap-3 border-b border-line/40 px-3 py-2 last:border-b-0"
                    >
                      <span className="font-medium">{permission.name}</span>
                      <span className="text-[10.5px] text-black/40 dark:text-white/30">
                        {permission.why}
                      </span>
                      <span className="rounded-full bg-black/[0.05] px-1.5 py-0.5 font-mono text-[10px] dark:bg-white/[0.06]">
                        {permission.access}
                      </span>
                    </div>
                  ))}
                </div>
              </div>

              {manifest.data ? (
                <form method="post" action={manifest.data.postUrl}>
                  <input
                    type="hidden"
                    name="manifest"
                    value={JSON.stringify(manifest.data.manifest)}
                  />
                  <Button type="submit" variant="primary" className="w-full">
                    <Github className="h-4 w-4" />
                    Register With GitHub
                  </Button>
                </form>
              ) : (
                <Button variant="primary" className="w-full" disabled>
                  {manifest.isError ? errorText(manifest.error) : "Preparing manifest…"}
                </Button>
              )}
              <p className="text-[10.5px] leading-relaxed text-black/40 dark:text-white/30">
                GitHub opens a confirmation page showing these permissions, creates the app under
                your account, then returns you here with its credentials.
              </p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Manual installation</CardTitle>
              <span className="rounded-full bg-black/[0.05] px-2 py-0.5 text-[10px] font-medium text-black/50 dark:bg-white/[0.06] dark:text-white/40">
                Advanced
              </span>
            </CardHeader>
            <CardContent className="space-y-3 text-[12px]">
              <p className="leading-relaxed text-black/55 dark:text-white/45">
                Already created the app on GitHub yourself? Paste its credentials and Meghify will
                verify the private key before storing it.
              </p>
              <ul className="list-inside list-disc space-y-1 text-[11px] text-black/45 dark:text-white/40">
                <li>Webhook URL: the endpoint shown on the left</li>
                <li>Permissions: Contents (read), Metadata (read)</li>
                <li>Events: pushes</li>
              </ul>
              <Button variant="neutral" onClick={() => setManualOpen(true)}>
                Continue manually
              </Button>
            </CardContent>
          </Card>
        </div>
      ) : (
        <div className="space-y-3">
          <Card>
            <CardHeader>
              <CardTitle>Installations</CardTitle>
              <span className="text-[11px] text-black/40 dark:text-white/35">
                {attached.length === 0
                  ? "not installed yet"
                  : attached.length === 1
                    ? "1 account"
                    : `${attached.length} accounts`}
              </span>
            </CardHeader>
            <CardContent className="space-y-3 text-[12px]">
              {attached.length === 0 ? (
                <p className="text-black/50 dark:text-white/45">
                  Install the app on the GitHub account that owns your repositories. Only the
                  repositories you pick become visible to Meghify.
                </p>
              ) : (
                <div className="space-y-1.5">
                  {attached.map((installation) => (
                    <div
                      key={installation.id}
                      className="flex items-center justify-between gap-3 rounded-md bg-recessed px-3 py-2"
                    >
                      <div className="min-w-0">
                        <div className="truncate font-medium">{installation.accountLogin}</div>
                        <div className="text-[10.5px] text-black/40 dark:text-white/30">
                          {installation.accountType === "Organization" ? "Organization" : "User"}{" "}
                          ·{" "}
                          {installation.repositorySelection === "all"
                            ? "all repositories"
                            : "selected repositories"}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
              <div className="flex flex-wrap items-center gap-2 border-t border-line/40 pt-3">
                <a href={installUrl} target="_blank" rel="noreferrer">
                  <Button variant="primary" size="sm">
                    <ExternalLink className="h-3.5 w-3.5" />
                    {attached.length === 0 ? "Install on GitHub" : "Manage on GitHub"}
                  </Button>
                </a>
                <span className="text-[10.5px] text-black/40 dark:text-white/30">
                  Grant access to more repositories any time on GitHub; press Refresh afterwards.
                </span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Automatic deploys</CardTitle>
            </CardHeader>
            <CardContent className="text-[12px] leading-relaxed text-black/55 dark:text-white/45">
              Applications created from this source redeploy whenever a push lands on their tracked
              branch. Toggle it per application on its page under{" "}
              <span className="text-current">Source → Deploy on every push</span>.
            </CardContent>
          </Card>
        </div>
      )}

      <Modal
        open={manualOpen}
        onClose={() => setManualOpen(false)}
        title="Connect an existing GitHub App"
        description="Find these values on the app's settings page on GitHub. The private key is generated there under “Generate a private key”."
        footer={
          <>
            <Button variant="ghost" onClick={() => setManualOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={
                manual.appId.trim() === "" ||
                manual.privateKey.trim() === "" ||
                manual.webhookSecret.trim() === "" ||
                saveManual.isPending
              }
              onClick={() => saveManual.mutate()}
            >
              Connect
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label htmlFor="manual-app-id">App ID</Label>
              <Input
                id="manual-app-id"
                inputMode="numeric"
                value={manual.appId}
                onChange={(event) => setManual({ ...manual, appId: event.target.value })}
                placeholder="123456"
              />
            </div>
            <div>
              <Label htmlFor="manual-slug">App slug (optional)</Label>
              <Input
                id="manual-slug"
                value={manual.slug}
                onChange={(event) => setManual({ ...manual, slug: event.target.value })}
                placeholder="my-app"
              />
            </div>
          </div>
          <div>
            <Label htmlFor="manual-key">Private key (PEM)</Label>
            <Textarea
              id="manual-key"
              rows={6}
              value={manual.privateKey}
              onChange={(event) => setManual({ ...manual, privateKey: event.target.value })}
              placeholder={"-----BEGIN RSA PRIVATE KEY-----\n…"}
            />
          </div>
          <div>
            <Label htmlFor="manual-secret">Webhook secret</Label>
            <Input
              id="manual-secret"
              value={manual.webhookSecret}
              onChange={(event) => setManual({ ...manual, webhookSecret: event.target.value })}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label htmlFor="manual-client-id">Client ID (optional)</Label>
              <Input
                id="manual-client-id"
                value={manual.clientId}
                onChange={(event) => setManual({ ...manual, clientId: event.target.value })}
              />
            </div>
            <div>
              <Label htmlFor="manual-client-secret">Client secret (optional)</Label>
              <Input
                id="manual-client-secret"
                value={manual.clientSecret}
                onChange={(event) => setManual({ ...manual, clientSecret: event.target.value })}
              />
            </div>
          </div>
          {saveManual.isError && (
            <p className="text-[11px] text-danger">{errorText(saveManual.error)}</p>
          )}
        </div>
      </Modal>
    </>
  );
}

export const Route = createFileRoute("/sources/$appId")({
  component: SourceDetailPage,
});
