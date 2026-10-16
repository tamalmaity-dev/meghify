import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { Job } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

/**
 * The durable queue powers deploys, backups and housekeeping. Surfacing it makes
 * a stuck job obvious instead of leaving it as a mystery outage.
 */
export function JobsPage() {
  const jobs = useQuery({
    queryKey: ["jobs"],
    queryFn: () => api.get<Job[]>("/jobs?status="),
    refetchInterval: 5000,
  });

  const pending = (jobs.data ?? []).filter((job) => !job.finishedAt);

  return (
    <>
      <PageHeader
        title="Job queue"
        description="Background work runs from a table in the panel's own database. No Redis required."
      />

      <div className="mb-4 grid gap-3 sm:grid-cols-3">
        <Card className="min-h-20">
          <CardContent className="pt-4">
            <div className="text-[20px] font-semibold tabular-nums">{pending.length}</div>
            <div className="text-[11px] text-black/45 dark:text-white/40">In flight</div>
          </CardContent>
        </Card>
        <Card className="min-h-20">
          <CardContent className="pt-4">
            <div className="text-[20px] font-semibold tabular-nums">
              {(jobs.data ?? []).filter((job) => job.status === "failed").length}
            </div>
            <div className="text-[11px] text-black/45 dark:text-white/40">Failed</div>
          </CardContent>
        </Card>
        <Card className="min-h-20">
          <CardContent className="pt-4">
            <div className="text-[20px] font-semibold tabular-nums">
              {(jobs.data ?? []).filter((job) => job.status === "done").length}
            </div>
            <div className="text-[11px] text-black/45 dark:text-white/40">Completed</div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Recent jobs</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Kind</Th>
                <Th>Status</Th>
                <Th>Attempts</Th>
                <Th>Error</Th>
                <Th align="right">Created</Th>
              </>
            }
          >
            {(jobs.data ?? []).map((job) => (
              <Tr key={job.id}>
                <Td className="font-mono text-[12px]">{job.kind}</Td>
                <Td>
                  <StatusBadge status={job.status} />
                </Td>
                <Td className="tabular-nums text-black/55 dark:text-white/45">
                  {job.attempts}/{job.maxAttempts}
                </Td>
                <Td className="max-w-xs truncate text-[11px] text-danger">{job.lastError || "-"}</Td>
                <Td align="right" className="text-black/45 dark:text-white/40">
                  {new Date(job.createdAt).toLocaleTimeString()}
                </Td>
              </Tr>
            ))}
            {(jobs.data ?? []).length === 0 && (
              <Tr>
                <Td align="center" className="text-center text-black/45 dark:text-white/40">
                  The queue is empty. Jobs appear here while they run.
                </Td>
              </Tr>
            )}
          </Table>
        </CardContent>
      </Card>
    </>
  );
}

export const Route = createFileRoute("/jobs")({
  component: JobsPage,
});
