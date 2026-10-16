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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { NodeMetrics } from "@/lib/types";

const REFETCH_MS = 15_000;

const CPU_COLOR = "#a78bfa";
const MEMORY_COLOR = "#34d399";
const RX_COLOR = "#38bdf8";
const TX_COLOR = "#fbbf24";

const TOOLTIP_STYLE: CSSProperties = {
  background: "rgba(24, 24, 27, 0.96)",
  border: "1px solid rgba(128, 128, 128, 0.25)",
  borderRadius: 8,
  fontSize: 12,
  padding: "6px 10px",
};

const AXIS_TICK = { fontSize: 10, fill: "rgba(128, 128, 128, 0.9)" };

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
  return "bg-brand";
}

/**
 * The machine section of the dashboard: live CPU, memory, storage and network
 * for the server the panel runs on, with an hour of history behind each chart.
 */
export function SystemMetrics() {
  const metrics = useQuery({
    queryKey: ["node-metrics", "local"],
    queryFn: () => api.get<NodeMetrics[]>("/nodes/local/metrics?since=1h"),
    refetchInterval: REFETCH_MS,
  });

  const rows = metrics.data ?? [];
  if (rows.length === 0) {
    return (
      <Card className="mb-4">
        <CardContent className="py-6 text-center text-[12px] text-black/45 dark:text-white/40">
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
    <section className="mb-4">
      <div className="mb-2 flex items-baseline justify-between">
        <h2 className="text-[13px] font-semibold tracking-tight">Machine</h2>
        <span className="text-[11px] text-black/40 dark:text-white/35">
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

      <div className="mt-3 grid gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader className="pb-1">
            <CardTitle>CPU &amp; memory</CardTitle>
            <Legend
              keys={[
                ["CPU", CPU_COLOR],
                ["Memory", MEMORY_COLOR],
              ]}
            />
          </CardHeader>
          <CardContent className="pt-1">
            <ResponsiveContainer width="100%" height={170}>
              <AreaChart data={series} margin={{ top: 6, right: 10, bottom: 0, left: -16 }}>
                <defs>
                  <linearGradient id="fill-cpu" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={CPU_COLOR} stopOpacity={0.32} />
                    <stop offset="100%" stopColor={CPU_COLOR} stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="fill-memory" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={MEMORY_COLOR} stopOpacity={0.28} />
                    <stop offset="100%" stopColor={MEMORY_COLOR} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="rgba(128,128,128,0.16)" strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="t"
                  tickFormatter={(value) => clock(Number(value))}
                  tick={AXIS_TICK}
                  axisLine={false}
                  tickLine={false}
                  minTickGap={56}
                />
                <YAxis
                  domain={[0, 100]}
                  ticks={[0, 25, 50, 75, 100]}
                  tick={AXIS_TICK}
                  axisLine={false}
                  tickLine={false}
                  width={44}
                />
                <Tooltip
                  contentStyle={TOOLTIP_STYLE}
                  labelFormatter={(value) => new Date(Number(value)).toLocaleTimeString()}
                  formatter={(value) => `${Number(value).toFixed(1)}%`}
                />
                <Area
                  type="monotone"
                  dataKey="cpu"
                  name="CPU"
                  stroke={CPU_COLOR}
                  strokeWidth={1.5}
                  fill="url(#fill-cpu)"
                  dot={false}
                  isAnimationActive={false}
                />
                <Area
                  type="monotone"
                  dataKey="memory"
                  name="Memory"
                  stroke={MEMORY_COLOR}
                  strokeWidth={1.5}
                  fill="url(#fill-memory)"
                  dot={false}
                  isAnimationActive={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-1">
            <CardTitle>Network</CardTitle>
            <Legend
              keys={[
                ["Down", RX_COLOR],
                ["Up", TX_COLOR],
              ]}
            />
          </CardHeader>
          <CardContent className="pt-1">
            <ResponsiveContainer width="100%" height={170}>
              <AreaChart data={series} margin={{ top: 6, right: 10, bottom: 0, left: -6 }}>
                <defs>
                  <linearGradient id="fill-rx" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={RX_COLOR} stopOpacity={0.3} />
                    <stop offset="100%" stopColor={RX_COLOR} stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="fill-tx" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={TX_COLOR} stopOpacity={0.26} />
                    <stop offset="100%" stopColor={TX_COLOR} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="rgba(128,128,128,0.16)" strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="t"
                  tickFormatter={(value) => clock(Number(value))}
                  tick={AXIS_TICK}
                  axisLine={false}
                  tickLine={false}
                  minTickGap={56}
                />
                <YAxis
                  tickFormatter={(value) => formatBytes(Number(value))}
                  tick={AXIS_TICK}
                  axisLine={false}
                  tickLine={false}
                  width={56}
                />
                <Tooltip
                  contentStyle={TOOLTIP_STYLE}
                  labelFormatter={(value) => new Date(Number(value)).toLocaleTimeString()}
                  formatter={(value) => `${formatBytes(Number(value))}/s`}
                />
                <Area
                  type="monotone"
                  dataKey="rx"
                  name="Down"
                  stroke={RX_COLOR}
                  strokeWidth={1.5}
                  fill="url(#fill-rx)"
                  dot={false}
                  isAnimationActive={false}
                />
                <Area
                  type="monotone"
                  dataKey="tx"
                  name="Up"
                  stroke={TX_COLOR}
                  strokeWidth={1.5}
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
          <div className="flex h-6 w-6 items-center justify-center rounded-md bg-white/[0.06] text-black/60 dark:text-white/55">
            {icon}
          </div>
          <span className="text-[11px] font-medium uppercase tracking-wider text-black/45 dark:text-white/40">
            {label}
          </span>
        </div>
        <div className="mt-3 text-[20px] font-semibold tabular-nums">{value}</div>
        {bar !== undefined && (
          <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-white/[0.07]">
            <div
              className={cn("h-full rounded-full transition-[width] duration-500", barTone ?? "bg-brand")}
              style={{ width: `${Math.min(100, Math.max(0, bar))}%` }}
            />
          </div>
        )}
        <div
          className={cn(
            "text-[11px] text-black/40 dark:text-white/35",
            bar !== undefined ? "mt-1.5" : "mt-2.5",
          )}
        >
          {detail}
        </div>
      </CardContent>
    </Card>
  );
}

function Legend({ keys }: { keys: [string, string][] }) {
  return (
    <span className="flex items-center gap-3 text-[11px] text-black/45 dark:text-white/40">
      {keys.map(([label, color]) => (
        <span key={label} className="flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full" style={{ background: color }} />
          {label}
        </span>
      ))}
    </span>
  );
}
