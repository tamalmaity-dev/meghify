import { useState } from "react";
import { Copy } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { useChangePassword, useSession } from "@/lib/session";
import { createFileRoute } from "@tanstack/react-router";

export function SettingsPage() {
  const { user, version } = useSession();
  const changePassword = useChangePassword();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");

  const submit = () => {
    if (next !== confirm) {
      toast.error("New passwords do not match");
      return;
    }
    changePassword.mutate(
      { currentPassword: current, newPassword: next },
      {
        onSuccess: () => {
          toast.success("Password updated. Other sessions were signed out.");
          setCurrent("");
          setNext("");
          setConfirm("");
        },
        onError: (error) => toast.error(error.message),
      },
    );
  };

  return (
    <>
      <PageHeader title="Settings" description="Instance and account configuration." />

      <div className="grid gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Account</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-[12px]">
            <div className="flex items-baseline justify-between gap-4">
              <span className="text-black/45 dark:text-white/40">Email</span>
              <span className="font-mono">{user?.email}</span>
            </div>
            <div className="flex items-baseline justify-between gap-4">
              <span className="text-black/45 dark:text-white/40">Role</span>
              <span className="font-mono">{user?.role}</span>
            </div>
            <div className="flex items-baseline justify-between gap-4">
              <span className="text-black/45 dark:text-white/40">Two-factor</span>
              <span className="font-mono">{user?.totpEnabled ? "enabled" : "off"}</span>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Panel</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-[12px]">
            <div className="flex items-baseline justify-between gap-4">
              <span className="text-black/45 dark:text-white/40">Version</span>
              <span className="font-mono">{version ?? "dev"}</span>
            </div>
            <div className="flex items-baseline justify-between gap-4">
              <span className="text-black/45 dark:text-white/40">Database</span>
              <span className="font-mono">SQLite (WAL)</span>
            </div>
            <div className="flex items-baseline justify-between gap-4">
              <span className="text-black/45 dark:text-white/40">Queue</span>
              <span className="font-mono">database backed</span>
            </div>
            <p className="pt-1 text-[11px] text-black/45 dark:text-white/40">
              Settings are read from environment variables at boot. AUTH_SECRET, DB_DSN,
              DATA_DIR and DOCKER_HOST drive behaviour.
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Change password</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <div>
              <Label htmlFor="current-password">Current password</Label>
              <Input
                id="current-password"
                type="password"
                value={current}
                onChange={(event) => setCurrent(event.target.value)}
              />
            </div>
            <div>
              <Label htmlFor="new-password">New password</Label>
              <Input
                id="new-password"
                type="password"
                value={next}
                onChange={(event) => setNext(event.target.value)}
              />
            </div>
            <div>
              <Label htmlFor="confirm-password">Confirm new password</Label>
              <Input
                id="confirm-password"
                type="password"
                value={confirm}
                onChange={(event) => setConfirm(event.target.value)}
              />
            </div>
            <Button
              variant="primary"
              disabled={!current || !next || changePassword.isPending}
              onClick={submit}
            >
              Update password
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>API access</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-[11px] text-black/50 dark:text-white/45">
              Issue a token to automate deployments from a pipeline. Tokens are shown
              once and stored only as a hash.
            </p>
            <Button
              variant="neutral"
              onClick={async () => {
                const result = await api.post<{ token: string }>("/tokens", { name: "pipeline" });
                await navigator.clipboard.writeText(result.token);
                toast.success("Token created and copied to clipboard");
              }}
            >
              <Copy className="h-4 w-4" />
              Create and copy token
            </Button>
          </CardContent>
        </Card>
      </div>
    </>
  );
}

export const Route = createFileRoute("/settings")({
  component: SettingsPage,
});
