import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  CheckCircle2,
  Cpu,
  MemoryStick,
  RefreshCw,
  ShieldCheck,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";
import { api, formatBytes, formatDate } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label, Select, Switch } from "@/components/ui/input";
import type {
  Application,
  ResourceGuardAction,
  ResourceGuardEvent,
  ResourceGuardSettings,
} from "@/lib/types";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));
const minutes = (seconds: number) => Math.max(0, Math.round(seconds / 60));

function metricLabel(metric: ResourceGuardEvent["metric"]): string {
  switch (metric) {
    case "cpu":
      return "CPU";
    case "host_memory":
      return "Host memory";
    default:
      return "Memory";
  }
}

function actionLabel(action: ResourceGuardAction): string {
  switch (action) {
    case "throttle":
      return "Throttled";
    case "stop":
      return "Stopped";
    default:
      return "Notified";
  }
}

function eventTone(event: ResourceGuardEvent) {
  if (event.status === "failed") return "text-danger";
  if (event.action === "stop") return "text-warn";
  if (event.action === "throttle") return "text-brand";
  return "text-ok";
}

function EventRow({ event }: { event: ResourceGuardEvent }) {
  return (
    <li className="flex flex-wrap items-start gap-x-3 gap-y-1 px-4 py-2.5">
      <span className={`mt-1 h-1.5 w-1.5 shrink-0 rounded-full ${
        event.status === "failed" ? "bg-danger" : event.action === "stop" ? "bg-warn" : "bg-brand"
      }`} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span className="text-[12px] font-medium">{event.applicationName || "application"}</span>
          <span className="rounded-full bg-white/[0.04] px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-black/45 dark:text-white/35">
            {metricLabel(event.metric)}
          </span>
          <span className={`text-[11px] font-medium ${eventTone(event)}`}>
            {actionLabel(event.action)}
          </span>
          <span className="text-[10.5px] tabular-nums text-black/40 dark:text-white/35">
            {event.observedPercent.toFixed(1)}% / {event.thresholdPercent}%
          </span>
        </div>
        <p className="mt-0.5 text-[11px] leading-relaxed text-black/55 dark:text-white/45">
          {event.message}
        </p>
        {event.error && <p className="mt-0.5 text-[10.5px] text-danger">{event.error}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-2 text-[10.5px] text-black/40 dark:text-white/35">
        {event.usageBytes ? <span>{formatBytes(event.usageBytes)}</span> : null}
        <span>{formatDate(event.createdAt)}</span>
        {event.status === "success" ? (
          <CheckCircle2 className="h-3.5 w-3.5 text-ok" />
        ) : (
          <XCircle className="h-3.5 w-3.5 text-danger" />
        )}
      </div>
    </li>
  );
}

/**
 * Panel-wide runaway-container protection. The policy applies to every
 * managed server; per-application overrides live on the application's Runtime
 * tab.
 */
export function ResourceGuardPanel() {
  const queryClient = useQueryClient();
  const [form, setForm] = useState<ResourceGuardSettings | null>(null);

  const settings = useQuery({
    queryKey: ["resource-guard", "settings"],
    queryFn: () => api.get<ResourceGuardSettings>("/resource-guard/settings"),
  });
  const events = useQuery({
    queryKey: ["resource-guard", "events"],
    queryFn: () => api.get<ResourceGuardEvent[]>("/resource-guard/events?limit=10"),
    refetchInterval: 15_000,
  });

  useEffect(() => {
    if (settings.data) setForm(settings.data);
  }, [settings.data]);

  const save = useMutation({
    mutationFn: (next: ResourceGuardSettings) =>
      api.put<ResourceGuardSettings>("/resource-guard/settings", next),
    onSuccess: (saved) => {
      setForm(saved);
      queryClient.setQueryData(["resource-guard", "settings"], saved);
      toast.success("Resource protection saved");
    },
    onError: (error) => toast.error(errorText(error)),
  });

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
          Loading resource protection…
        </CardContent>
      </Card>
    );
  }

  const update = <K extends keyof ResourceGuardSettings>(key: K, value: ResourceGuardSettings[K]) =>
    setForm((current) => (current ? { ...current, [key]: value } : current));

  const original = settings.data;
  const dirty = Boolean(
    original &&
      (form.enabled !== original.enabled ||
        form.cpuEnabled !== original.cpuEnabled ||
        form.cpuPercent !== original.cpuPercent ||
        form.cpuDurationSeconds !== original.cpuDurationSeconds ||
        form.cpuAction !== original.cpuAction ||
        form.cpuThrottlePercent !== original.cpuThrottlePercent ||
        form.memoryEnabled !== original.memoryEnabled ||
        form.memoryPercent !== original.memoryPercent ||
        form.memoryDurationSeconds !== original.memoryDurationSeconds ||
        form.memoryAction !== original.memoryAction ||
        form.hostMemoryEnabled !== original.hostMemoryEnabled ||
        form.hostMemoryPercent !== original.hostMemoryPercent ||
        form.hostMemoryDurationSeconds !== original.hostMemoryDurationSeconds ||
        form.graceSeconds !== original.graceSeconds ||
        form.cooldownSeconds !== original.cooldownSeconds),
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-[16px] font-semibold tracking-tight">Resource protection</h2>
          <p className="mt-0.5 text-[12px] text-black/50 dark:text-white/45">
            Watch managed application containers on every server and act on sustained CPU or memory pressure.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10.5px] font-medium ${
            form.enabled ? "bg-ok/10 text-ok" : "bg-white/[0.05] text-black/45 dark:text-white/35"
          }`}>
            <span className={`h-1.5 w-1.5 rounded-full ${form.enabled ? "bg-ok" : "bg-white/30"}`} />
            {form.enabled ? "Guard active" : "Guard disabled"}
          </span>
          <Button
            variant="ghost"
            size="icon"
            title="Refresh recent actions"
            onClick={() => events.refetch()}
            disabled={events.isFetching}
          >
            <RefreshCw className={events.isFetching ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
          </Button>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        <Card>
          <CardContent className="flex items-start gap-2.5 py-3">
            <span className="mt-0.5 flex h-7 w-7 items-center justify-center rounded-md bg-white/[0.04] text-black/50 dark:text-white/40">
              <ShieldCheck className="h-3.5 w-3.5" />
            </span>
            <div>
              <div className="text-[10px] uppercase tracking-wide text-black/40 dark:text-white/35">Watch scope</div>
              <div className="mt-0.5 text-[13px] font-medium">All servers</div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="flex items-start gap-2.5 py-3">
            <span className="mt-0.5 flex h-7 w-7 items-center justify-center rounded-md bg-white/[0.04] text-black/50 dark:text-white/40">
              <Cpu className="h-3.5 w-3.5" />
            </span>
            <div>
              <div className="text-[10px] uppercase tracking-wide text-black/40 dark:text-white/35">CPU default</div>
              <div className="mt-0.5 text-[13px] font-medium capitalize">
                {form.cpuEnabled ? form.cpuAction : "off"} after {minutes(form.cpuDurationSeconds)}m
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="flex items-start gap-2.5 py-3">
            <span className="mt-0.5 flex h-7 w-7 items-center justify-center rounded-md bg-white/[0.04] text-black/50 dark:text-white/40">
              <MemoryStick className="h-3.5 w-3.5" />
            </span>
            <div>
              <div className="text-[10px] uppercase tracking-wide text-black/40 dark:text-white/35">Memory default</div>
              <div className="mt-0.5 text-[13px] font-medium capitalize">
                {form.memoryEnabled ? form.memoryAction : "off"} after {minutes(form.memoryDurationSeconds)}m
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Container thresholds</CardTitle>
            <p className="mt-0.5 text-[11px] text-black/45 dark:text-white/40">
              A condition must persist for the whole duration before the guard acts. Short spikes are ignored.
            </p>
          </div>
          <Switch
            checked={form.enabled}
            aria-label="Enable resource protection"
            onChange={() => update("enabled", !form.enabled)}
          />
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-3 rounded-md bg-white/[0.02] p-3 sm:grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)] sm:items-end">
            <div className="flex items-center gap-2 pb-1.5">
              <Cpu className="h-4 w-4 text-black/45 dark:text-white/40" />
              <span className="text-[12px] font-medium">CPU</span>
              <Switch
                checked={form.cpuEnabled}
                aria-label="Enable CPU protection"
                onChange={() => update("cpuEnabled", !form.cpuEnabled)}
              />
            </div>
            <div>
              <Label htmlFor="guard-cpu-percent">Host CPU threshold (%)</Label>
              <Input
                id="guard-cpu-percent"
                type="number"
                min={1}
                max={100}
                value={form.cpuPercent}
                disabled={!form.cpuEnabled}
                onChange={(event) => update("cpuPercent", Number(event.target.value) || 1)}
              />
            </div>
            <div>
              <Label htmlFor="guard-cpu-duration">Sustained for (minutes)</Label>
              <Input
                id="guard-cpu-duration"
                type="number"
                min={1}
                value={minutes(form.cpuDurationSeconds)}
                disabled={!form.cpuEnabled}
                onChange={(event) => update("cpuDurationSeconds", Math.max(1, Number(event.target.value) || 1) * 60)}
              />
            </div>
            <div>
              <Label htmlFor="guard-cpu-action">Action</Label>
              <Select
                id="guard-cpu-action"
                value={form.cpuAction}
                disabled={!form.cpuEnabled}
                onChange={(event) => update("cpuAction", event.target.value as ResourceGuardSettings["cpuAction"])}
              >
                <option value="notify">Notify only</option>
                <option value="throttle">Throttle and notify</option>
                <option value="stop">Stop container</option>
              </Select>
            </div>
          </div>
          {form.cpuAction === "throttle" && (
            <div className="ml-0 max-w-xs">
              <Label htmlFor="guard-cpu-throttle">Throttle to (% of host CPU)</Label>
              <Input
                id="guard-cpu-throttle"
                type="number"
                min={1}
                max={99}
                value={form.cpuThrottlePercent}
                disabled={!form.cpuEnabled}
                onChange={(event) => update("cpuThrottlePercent", Number(event.target.value) || 1)}
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
                The container keeps running with a temporary CPU cap until its next deployment.
              </p>
            </div>
          )}

          <div className="grid gap-3 rounded-md bg-white/[0.02] p-3 sm:grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)] sm:items-end">
            <div className="flex items-center gap-2 pb-1.5">
              <MemoryStick className="h-4 w-4 text-black/45 dark:text-white/40" />
              <span className="text-[12px] font-medium">Memory</span>
              <Switch
                checked={form.memoryEnabled}
                aria-label="Enable memory protection"
                onChange={() => update("memoryEnabled", !form.memoryEnabled)}
              />
            </div>
            <div>
              <Label htmlFor="guard-memory-percent">Limit threshold (%)</Label>
              <Input
                id="guard-memory-percent"
                type="number"
                min={1}
                max={100}
                value={form.memoryPercent}
                disabled={!form.memoryEnabled}
                onChange={(event) => update("memoryPercent", Number(event.target.value) || 1)}
              />
              <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
                Measured against the container limit, or host RAM when no limit is set.
              </p>
            </div>
            <div>
              <Label htmlFor="guard-memory-duration">Sustained for (minutes)</Label>
              <Input
                id="guard-memory-duration"
                type="number"
                min={1}
                value={minutes(form.memoryDurationSeconds)}
                disabled={!form.memoryEnabled}
                onChange={(event) => update("memoryDurationSeconds", Math.max(1, Number(event.target.value) || 1) * 60)}
              />
            </div>
            <div>
              <Label htmlFor="guard-memory-action">Action</Label>
              <Select
                id="guard-memory-action"
                value={form.memoryAction}
                disabled={!form.memoryEnabled}
                onChange={(event) => update("memoryAction", event.target.value as ResourceGuardSettings["memoryAction"])}
              >
                <option value="notify">Notify only</option>
                <option value="stop">Stop container</option>
              </Select>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Host memory emergency</CardTitle>
            <p className="mt-0.5 text-[11px] text-black/45 dark:text-white/40">
              When the whole machine is nearly out of RAM, stop the largest protected application on that server.
            </p>
          </div>
          <Switch
            checked={form.hostMemoryEnabled}
            aria-label="Enable host memory emergency protection"
            onChange={() => update("hostMemoryEnabled", !form.hostMemoryEnabled)}
          />
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <div>
            <Label htmlFor="guard-host-percent">Host memory threshold (%)</Label>
            <Input
              id="guard-host-percent"
              type="number"
              min={1}
              max={100}
              value={form.hostMemoryPercent}
              disabled={!form.hostMemoryEnabled}
              onChange={(event) => update("hostMemoryPercent", Number(event.target.value) || 1)}
            />
          </div>
          <div>
            <Label htmlFor="guard-host-duration">Sustained for (minutes)</Label>
            <Input
              id="guard-host-duration"
              type="number"
              min={1}
              value={minutes(form.hostMemoryDurationSeconds)}
              disabled={!form.hostMemoryEnabled}
              onChange={(event) => update("hostMemoryDurationSeconds", Math.max(1, Number(event.target.value) || 1) * 60)}
            />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Safety windows</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <div>
            <Label htmlFor="guard-grace">Ignore new containers for (minutes)</Label>
            <Input
              id="guard-grace"
              type="number"
              min={0}
              value={minutes(form.graceSeconds)}
              onChange={(event) => update("graceSeconds", Math.max(0, Number(event.target.value) || 0) * 60)}
            />
            <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
              Deployments and cold starts get time to settle.
            </p>
          </div>
          <div>
            <Label htmlFor="guard-cooldown">Cooldown after an action (minutes)</Label>
            <Input
              id="guard-cooldown"
              type="number"
              min={0}
              value={minutes(form.cooldownSeconds)}
              onChange={(event) => update("cooldownSeconds", Math.max(0, Number(event.target.value) || 0) * 60)}
            />
            <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
              Prevents repeated actions while an application or host recovers.
            </p>
          </div>
        </CardContent>
      </Card>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-[10.5px] text-black/40 dark:text-white/35">
          The guard samples every 30 seconds. It never targets the panel, proxy, build sandbox, databases or services.
          Compose stacks are managed as a stack and are not individually guarded yet.
        </p>
        <div className="flex items-center gap-2">
          {dirty && <span className="text-[10.5px] text-warn">Unsaved changes</span>}
          <Button
            variant="primary"
            size="sm"
            onClick={() => save.mutate(form)}
            disabled={save.isPending || !dirty}
          >
            {save.isPending ? "Saving…" : "Save protection"}
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Recent actions</CardTitle>
          <Activity className="h-3.5 w-3.5 text-black/35 dark:text-white/30" />
        </CardHeader>
        <CardContent className="p-0">
          {events.isLoading ? (
            <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">Loading actions…</p>
          ) : !events.data?.length ? (
            <p className="px-4 py-6 text-center text-[12px] text-black/45 dark:text-white/40">
              No guard action has been recorded yet.
            </p>
          ) : (
            <ul className="divide-y divide-line/40">
              {events.data.map((event) => (
                <EventRow key={event.id} event={event} />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

/**
 * Per-application overrides, rendered under the Runtime limits. Empty action
 * values inherit the panel policy; a zero threshold also inherits.
 */
export function ApplicationResourceGuardCard({
  app,
  applicationId,
}: {
  app: Application;
  applicationId: string;
}) {
  const queryClient = useQueryClient();
  const global = useQuery({
    queryKey: ["resource-guard", "settings"],
    queryFn: () => api.get<ResourceGuardSettings>("/resource-guard/settings"),
  });
  const events = useQuery({
    queryKey: ["resource-guard", "events", applicationId],
    queryFn: () => api.get<ResourceGuardEvent[]>(
      `/resource-guard/events?applicationId=${encodeURIComponent(applicationId)}&limit=3`,
    ),
    refetchInterval: 20_000,
  });
  const [enabled, setEnabled] = useState(app.resourceGuardEnabled);
  const [cpuAction, setCpuAction] = useState(app.resourceGuardCpuAction);
  const [memoryAction, setMemoryAction] = useState(app.resourceGuardMemoryAction);
  const [cpuPercent, setCpuPercent] = useState(app.resourceGuardCpuPercent);
  const [memoryPercent, setMemoryPercent] = useState(app.resourceGuardMemoryPercent);

  useEffect(() => {
    setEnabled(app.resourceGuardEnabled);
    setCpuAction(app.resourceGuardCpuAction);
    setMemoryAction(app.resourceGuardMemoryAction);
    setCpuPercent(app.resourceGuardCpuPercent);
    setMemoryPercent(app.resourceGuardMemoryPercent);
  }, [
    app.id,
    app.resourceGuardEnabled,
    app.resourceGuardCpuAction,
    app.resourceGuardMemoryAction,
    app.resourceGuardCpuPercent,
    app.resourceGuardMemoryPercent,
  ]);

  const save = useMutation({
    mutationFn: () =>
      api.patch<Application>(`/applications/${applicationId}`, {
        resourceGuardEnabled: enabled,
        resourceGuardCpuAction: cpuAction,
        resourceGuardMemoryAction: memoryAction,
        resourceGuardCpuPercent: cpuPercent,
        resourceGuardMemoryPercent: memoryPercent,
      }),
    onSuccess: async () => {
      toast.success("Application protection saved");
      await queryClient.invalidateQueries({ queryKey: ["applications", applicationId] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const effectiveCpu = cpuPercent > 0 ? cpuPercent : global.data?.cpuPercent ?? 90;
  const effectiveMemory = memoryPercent > 0 ? memoryPercent : global.data?.memoryPercent ?? 90;
  const effectiveCpuAction = cpuAction || global.data?.cpuAction || "throttle";
  const effectiveMemoryAction = memoryAction || global.data?.memoryAction || "stop";
  const dirty =
    enabled !== app.resourceGuardEnabled ||
    cpuAction !== app.resourceGuardCpuAction ||
    memoryAction !== app.resourceGuardMemoryAction ||
    cpuPercent !== app.resourceGuardCpuPercent ||
    memoryPercent !== app.resourceGuardMemoryPercent;

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Resource protection</CardTitle>
          <p className="mt-0.5 text-[11px] text-black/45 dark:text-white/40">
            {enabled
              ? `Effective policy: CPU ${effectiveCpuAction} above ${effectiveCpu}%; memory ${effectiveMemoryAction} above ${effectiveMemory}%.`
              : "The panel-wide guard will not act on this application."}
          </p>
        </div>
        <Switch
          checked={enabled}
          aria-label="Enable protection for this application"
          onChange={() => setEnabled(!enabled)}
        />
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <Label htmlFor="app-guard-cpu-percent">CPU threshold override (%)</Label>
            <Input
              id="app-guard-cpu-percent"
              type="number"
              min={0}
              max={100}
              value={cpuPercent || ""}
              disabled={!enabled}
              placeholder={`inherit ${global.data?.cpuPercent ?? 90}`}
              onChange={(event) => setCpuPercent(Math.max(0, Math.min(100, Number(event.target.value) || 0)))}
            />
            <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
              Percentage of total host CPU. Leave empty to inherit.
            </p>
          </div>
          <div>
            <Label htmlFor="app-guard-memory-percent">Memory threshold override (%)</Label>
            <Input
              id="app-guard-memory-percent"
              type="number"
              min={0}
              max={100}
              value={memoryPercent || ""}
              disabled={!enabled}
              placeholder={`inherit ${global.data?.memoryPercent ?? 90}`}
              onChange={(event) => setMemoryPercent(Math.max(0, Math.min(100, Number(event.target.value) || 0)))}
            />
            <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
              Percentage of the configured memory limit, or host RAM if unlimited.
            </p>
          </div>
          <div>
            <Label htmlFor="app-guard-cpu-action">CPU action</Label>
            <Select
              id="app-guard-cpu-action"
              value={cpuAction}
              disabled={!enabled}
              onChange={(event) => setCpuAction(event.target.value as typeof cpuAction)}
            >
              <option value="">Inherit panel policy</option>
              <option value="notify">Notify only</option>
              <option value="throttle">Throttle and notify</option>
              <option value="stop">Stop container</option>
            </Select>
          </div>
          <div>
            <Label htmlFor="app-guard-memory-action">Memory action</Label>
            <Select
              id="app-guard-memory-action"
              value={memoryAction}
              disabled={!enabled}
              onChange={(event) => setMemoryAction(event.target.value as typeof memoryAction)}
            >
              <option value="">Inherit panel policy</option>
              <option value="notify">Notify only</option>
              <option value="stop">Stop container</option>
            </Select>
          </div>
        </div>

        {enabled && effectiveCpuAction === "throttle" && (
          <div className="flex items-start gap-2 rounded-md bg-brand/5 px-3 py-2 text-[11px] leading-relaxed text-brand">
            <Activity className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>
              Sustained CPU will be capped to {global.data?.cpuThrottlePercent ?? 50}% of host CPU. The saved CPU limit is restored on the next deployment.
            </span>
          </div>
        )}
        {enabled && effectiveMemoryAction === "stop" && (
          <div className="flex items-start gap-2 rounded-md bg-warn/10 px-3 py-2 text-[11px] leading-relaxed text-warn">
            <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>
              Sustained memory pressure stops the container. The panel does not restart it automatically.
            </span>
          </div>
        )}

        {events.data?.length ? (
          <div className="border-t border-line/40 pt-3">
            <div className="mb-1 text-[10px] uppercase tracking-wide text-black/40 dark:text-white/35">
              Recent protection actions
            </div>
            <ul className="space-y-1.5">
              {events.data.map((event) => (
                <li key={event.id} className="flex items-start gap-2 text-[11px]">
                  <span className={`mt-1 h-1.5 w-1.5 shrink-0 rounded-full ${
                    event.status === "failed" ? "bg-danger" : event.action === "stop" ? "bg-warn" : "bg-brand"
                  }`} />
                  <span className="min-w-0 flex-1 text-black/55 dark:text-white/45">{event.message}</span>
                  <span className="shrink-0 text-[10px] text-black/35 dark:text-white/30">
                    {formatDate(event.createdAt)}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        <div className="flex items-center justify-between border-t border-line/40 pt-3">
          <p className="text-[10.5px] text-black/40 dark:text-white/35">
            CPU and memory ceilings are set in the card above.
          </p>
          <Button variant="primary" size="sm" onClick={() => save.mutate()} disabled={save.isPending || !dirty}>
            {save.isPending ? "Saving…" : "Save protection"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
