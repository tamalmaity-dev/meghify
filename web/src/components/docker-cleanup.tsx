import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CalendarClock,
  CheckCircle2,
  Clock3,
  HardDrive,
  Play,
  RefreshCw,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";
import { api, formatBytes, formatDate } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Select } from "@/components/ui/input";
import type {
  DockerCleanupResult,
  DockerCleanupRun,
  DockerCleanupSettings,
} from "@/lib/types";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

const SCHEDULE_PRESETS = [
  { label: "Daily", value: "0 0 * * *" },
  { label: "Every 6 hours", value: "0 */6 * * *" },
  { label: "Weekly", value: "0 3 * * 0" },
];

function formatDuration(ms: number): string {
  if (!ms || ms < 1000) return `${Math.max(0, Math.round(ms))} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const totalSeconds = Math.round(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return seconds ? `${minutes}m ${seconds}s` : `${minutes}m`;
}

function runTone(status: DockerCleanupRun["status"]): {
  label: string;
  dot: string;
  text: string;
} {
  switch (status) {
    case "success":
      return { label: "Success", dot: "bg-ok", text: "text-ok" };
    case "failed":
      return { label: "Failed", dot: "bg-danger", text: "text-danger" };
    default:
      return { label: "Running", dot: "animate-pulse bg-warn", text: "text-warn" };
  }
}

export function DockerCleanupPanel() {
  const queryClient = useQueryClient();
  const [form, setForm] = useState<DockerCleanupSettings | null>(null);

  const settings = useQuery({
    queryKey: ["docker-cleanup", "settings"],
    queryFn: () => api.get<DockerCleanupSettings>("/docker/cleanup"),
  });

  const history = useQuery({
    queryKey: ["docker-cleanup", "history"],
    queryFn: () => api.get<DockerCleanupRun[]>("/docker/cleanup/history?limit=8"),
    refetchInterval: 15_000,
  });

  useEffect(() => {
    if (settings.data) setForm(settings.data);
  }, [settings.data]);

  const save = useMutation({
    mutationFn: (next: DockerCleanupSettings) =>
      api.put<DockerCleanupSettings>("/docker/cleanup", next),
    onSuccess: (saved) => {
      setForm(saved);
      queryClient.setQueryData(["docker-cleanup", "settings"], saved);
      toast.success("Cleanup schedule saved");
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const run = useMutation({
    mutationFn: () => api.post<DockerCleanupResult>("/docker/cleanup?applyOptions=1"),
    onSuccess: (result) => {
      toast.success(
        result.reclaimed > 0
          ? `Cleanup finished — reclaimed ${formatBytes(result.reclaimed)}`
          : "Cleanup finished — nothing to reclaim",
      );
      queryClient.invalidateQueries({ queryKey: ["docker-cleanup", "history"] });
      queryClient.invalidateQueries({ queryKey: ["docker-df"] });
    },
    onError: (error) => {
      toast.error(errorText(error));
      queryClient.invalidateQueries({ queryKey: ["docker-cleanup", "history"] });
    },
  });

  const lastRun = history.data?.[0];
  const lastSuccess = history.data?.find((item) => item.status === "success");
  const dirty = Boolean(
    form &&
      settings.data &&
      (form.enabled !== settings.data.enabled ||
        form.schedule !== settings.data.schedule ||
        form.trigger !== settings.data.trigger ||
        form.thresholdPercent !== settings.data.thresholdPercent ||
        form.unusedVolumes !== settings.data.unusedVolumes ||
        form.unusedNetworks !== settings.data.unusedNetworks ||
        form.applicationImages !== settings.data.applicationImages),
  );

  if (settings.isError) {
    return (
      <Card>
        <CardContent className="py-8 text-center text-[12px] text-danger">
          {errorText(settings.error)}
        </CardContent>
      </Card>
    );
  }

  if (!form) {
    return (
      <Card>
        <CardContent className="py-8 text-center text-[12px] text-black/45 dark:text-white/40">
          Loading cleanup settings…
        </CardContent>
      </Card>
    );
  }

  const update = <K extends keyof DockerCleanupSettings>(
    key: K,
    value: DockerCleanupSettings[K],
  ) => setForm((current) => (current ? { ...current, [key]: value } : current));

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-[16px] font-semibold tracking-tight">Docker cleanup</h2>
          <p className="mt-0.5 text-[12px] text-black/50 dark:text-white/45">
            Reclaim images, volumes, networks and build cache on this server.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {dirty && (
            <span className="text-[10.5px] text-warn">Save settings before running</span>
          )}
          <Button
            variant="ghost"
            size="icon"
            title="Refresh cleanup history"
            onClick={() => history.refetch()}
            disabled={history.isFetching}
          >
            <RefreshCw className={history.isFetching ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => {
              if (form.unusedVolumes || form.unusedNetworks) {
                const confirmed = window.confirm(
                  "Run Docker cleanup with unused volume/network removal enabled? This only touches panel-labelled resources, but unused volumes may still contain data.",
                );
                if (!confirmed) return;
              }
              run.mutate();
            }}
            disabled={run.isPending || dirty}
            title={dirty ? "Save the cleanup settings before running" : "Run cleanup now"}
          >
            {run.isPending ? (
              <RefreshCw className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Play className="h-3.5 w-3.5" />
            )}
            {run.isPending ? "Cleaning…" : "Run cleanup"}
          </Button>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        <Card>
          <CardContent className="flex items-start gap-2.5 py-3">
            <span className="mt-0.5 flex h-7 w-7 items-center justify-center rounded-md bg-white/[0.04] text-black/50 dark:text-white/40">
              <HardDrive className="h-3.5 w-3.5" />
            </span>
            <div>
              <div className="text-[10px] uppercase tracking-wide text-black/40 dark:text-white/35">
                Last reclaim
              </div>
              <div className="mt-0.5 text-[13px] font-medium">
                {lastSuccess ? formatBytes(lastSuccess.reclaimedBytes) : "—"}
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="flex items-start gap-2.5 py-3">
            <span className="mt-0.5 flex h-7 w-7 items-center justify-center rounded-md bg-white/[0.04] text-black/50 dark:text-white/40">
              <Clock3 className="h-3.5 w-3.5" />
            </span>
            <div>
              <div className="text-[10px] uppercase tracking-wide text-black/40 dark:text-white/35">
                Last run
              </div>
              <div className="mt-0.5 text-[13px] font-medium">
                {lastRun ? formatDate(lastRun.startedAt) : "Never"}
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="flex items-start gap-2.5 py-3">
            <span className="mt-0.5 flex h-7 w-7 items-center justify-center rounded-md bg-white/[0.04] text-black/50 dark:text-white/40">
              <CalendarClock className="h-3.5 w-3.5" />
            </span>
            <div>
              <div className="text-[10px] uppercase tracking-wide text-black/40 dark:text-white/35">
                Schedule
              </div>
              <div className="mt-0.5 truncate font-mono text-[12px] font-medium">
                {form.enabled ? form.schedule : "Disabled"}
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Scheduled maintenance</CardTitle>
            <p className="mt-0.5 text-[11px] text-black/45 dark:text-white/40">
              Cleanup runs automatically on the schedule below. Manual runs are always available.
            </p>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={form.enabled}
            aria-label="Enable scheduled cleanup"
            onClick={() => update("enabled", !form.enabled)}
            className={`relative h-4 w-7 shrink-0 rounded-full transition-colors ${
              form.enabled ? "bg-brand" : "bg-white/15"
            }`}
          >
            <span
              className={`absolute top-0.5 left-0.5 h-3 w-3 rounded-full bg-white transition-transform ${
                form.enabled ? "translate-x-3" : ""
              }`}
            />
          </button>
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_200px]">
          <div>
            <Label htmlFor="cleanup-schedule">Frequency (cron)</Label>
            <Input
              id="cleanup-schedule"
              value={form.schedule}
              onChange={(event) => update("schedule", event.target.value)}
              placeholder="0 0 * * *"
              className="font-mono"
              disabled={!form.enabled}
            />
            <div className="mt-2 flex flex-wrap gap-1.5">
              {SCHEDULE_PRESETS.map((preset) => (
                <button
                  key={preset.value}
                  type="button"
                  disabled={!form.enabled}
                  onClick={() => update("schedule", preset.value)}
                  className="rounded-full border border-line/60 px-2 py-0.5 text-[10.5px] text-black/50 transition-colors hover:bg-black/[0.04] hover:text-current disabled:opacity-40 dark:text-white/45 dark:hover:bg-white/[0.05]"
                >
                  {preset.label}
                </button>
              ))}
            </div>
            <p className="mt-1.5 text-[10.5px] text-black/40 dark:text-white/35">
              Five-field cron in the server&apos;s timezone. Default is daily at midnight.
            </p>
          </div>
          <div>
            <Label htmlFor="cleanup-trigger">Trigger</Label>
            <Select
              id="cleanup-trigger"
              value={form.trigger}
              onChange={(event) =>
                update("trigger", event.target.value as DockerCleanupSettings["trigger"])
              }
              disabled={!form.enabled}
            >
              <option value="schedule">Run on every schedule</option>
              <option value="threshold">Only above disk threshold</option>
            </Select>
            {form.trigger === "threshold" && (
              <div className="mt-2">
                <Label htmlFor="cleanup-threshold">Host disk threshold (%)</Label>
                <Input
                  id="cleanup-threshold"
                  type="number"
                  min={1}
                  max={99}
                  value={form.thresholdPercent}
                  onChange={(event) =>
                    update("thresholdPercent", Number(event.target.value) || 1)
                  }
                  disabled={!form.enabled}
                />
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Advanced cleanup</CardTitle>
          <span className="text-[10.5px] text-black/40 dark:text-white/35">
            Applied to schedules and the Run cleanup button
          </span>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-3">
            <div>
              <Label>Unused volumes</Label>
              <Select
                value={form.unusedVolumes ? "remove" : "keep"}
                onChange={(event) => update("unusedVolumes", event.target.value === "remove")}
              >
                <option value="keep">Keep volumes</option>
                <option value="remove">Remove unused</option>
              </Select>
            </div>
            <div>
              <Label>Unused networks</Label>
              <Select
                value={form.unusedNetworks ? "remove" : "keep"}
                onChange={(event) => update("unusedNetworks", event.target.value === "remove")}
              >
                <option value="keep">Keep networks</option>
                <option value="remove">Remove unused</option>
              </Select>
            </div>
            <div>
              <Label>Application images</Label>
              <Select
                value={form.applicationImages}
                onChange={(event) =>
                  update(
                    "applicationImages",
                    event.target.value as DockerCleanupSettings["applicationImages"],
                  )
                }
              >
                <option value="keep">Keep configured retention</option>
                <option value="remove">Remove retained images</option>
              </Select>
            </div>
          </div>
          {(form.unusedVolumes || form.unusedNetworks || form.applicationImages === "remove") && (
            <div className="flex items-start gap-2 rounded-md bg-warn/10 px-3 py-2 text-[11px] leading-relaxed text-warn">
              <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>
                {form.applicationImages === "remove" &&
                  "Retained application images are rollback points; removing them keeps only what current containers reference. "}
                {(form.unusedVolumes || form.unusedNetworks) &&
                  "Unused volumes may contain data you still need. Only panel-labelled volumes and networks are touched, and only volumes no container references are removed; unrelated Docker resources stay put."}
              </span>
            </div>
          )}
          <div className="flex items-center justify-between border-t border-line/40 pt-3">
            <p className="text-[10.5px] text-black/40 dark:text-white/35">
              Schedule changes take effect within a minute; no panel restart is needed.
            </p>
            <Button
              variant="primary"
              size="sm"
              onClick={() => save.mutate(form)}
              disabled={save.isPending}
            >
              {save.isPending ? "Saving…" : "Save cleanup settings"}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent executions</CardTitle>
          <span className="text-[10.5px] text-black/40 dark:text-white/35">
            Scheduled and manual runs
          </span>
        </CardHeader>
        <CardContent className="p-0">
          {history.isLoading ? (
            <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
              Loading history…
            </p>
          ) : !history.data?.length ? (
            <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
              No cleanup has run yet. Run one now or wait for the schedule.
            </p>
          ) : (
            <ul className="divide-y divide-line/40">
              {history.data.map((item) => {
                const tone = runTone(item.status);
                return (
                  <li key={item.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5">
                    <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${tone.dot}`} />
                    <span className={`text-[11.5px] font-medium ${tone.text}`}>{tone.label}</span>
                    <span className="text-[11.5px] text-black/55 dark:text-white/45">
                      {formatDate(item.startedAt)}
                    </span>
                    <span className="rounded-full bg-white/[0.04] px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-black/45 dark:text-white/35">
                      {item.trigger === "schedule" ? "scheduled" : "manual"}
                    </span>
                    <span className="ml-auto flex items-center gap-3 text-[10.5px] tabular-nums text-black/45 dark:text-white/35">
                      {item.status === "success" && item.reclaimedBytes > 0 && (
                        <span>{formatBytes(item.reclaimedBytes)} reclaimed</span>
                      )}
                      {item.status === "success" && item.imagesRemoved > 0 && (
                        <span>{item.imagesRemoved} images</span>
                      )}
                      {item.status === "success" && item.volumesRemoved > 0 && (
                        <span>{item.volumesRemoved} volumes</span>
                      )}
                      {item.status === "success" && item.networksRemoved > 0 && (
                        <span>{item.networksRemoved} networks</span>
                      )}
                      <span>
                        {item.status === "running" ? "in progress" : formatDuration(item.durationMs)}
                      </span>
                      {item.status === "success" ? (
                        <CheckCircle2 className="h-3.5 w-3.5 text-ok" />
                      ) : item.status === "failed" ? (
                        <XCircle className="h-3.5 w-3.5 text-danger" />
                      ) : (
                        <RefreshCw className="h-3.5 w-3.5 animate-spin text-warn" />
                      )}
                    </span>
                    {item.error && (
                      <p className="w-full pl-3 text-[10.5px] leading-relaxed text-danger/80">
                        {item.error}
                      </p>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
