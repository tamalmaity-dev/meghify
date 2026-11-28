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
            <span className="rounded-full border border-brand/25 bg-brand/12 px-2 py-0.5 text-[10px] font-medium text-brand">
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
      <CardContent className="space-y-2.5 text-[12px]">
        <div className="flex items-baseline justify-between gap-4">
          <span className="text-muted">Installed</span>
          <span className="font-mono text-ink">{current || "dev"}</span>
        </div>

        {status?.latest && (
          <div className="flex items-baseline justify-between gap-4">
            <span className="text-muted">Latest release</span>
            <span className={available ? "font-mono font-medium text-brand" : "font-mono text-ink"}>
              {status.latest}
            </span>
          </div>
        )}

        {available ? (
          <div className="space-y-2 rounded-lg border border-brand/20 bg-brand/8 px-3 py-2.5 text-[11px] leading-relaxed text-ink">
            <p className="font-medium text-brand">
              <ArrowUpCircle className="mr-1 inline h-3.5 w-3.5" />
              A newer release is available.
            </p>

            {updatingTo ? (
              <p className="flex items-center gap-1.5 text-muted">
                <RefreshCw className="h-3.5 w-3.5 animate-spin text-brand" />
                Updating to {updatingTo}… the panel restarts and this page reloads.
              </p>
            ) : canApply ? (
              confirming ? (
                <div className="space-y-2">
                  <p className="text-muted">
                    Update to <span className="font-mono text-ink">{status.latest}</span> now? The
                    panel restarts when the new binary is in place.
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
              <p className="text-muted">
                {canUpdate
                  ? status.applyBlockedReason ?? "This install cannot update from the panel."
                  : "Ask an administrator to apply the update."}
              </p>
            )}

            <div className="flex items-center gap-2">
              <code className="rounded border border-line/70 bg-recessed px-2 py-1 font-mono text-[11px] text-ink">
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
                className="inline-flex items-center gap-1 font-medium text-brand hover:underline"
              >
                Release notes <ExternalLink className="h-3 w-3" />
              </a>
            )}
          </div>
        ) : status?.error ? (
          <p className="text-[11px] text-muted">Could not check for updates: {status.error}</p>
        ) : update.isLoading ? (
          <p className="text-[11px] text-muted">Checking for updates…</p>
        ) : update.isError ? (
          <p className="text-[11px] text-muted">
            Could not check for updates. Try again in a moment.
          </p>
        ) : dev ? (
          <p className="text-[11px] text-muted">
            Development build — update checks are informational only.
          </p>
        ) : (
          <div className="flex items-center gap-1.5 text-[11px] font-medium text-ok">
            <CheckCircle2 className="h-3.5 w-3.5" />
            You are on the latest release.
          </div>
        )}

        {status?.checkedAt && (
          <p className="pt-1 text-[10px] text-faint">
            Checked {new Date(status.checkedAt).toLocaleString()}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
