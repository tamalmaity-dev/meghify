import type { CSSProperties, ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Cpu, HardDrive, MemoryStick, Network } from "lucide-react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api, formatBytes } from "@/lib/api";
import { useTheme } from "@/lib/theme";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { NodeMetrics } from "@/lib/types";

const REFETCH_MS = 15_000;

interface SeriesPoint {
  t: number;
  cpu: number;
  memory: number;
  rx: number;
  tx: number;
}

/**
 * Folds raw samples into chart points. The network counters are cumulative
 * odometers, so consecutive samples become per-second rates.
 */
function buildSeries(rows: NodeMetrics[]): SeriesPoint[] {
  return rows.map((row, index) => {
    const previous = index > 0 ? rows[index - 1] : undefined;
    let rx = 0;
    let tx = 0;
    if (previous) {
      const seconds = (Date.parse(row.recordedAt) - Date.parse(previous.recordedAt)) / 1000;
      if (seconds > 0) {
        rx = Math.max(0, (row.netRx - previous.netRx) / seconds);
        tx = Math.max(0, (row.netTx - previous.netTx) / seconds);
      }
    }
    return {
      t: Date.parse(row.recordedAt),
      cpu: row.cpuPercent,
      memory: row.memTotal > 0 ? (row.memUsed / row.memTotal) * 100 : 0,
      rx,
      tx,
    };
  });
}

const clock = (t: number) =>
  new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

function usageTone(percent: number): string {
  if (percent >= 92) return "bg-danger";
  if (percent >= 80) return "bg-warn";
  return "bg-brand-solid";
}

/**
 * The machine section of the dashboard: live CPU, memory, storage and network
 * for the server the panel runs on, with an hour of history behind each chart.
 * Pass a nodeId to render the same charts for another server.
 */
