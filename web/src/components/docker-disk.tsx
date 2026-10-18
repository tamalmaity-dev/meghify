import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { HardDrive, RefreshCw, Trash2 } from "lucide-react";
import { Cell, Pie, PieChart, ResponsiveContainer } from "recharts";
import { toast } from "sonner";
import { api, formatBytes } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { DockerCleanupResult, DockerDiskUsage, DockerUsageItem } from "@/lib/types";

/**
 * The Docker side of the disk the STORAGE tile counts: what the engine holds
 * in images, containers, volumes and build cache, what the build sandbox's
 * own daemon adds, and the objects that dominate each. `docker system df`
 * walks the stores for this, so the card refreshes on the minute and reloads
 * manually instead of every tick.
 */

const REFETCH_MS = 60_000;

const IMAGES_COLOR = "#a78bfa";
const CONTAINERS_COLOR = "#fbbf24";
const VOLUMES_COLOR = "#34d399";
const BUILD_CACHE_COLOR = "#38bdf8";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

const kindLabel: Record<DockerUsageItem["kind"], string> = {
  image: "image",
  container: "container",
  volume: "volume",
};

export function DockerDiskUsage() {
  const queryClient = useQueryClient();
  const usage = useQuery({
    queryKey: ["docker-df"],
    queryFn: () => api.get<DockerDiskUsage>("/docker/df"),
    refetchInterval: REFETCH_MS,
  });

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

  if (usage.isError) {
    return (
      <section className="mb-4">
        <div className="mb-2 flex items-baseline justify-between">
          <h2 className="text-[13px] font-semibold tracking-tight">Docker storage</h2>
        </div>
        <Card>
          <CardContent className="py-6 text-center text-[12px] text-black/45 dark:text-white/40">
            Docker disk usage is unavailable right now — {errorText(usage.error)}
          </CardContent>
        </Card>
      </section>
    );
  }

  const slices = engine
    ? [
        { name: "Images", value: engine.images, color: IMAGES_COLOR },
        { name: "Build cache", value: engine.buildCache, color: BUILD_CACHE_COLOR },
        { name: "Volumes", value: engine.volumes, color: VOLUMES_COLOR },
        { name: "Containers", value: engine.containers, color: CONTAINERS_COLOR },
      ].filter((slice) => slice.value > 0)
    : [];
  const empty = slices.length === 0;

  return (
    <section className="mb-4">
      <div className="mb-2 flex items-baseline justify-between">
        <h2 className="text-[13px] font-semibold tracking-tight">Docker storage</h2>
        <span className="text-[11px] text-black/40 dark:text-white/35">refreshes every minute</span>
      </div>

      <Card>
        <CardHeader className="pb-1">
          <CardTitle>
            <span className="inline-flex items-center gap-2">
              <HardDrive className="h-4 w-4" />
              Docker disk usage
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-1">
          <div className="grid gap-4 lg:grid-cols-2">
            <div className="flex items-center gap-4">
              <div className="relative h-[168px] w-[168px] shrink-0">
                {usage.isLoading || empty ? (
                  <div className="flex h-full items-center justify-center text-[12px] text-black/40 dark:text-white/35">
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
                      <div className="text-[15px] font-semibold tabular-nums">
                        {formatBytes(engine?.total)}
                      </div>
                      <div className="text-[10px] uppercase tracking-wider text-black/40 dark:text-white/35">
                        Docker usage
                      </div>
                    </div>
                  </>
                )}
              </div>

              <div className="min-w-0 flex-1 space-y-1.5 text-[12px]">
                {([["Images", IMAGES_COLOR], ["Build cache", BUILD_CACHE_COLOR], ["Volumes", VOLUMES_COLOR], ["Containers", CONTAINERS_COLOR]] as const).map(
                  ([label, color]) => (
                    <div key={label} className="flex items-center justify-between gap-2">
                      <span className="flex min-w-0 items-center gap-1.5">
                        <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: color }} />
                        <span className="truncate text-black/60 dark:text-white/50">{label}</span>
                      </span>
                      <span className="shrink-0 tabular-nums text-black/55 dark:text-white/45">
                        {formatBytes(
                          label === "Images" ? engine?.images
                          : label === "Build cache" ? engine?.buildCache
                          : label === "Volumes" ? engine?.volumes
                          : engine?.containers,
                        )}
                      </span>
                    </div>
                  ),
                )}
                {sandbox && (
                  <p className="pt-1 text-[11px] text-black/40 dark:text-white/35">
                    Build sandbox stores{" "}
                    <span className="tabular-nums">{formatBytes(sandbox.total)}</span> of that —
                    images {formatBytes(sandbox.images)}, cache {formatBytes(sandbox.buildCache)}
                  </p>
                )}
                <div className="flex gap-2 pt-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={usage.isFetching}
                    onClick={() => void usage.refetch()}
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
              </div>
            </div>

            <div>
              <p className="mb-2 text-[11px] uppercase tracking-wider text-black/40 dark:text-white/35">
                Largest items
              </p>
              <div className="space-y-1.5">
                {top.length === 0 && (
                  <p className="text-[12px] text-black/40 dark:text-white/35">
                    Nothing measured yet.
                  </p>
                )}
                {top.map((item) => (
                  <TopRow key={`${item.kind}:${item.name}`} item={item} total={engine?.total ?? 0} />
                ))}
              </div>
            </div>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}

function TopRow({ item, total }: { item: DockerUsageItem; total: number }) {
  const width = total > 0 ? Math.max(2, Math.round((item.bytes / total) * 100)) : 0;
  return (
    <div className="text-[12px]">
      <div className="flex items-baseline justify-between gap-2">
        <span className="truncate font-mono text-[11.5px] text-black/70 dark:text-white/55" title={item.name}>
          {item.name}
        </span>
        <span className="shrink-0 text-black/45 dark:text-white/40">
          <span className="tabular-nums">{formatBytes(item.bytes)}</span>
          <span className="ml-1.5 text-[10.5px] uppercase tracking-wide">{kindLabel[item.kind]}</span>
        </span>
      </div>
      <div className="mt-0.5 h-1 w-full overflow-hidden rounded-full bg-white/[0.07] dark:bg-white/[0.06]">
        <div
          className="h-full rounded-full opacity-70 transition-[width] duration-500"
          style={{
            width: `${width}%`,
            background:
              item.kind === "volume" ? VOLUMES_COLOR : item.kind === "image" ? IMAGES_COLOR : CONTAINERS_COLOR,
          }}
        />
      </div>
    </div>
  );
}
