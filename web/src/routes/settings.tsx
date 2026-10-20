import { useState } from "react";
import { Copy, KeyRound, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import {
  useChangePassword,
  useSession,
  useTotpConfirm,
  useTotpDisable,
  useTotpEnroll,
} from "@/lib/session";
import { createFileRoute } from "@tanstack/react-router";

/** Clipboard is unavailable on plain-HTTP origins. Never fail silently: the
 * user needs a way to get a one-time token or TOTP seed off the screen. */
async function copyText(value: string, label: string) {
  try {
    await navigator.clipboard.writeText(value);
    toast.success(`${label} copied`);
  } catch {
    toast.error("Clipboard is unavailable. Select the value and copy it manually.");
  }
}

export function SettingsPage() {
  const { user, version } = useSession();
  const changePassword = useChangePassword();
  const enroll = useTotpEnroll();
  const confirmTotp = useTotpConfirm();
  const disableTotp = useTotpDisable();

  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");

  const [totpUri, setTotpUri] = useState("");
  const [totpSecret, setTotpSecret] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [disablePassword, setDisablePassword] = useState("");

  const [issuedToken, setIssuedToken] = useState("");

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

  const startEnrollment = () => {
    enroll.mutate(undefined, {
      onSuccess: (data) => {
        setTotpUri(data.uri);
        setTotpSecret(data.secret);
        setTotpCode("");
      },
      onError: (error) => toast.error(error.message),
    });
  };

  const confirmEnrollment = () => {
    confirmTotp.mutate(totpCode.trim(), {
      onSuccess: () => {
        toast.success("Two-factor authentication enabled");
        setTotpUri("");
        setTotpSecret("");
        setTotpCode("");
      },
      onError: (error) => toast.error(error.message),
    });
  };

  const disable = () => {
    disableTotp.mutate(disablePassword, {
      onSuccess: () => {
        toast.success("Two-factor authentication disabled");
        setDisablePassword("");
      },
      onError: (error) => toast.error(error.message),
    });
  };

  const createToken = async () => {
    try {
      const result = await api.post<{ token: string }>("/tokens", {
        name: "pipeline",
        // An explicit scope list documents intent and limits a leaked token.
        scopes: ["read", "write", "deploy"],
      });
      setIssuedToken(result.token);
      await copyText(result.token, "Token");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not create token");
    }
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
            <CardTitle>Two-factor authentication</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {user?.totpEnabled ? (
              <>
                <div className="flex items-center gap-2 rounded-md bg-recessed px-3 py-2 text-[12px] text-emerald-600 dark:text-emerald-400">
                  <ShieldCheck className="h-4 w-4" />
                  TOTP is protecting this account.
                </div>
                <div>
                  <Label htmlFor="disable-totp-password">Confirm your password</Label>
                  <Input
                    id="disable-totp-password"
                    type="password"
                    value={disablePassword}
                    onChange={(event) => setDisablePassword(event.target.value)}
                  />
                </div>
                <Button
                  variant="danger"
                  disabled={!disablePassword || disableTotp.isPending}
                  onClick={disable}
                >
                  Disable two-factor authentication
                </Button>
              </>
            ) : totpSecret ? (
              <>
                <p className="text-[11px] text-black/50 dark:text-white/45">
                  Add this account to your authenticator app, then enter the six-digit code.
                  The secret is shown only while you finish enrolment.
                </p>
                <div>
                  <Label>Secret</Label>
                  <div className="flex gap-2">
                    <Input readOnly value={totpSecret} className="font-mono" />
                    <Button
                      variant="neutral"
                      onClick={() => copyText(totpSecret, "Secret")}
                      title="Copy secret"
                    >
                      <Copy className="h-4 w-4" />
                    </Button>
                  </div>
                </div>
                <div>
                  <Label>otpauth URI</Label>
                  <Input readOnly value={totpUri} className="font-mono text-[11px]" />
                </div>
                <div>
                  <Label htmlFor="totp-code">Verification code</Label>
                  <Input
                    id="totp-code"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    maxLength={6}
                    value={totpCode}
                    onChange={(event) => setTotpCode(event.target.value.replace(/\D/g, ""))}
                  />
                </div>
                <div className="flex gap-2">
                  <Button
                    variant="primary"
                    disabled={totpCode.length !== 6 || confirmTotp.isPending}
                    onClick={confirmEnrollment}
                  >
                    Enable two-factor
                  </Button>
                  <Button
                    variant="neutral"
                    onClick={() => {
                      setTotpSecret("");
                      setTotpUri("");
                      setTotpCode("");
                    }}
                  >
                    Cancel
                  </Button>
                </div>
              </>
            ) : (
              <>
                <p className="text-[11px] text-black/50 dark:text-white/45">
                  Require a code from an authenticator app in addition to your password.
                </p>
                <Button
                  variant="neutral"
                  disabled={enroll.isPending}
                  onClick={startEnrollment}
                >
                  <KeyRound className="h-4 w-4" />
                  Set up two-factor authentication
                </Button>
              </>
            )}
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
              Issue a scoped token to automate deployments from a pipeline. Tokens are shown
              once and stored only as a hash.
            </p>
            <Button variant="neutral" onClick={createToken}>
              <Copy className="h-4 w-4" />
              Create token
            </Button>
            {issuedToken && (
              <div>
                <Label>New token (copy it now)</Label>
                <div className="flex gap-2">
                  <Input readOnly value={issuedToken} className="font-mono" />
                  <Button
                    variant="neutral"
                    onClick={() => copyText(issuedToken, "Token")}
                    title="Copy token"
                  >
                    <Copy className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}

export const Route = createFileRoute("/settings")({
  component: SettingsPage,
});