export function SystemMetrics({ nodeId = "local" }: { nodeId?: string }) {
  const { resolvedTheme } = useTheme();
  const isLight = resolvedTheme === "light";

  const cpuColor = isLight ? "#7c3aed" : "#a78bfa";
  const memoryColor = isLight ? "#059669" : "#34d399";
  const rxColor = isLight ? "#0284c7" : "#38bdf8";
  const txColor = isLight ? "#d97706" : "#fbbf24";

  const tooltipStyle: CSSProperties = isLight
    ? {
        background: "rgba(255, 255, 255, 0.96)",
        border: "1px solid rgba(15, 23, 42, 0.12)",
        borderRadius: 10,
        color: "#18181b",
        fontSize: 12,
        padding: "6px 10px",
        boxShadow: "0 8px 20px rgba(15, 23, 42, 0.1)",
      }
    : {
        background: "rgba(24, 24, 30, 0.96)",
        border: "1px solid rgba(255, 255, 255, 0.12)",
        borderRadius: 10,
        color: "#f4f4f5",
        fontSize: 12,
        padding: "6px 10px",
        boxShadow: "0 8px 20px rgba(0, 0, 0, 0.4)",
      };

  const axisTick = {
    fontSize: 10,
    fill: isLight ? "rgba(71, 85, 105, 0.85)" : "rgba(161, 161, 170, 0.85)",
  };
  const gridStroke = isLight ? "rgba(15, 23, 42, 0.08)" : "rgba(255, 255, 255, 0.08)";

  const metrics = useQuery({
    queryKey: ["node-metrics", nodeId],
    queryFn: () => api.get<NodeMetrics[]>(`/nodes/${nodeId}/metrics?since=1h`),
    refetchInterval: REFETCH_MS,
  });

  const rows = metrics.data ?? [];
  if (rows.length === 0) {
    return (
      <Card className="mb-4">
        <CardContent className="py-6 text-center text-[12px] text-muted">
          {metrics.isError
            ? "Metrics are unavailable right now — the panel retries every 15 seconds."
            : "Collecting the first sample — machine metrics appear within 15 seconds."}
        </CardContent>
      </Card>
    );
  }

  const latest = rows[rows.length - 1];
  const series = buildSeries(rows);
  const last = series[series.length - 1];

  const memPercent = latest.memTotal > 0 ? (latest.memUsed / latest.memTotal) * 100 : 0;
  const diskPercent = latest.diskTotal > 0 ? (latest.diskUsed / latest.diskTotal) * 100 : 0;

  return (
    <section className="mb-5">
      <div className="mb-2.5 flex items-baseline justify-between">
        <h2 className="text-[13px] font-semibold tracking-tight text-ink">Machine</h2>
        <span className="text-[11px] text-faint">
          sampled at {clock(last.t)} · refreshes every 15s
        </span>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <MetricTile
          icon={<Cpu className="h-4 w-4" />}
          label="CPU"
          value={`${latest.cpuPercent.toFixed(1)}%`}
          detail="CPU time in use"
          bar={latest.cpuPercent}
          barTone={usageTone(latest.cpuPercent)}
        />
        <MetricTile
          icon={<MemoryStick className="h-4 w-4" />}
          label="Memory"
          value={formatBytes(latest.memUsed)}
          detail={`of ${formatBytes(latest.memTotal)} · ${memPercent.toFixed(0)}% in use`}
          bar={memPercent}
          barTone={usageTone(memPercent)}
        />
        <MetricTile
          icon={<HardDrive className="h-4 w-4" />}
          label="Storage"
          value={formatBytes(latest.diskUsed)}
          detail={`of ${formatBytes(latest.diskTotal)} · ${diskPercent.toFixed(0)}% in use`}
          bar={diskPercent}
          barTone={usageTone(diskPercent)}
        />
        <MetricTile
          icon={<Network className="h-4 w-4" />}
          label="Network"
          value={`↓ ${formatBytes(last.rx)}/s`}
          detail={`↑ ${formatBytes(last.tx)}/s · total ↓ ${formatBytes(latest.netRx)} ↑ ${formatBytes(latest.netTx)}`}
        />
      </div>

      <p className="mt-2.5 text-[11px] leading-relaxed text-faint">
        Memory is host-wide: it includes the panel, Docker, the build sandbox, the proxy and every
        application. Docker images and build cache count toward storage, not this memory figure.
        An idle build sandbox is stopped automatically and starts again on the next deployment.
      </p>

      <div className="mt-3 grid gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>CPU &amp; memory</CardTitle>
            <Legend
              keys={[
                ["CPU", cpuColor],
                ["Memory", memoryColor],
              ]}
            />
          </CardHeader>
          <CardContent className="pt-3">
            <ResponsiveContainer width="100%" height={170}>
              <AreaChart data={series} margin={{ top: 6, right: 10, bottom: 0, left: -16 }}>
                <defs>
                  <linearGradient id="fill-cpu" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={cpuColor} stopOpacity={isLight ? 0.24 : 0.32} />
                    <stop offset="100%" stopColor={cpuColor} stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="fill-memory" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={memoryColor} stopOpacity={isLight ? 0.2 : 0.28} />
                    <stop offset="100%" stopColor={memoryColor} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke={gridStroke} strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="t"
                  tickFormatter={(value) => clock(Number(value))}
                  tick={axisTick}
                  axisLine={false}
                  tickLine={false}
                  minTickGap={56}
                />
                <YAxis
                  domain={[0, 100]}
                  ticks={[0, 25, 50, 75, 100]}
                  tick={axisTick}
                  axisLine={false}
                  tickLine={false}
                  width={44}
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(value) => new Date(Number(value)).toLocaleTimeString()}
                  formatter={(value) => `${Number(value).toFixed(1)}%`}
                />
                <Area
                  type="monotone"
                  dataKey="cpu"
                  name="CPU"
                  stroke={cpuColor}
                  strokeWidth={1.75}
                  fill="url(#fill-cpu)"
                  dot={false}
                  isAnimationActive={false}
                />
                <Area
                  type="monotone"
                  dataKey="memory"
                  name="Memory"
                  stroke={memoryColor}
                  strokeWidth={1.75}
                  fill="url(#fill-memory)"
                  dot={false}
                  isAnimationActive={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Network</CardTitle>
            <Legend
              keys={[
                ["Down", rxColor],
                ["Up", txColor],
              ]}
            />
          </CardHeader>
          <CardContent className="pt-3">
            <ResponsiveContainer width="100%" height={170}>
              <AreaChart data={series} margin={{ top: 6, right: 10, bottom: 0, left: -6 }}>
                <defs>
                  <linearGradient id="fill-rx" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={rxColor} stopOpacity={isLight ? 0.22 : 0.3} />
                    <stop offset="100%" stopColor={rxColor} stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="fill-tx" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={txColor} stopOpacity={isLight ? 0.2 : 0.26} />
                    <stop offset="100%" stopColor={txColor} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke={gridStroke} strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="t"
                  tickFormatter={(value) => clock(Number(value))}
                  tick={axisTick}
                  axisLine={false}
                  tickLine={false}
                  minTickGap={56}
                />
                <YAxis
                  tickFormatter={(value) => formatBytes(Number(value))}
                  tick={axisTick}
                  axisLine={false}
                  tickLine={false}
                  width={56}
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(value) => new Date(Number(value)).toLocaleTimeString()}
                  formatter={(value) => `${formatBytes(Number(value))}/s`}
                />
                <Area
                  type="monotone"
                  dataKey="rx"
                  name="Down"
                  stroke={rxColor}
                  strokeWidth={1.75}
                  fill="url(#fill-rx)"
                  dot={false}
                  isAnimationActive={false}
                />
                <Area
                  type="monotone"
                  dataKey="tx"
                  name="Up"
                  stroke={txColor}
                  strokeWidth={1.75}
                  fill="url(#fill-tx)"
                  dot={false}
                  isAnimationActive={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      </div>
    </section>
  );
}

function MetricTile({
  icon,
  label,
  value,
  detail,
  bar,
  barTone,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  detail: string;
  bar?: number;
  barTone?: string;
}) {
  return (
    <Card>
      <CardContent className="pt-4">
        <div className="flex items-center gap-2">
          <div className="flex h-6.5 w-6.5 items-center justify-center rounded-md border border-line/60 bg-recessed text-muted">
            {icon}
          </div>
          <span className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            {label}
          </span>
        </div>
        <div className="mt-3 text-[20px] font-semibold tabular-nums text-ink">{value}</div>
        {bar !== undefined && (
          <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-subtle">
            <div
              className={cn("h-full rounded-full transition-[width] duration-500", barTone ?? "bg-brand-solid")}
              style={{ width: `${Math.min(100, Math.max(0, bar))}%` }}
            />
          </div>
        )}
        <div className={cn("text-[11px] text-faint", bar !== undefined ? "mt-1.5" : "mt-2.5")}>
          {detail}
        </div>
      </CardContent>
    </Card>
  );
}

function Legend({ keys }: { keys: [string, string][] }) {
  return (
    <span className="flex items-center gap-3 text-[11px] text-muted">
      {keys.map(([label, color]) => (
        <span key={label} className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full" style={{ background: color }} />
          {label}
        </span>
      ))}
    </span>
  );
}
