import { useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { HardDrive, RefreshCw, Trash2 } from "lucide-react";
import { Cell, Pie, PieChart, ResponsiveContainer } from "recharts";
import { toast } from "sonner";
import { api, formatBytes } from "@/lib/api";
import { useTheme } from "@/lib/theme";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { DockerCleanupResult, DockerDiskUsage, DockerUsageItem } from "@/lib/types";

const REFETCH_MS = 15 * 60_000;

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

const kindLabel: Record<DockerUsageItem["kind"], string> = {
  image: "image",
  container: "container",
  volume: "volume",
};

export function DockerDiskUsage() {
  const { resolvedTheme } = useTheme();
  const isLight = resolvedTheme === "light";

  const imagesColor = isLight ? "#7c3aed" : "#a78bfa";
  const containersColor = isLight ? "#d97706" : "#fbbf24";
  const volumesColor = isLight ? "#059669" : "#34d399";
  const buildCacheColor = isLight ? "#0284c7" : "#38bdf8";

  const queryClient = useQueryClient();
  const forceNext = useRef(false);
  const usage = useQuery({
    queryKey: ["docker-df"],
    queryFn: () => api.get<DockerDiskUsage>(`/docker/df${forceNext.current ? "?fresh=1" : ""}`),
    refetchInterval: REFETCH_MS,
  });

  const refresh = async () => {
    forceNext.current = true;
    try {
      await usage.refetch();
    } finally {
      forceNext.current = false;
    }
  };

  const cleanup = useMutation({
    mutationFn: () => api.post<DockerCleanupResult>("/docker/cleanup"),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["docker-df"] });
      const details = [
        formatBytes(result.reclaimed) + " reclaimed",
        `${result.imagesRemoved} image${result.imagesRemoved === 1 ? "" : "s"} removed`,
        result.sandbox ? "build cache bounded" : null,
      ].filter(Boolean);
      toast.success(`Free space reclaimed — ${details.join(" · ")}`);
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const engine = usage.data?.engine;
  const sandbox = usage.data?.sandbox;
  const top = usage.data?.top ?? [];
  const sampledAt = usage.data?.sampledAt
    ? new Date(usage.data.sampledAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : "";

  if (usage.isError) {
    return (
      <section className="mb-5">
        <div className="mb-2 flex items-baseline justify-between">
          <h2 className="text-[13px] font-semibold tracking-tight text-ink">Docker storage</h2>
        </div>
        <Card>
          <CardContent className="py-6 text-center text-[12px] text-muted">
            Docker disk usage is unavailable right now — {errorText(usage.error)}
          </CardContent>
        </Card>
      </section>
    );
  }

  const slices = engine
    ? [
        { name: "Images", value: engine.images, color: imagesColor },
        { name: "Build cache", value: engine.buildCache, color: buildCacheColor },
        { name: "Volumes", value: engine.volumes, color: volumesColor },
        { name: "Containers", value: engine.containers, color: containersColor },
      ].filter((slice) => slice.value > 0)
    : [];
  const empty = slices.length === 0;

  return (
    <section className="mb-5">
      <div className="mb-2.5 flex items-baseline justify-between">
        <h2 className="text-[13px] font-semibold tracking-tight text-ink">Docker storage</h2>
        <span className="text-[11px] text-faint">
          {sampledAt ? `measured ${sampledAt} · ` : ""}refreshes every 15 min
        </span>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>
            <span className="inline-flex items-center gap-2">
              <HardDrive className="h-4 w-4 text-muted" />
              Docker disk usage
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-4">
          <div className="grid gap-5 lg:grid-cols-2">
            <div className="flex flex-col gap-4 sm:flex-row sm:items-center">
              <div className="relative h-[168px] w-[168px] shrink-0 self-center">
                {usage.isLoading || empty ? (
                  <div className="flex h-full items-center justify-center rounded-full border border-dashed border-line text-[12px] text-faint">
                    {usage.isLoading ? "Measuring…" : "Nothing stored yet"}
                  </div>
                ) : (
                  <>
                    <ResponsiveContainer width="100%" height="100%">
                      <PieChart>
                        <Pie
                          data={slices}
                          dataKey="value"
                          nameKey="name"
                          innerRadius={56}
                          outerRadius={80}
                          paddingAngle={2}
                          cornerRadius={4}
                          strokeWidth={0}
                          isAnimationActive={false}
                        >
                          {slices.map((slice) => (
                            <Cell key={slice.name} fill={slice.color} />
                          ))}
                        </Pie>
                      </PieChart>
                    </ResponsiveContainer>
                    <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
                      <div className="text-[15px] font-semibold tabular-nums text-ink">
                        {formatBytes(engine?.total)}
                      </div>
                      <div className="text-[10px] uppercase tracking-wider text-faint">
                        Docker usage
                      </div>
                    </div>
                  </>
                )}
              </div>

              <div className="min-w-0 flex-1 space-y-1.5 text-[12px]">
                {([
                  ["Images", imagesColor],
                  ["Build cache", buildCacheColor],
                  ["Volumes", volumesColor],
                  ["Containers", containersColor],
                ] as const).map(([label, color]) => (
                  <div key={label} className="flex items-center justify-between gap-2">
                    <span className="flex min-w-0 items-center gap-1.5">
                      <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: color }} />
                      <span className="truncate text-muted">{label}</span>
                    </span>
                    <span className="shrink-0 font-mono text-[11.5px] tabular-nums text-ink">
                      {formatBytes(
                        label === "Images"
                          ? engine?.images
                          : label === "Build cache"
                            ? engine?.buildCache
                            : label === "Volumes"
                              ? engine?.volumes
                              : engine?.containers,
                      )}
                    </span>
                  </div>
                ))}
                {sandbox && (
                  <p className="pt-1 text-[11px] text-faint">
                    Build sandbox stores{" "}
                    <span className="tabular-nums text-muted">{formatBytes(sandbox.total)}</span> of
                    that — images {formatBytes(sandbox.images)}, cache{" "}
                    {formatBytes(sandbox.buildCache)}
                  </p>
                )}
                <div className="flex flex-wrap gap-2 pt-2">
                  <Button
                    variant="neutral"
                    size="sm"
                    disabled={usage.isFetching}
                    onClick={() => void refresh()}
                  >
                    <RefreshCw className={cn("h-3.5 w-3.5", usage.isFetching && "animate-spin")} />
                    Refresh
                  </Button>
                  <Button
                    variant="danger"
                    size="sm"
                    disabled={cleanup.isPending}
                    onClick={() => cleanup.mutate()}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    {cleanup.isPending ? "Cleaning up…" : "Free up space"}
                  </Button>
                </div>
                <p className="pt-1 text-[11px] leading-relaxed text-faint">
                  Old panel-built images, superseded proxy versions and retired helpers are pruned
                  automatically after releases, at startup and by the hourly GC.
                </p>
              </div>
            </div>

            <div>
              <p className="mb-2.5 text-[11px] font-semibold uppercase tracking-wider text-faint">
                Largest items
              </p>
              <div className="space-y-2">
                {top.length === 0 && (
                  <p className="text-[12px] text-faint">Nothing measured yet.</p>
                )}
                {top.map((item) => (
                  <TopRow
                    key={`${item.kind}:${item.name}`}
                    item={item}
                    total={engine?.total ?? 0}
                    colors={{ imagesColor, volumesColor, containersColor }}
                  />
                ))}
              </div>
            </div>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}

function TopRow({
  item,
  total,
  colors,
}: {
  item: DockerUsageItem;
  total: number;
  colors: { imagesColor: string; volumesColor: string; containersColor: string };
}) {
  const width = total > 0 ? Math.max(2, Math.round((item.bytes / total) * 100)) : 0;
  return (
    <div className="text-[12px]">
      <div className="flex items-baseline justify-between gap-2">
        <span className="truncate font-mono text-[11.5px] text-ink" title={item.name}>
          {item.name}
        </span>
        <span className="shrink-0 text-muted">
          <span className="tabular-nums">{formatBytes(item.bytes)}</span>
          <span className="ml-1.5 text-[10px] uppercase tracking-wide text-faint">
            {kindLabel[item.kind]}
          </span>
        </span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-subtle">
        <div
          className="h-full rounded-full opacity-80 transition-[width] duration-500"
          style={{
            width: `${width}%`,
            background:
              item.kind === "volume"
                ? colors.volumesColor
                : item.kind === "image"
                  ? colors.imagesColor
                  : colors.containersColor,
          }}
        />
      </div>
    </div>
  );
}
