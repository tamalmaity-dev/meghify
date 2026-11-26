import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  ArrowUpCircle,
  CheckCircle2,
  Copy,
  ExternalLink,
  RefreshCw,
} from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { UpdateStatus } from "@/lib/types";

async function copyCommand() {
  try {
    await navigator.clipboard.writeText("meghify update");
    toast.success("Update command copied");
  } catch {
    toast.error("Clipboard is unavailable. Run `meghify update` from a shell.");
  }
}

interface ApplyResponse {
  status: string;
  from: string;
  to: string;
  message: string;
}

/**
 * Shows whether a newer release exists and, for admins on a systemd install,
 * starts `meghify update` in a transient unit. The panel restarts under the new
 * binary; the card polls /api/version and reloads once the version changes.
 *
 * The server checks GitHub and caches the answer for an hour, so this card is
 * cheap to keep on the Settings page.
 */
export function UpdateCard({
  currentVersion,
  canUpdate = false,
}: {
  currentVersion?: string;
  canUpdate?: boolean;
}) {
  const [force, setForce] = useState(0);
  const [confirming, setConfirming] = useState(false);
  const [updatingTo, setUpdatingTo] = useState<string | null>(null);

  const update = useQuery({
    queryKey: ["update-check", force],
    queryFn: () =>
      api.get<UpdateStatus>(force > 0 ? "/update/check?force=1" : "/update/check"),
    staleTime: 30 * 60_000,
    refetchInterval: 6 * 60 * 60_000,
    retry: false,
  });

  const apply = useMutation({
    mutationFn: () => api.post<ApplyResponse>("/update/apply"),
    onSuccess: (data) => {
      setConfirming(false);
      setUpdatingTo(data.to);
      toast.success(data.message);
    },
    onError: (error) => {
      if (error instanceof ApiError) {
        toast.error(error.message);
        return;
      }
      // The panel can restart before the response reaches the browser. Treat a
      // transport failure as "update likely started" and let polling decide.
      setConfirming(false);
      setUpdatingTo(update.data?.latest ?? "the new release");
    },
  });

  const versionPoll = useQuery({
    queryKey: ["panel-version", updatingTo],
    queryFn: () => api.get<{ version: string }>("/version"),
    enabled: updatingTo !== null,
    refetchInterval: 3_000,
    retry: 1,
  });

  const status = update.data;
  const current = status?.current ?? currentVersion ?? "dev";
  const dev = current === "" || current === "dev" || current === "unknown";
  const available = status?.available === true;
  const canApply = canUpdate && status?.canApply === true;

  useEffect(() => {
    if (!updatingTo) return;
    const next = versionPoll.data?.version;
    if (next && next !== current) {
      toast.success(`Updated to ${next}`);
      setUpdatingTo(null);
      window.location.reload();
    }
  }, [updatingTo, versionPoll.data, current]);

  useEffect(() => {
    if (!updatingTo) return;
    const timer = window.setTimeout(() => {
      setUpdatingTo(null);
      toast.error(
        "The panel did not come back with a new version. Check `journalctl -u meghify-update`.",
      );
    }, 10 * 60_000);
    return () => window.clearTimeout(timer);
  }, [updatingTo]);

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <CardTitle>Updates</CardTitle>
          {available && !updatingTo && (
            <span className="rounded-full bg-brand/10 px-2 py-0.5 text-[10px] font-medium text-brand">
              new release
            </span>
          )}
        </div>
        <Button
          variant="ghost"
          size="icon"
          title="Check for updates"
          onClick={() => setForce((n) => n + 1)}
          disabled={update.isFetching || updatingTo !== null}
        >
          <RefreshCw
            className={update.isFetching ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"}
          />
        </Button>
      </CardHeader>
      <CardContent className="space-y-2 text-[12px]">
        <div className="flex items-baseline justify-between gap-4">
          <span className="text-black/45 dark:text-white/40">Installed</span>
          <span className="font-mono">{current || "dev"}</span>
        </div>

        {status?.latest && (
          <div className="flex items-baseline justify-between gap-4">
            <span className="text-black/45 dark:text-white/40">Latest release</span>
            <span className={available ? "font-mono text-brand" : "font-mono"}>
              {status.latest}
            </span>
          </div>
        )}

        {available ? (
          <div className="space-y-2 rounded-md bg-brand/5 px-3 py-2 text-[11px] leading-relaxed text-brand">
            <p>
              <ArrowUpCircle className="mr-1 inline h-3.5 w-3.5" />
              A newer release is available.
            </p>

            {updatingTo ? (
              <p className="flex items-center gap-1.5">
                <RefreshCw className="h-3.5 w-3.5 animate-spin" />
                Updating to {updatingTo}… the panel restarts and this page reloads.
              </p>
            ) : canApply ? (
              confirming ? (
                <div className="space-y-2">
                  <p>
                    Update to <span className="font-mono">{status.latest}</span> now? The panel
                    restarts when the new binary is in place.
                  </p>
                  <div className="flex gap-2">
                    <Button
                      variant="primary"
                      size="sm"
                      onClick={() => apply.mutate()}
                      disabled={apply.isPending}
                    >
                      {apply.isPending ? "Starting…" : "Update now"}
                    </Button>
                    <Button
                      variant="neutral"
                      size="sm"
                      onClick={() => setConfirming(false)}
                      disabled={apply.isPending}
                    >
                      Cancel
                    </Button>
                  </div>
                </div>
              ) : (
                <Button variant="primary" size="sm" onClick={() => setConfirming(true)}>
                  Update now
                </Button>
              )
            ) : (
              <p>
                {canUpdate
                  ? status.applyBlockedReason ?? "This install cannot update from the panel."
                  : "Ask an administrator to apply the update."}
              </p>
            )}

            <div className="flex items-center gap-2">
              <code className="rounded bg-black/20 px-2 py-1 font-mono text-[11px]">
                meghify update
              </code>
              <Button
                variant="ghost"
                size="icon"
                title="Copy update command"
                onClick={copyCommand}
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
            </div>
            {status.releaseUrl && (
              <a
                href={status.releaseUrl}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 hover:underline"
              >
                Release notes <ExternalLink className="h-3 w-3" />
              </a>
            )}
          </div>
        ) : status?.error ? (
          <p className="text-[11px] text-black/45 dark:text-white/40">
            Could not check for updates: {status.error}
          </p>
        ) : update.isLoading ? (
          <p className="text-[11px] text-black/45 dark:text-white/40">Checking for updates…</p>
        ) : update.isError ? (
          <p className="text-[11px] text-black/45 dark:text-white/40">
            Could not check for updates. Try again in a moment.
          </p>
        ) : dev ? (
          <p className="text-[11px] text-black/45 dark:text-white/40">
            Development build — update checks are informational only.
          </p>
        ) : (
          <div className="flex items-center gap-1.5 text-[11px] text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 className="h-3.5 w-3.5" />
            You are on the latest release.
          </div>
        )}

        {status?.checkedAt && (
          <p className="pt-1 text-[10px] text-black/35 dark:text-white/30">
            Checked {new Date(status.checkedAt).toLocaleString()}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
