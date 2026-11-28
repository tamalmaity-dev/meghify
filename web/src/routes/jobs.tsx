import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { Job } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const KIND_LABELS: Record<string, string> = {
  deploy: "Application deploy",
  build_image: "Compose stack build",
  backup: "Database backup",
  gc: "Cleanup & retention",
  node_metrics: "Host metrics",
};

export function JobsPage() {
  const jobs = useQuery({
    queryKey: ["jobs"],
    queryFn: () => api.get<Job[]>("/jobs?status="),
    refetchInterval: 5000,
  });

  const pending = (jobs.data ?? []).filter((job) => !job.finishedAt);
  const failed = (jobs.data ?? []).filter((job) => job.status === "failed").length;
  const done = (jobs.data ?? []).filter((job) => job.status === "done").length;

  return (
    <>
      <PageHeader
        title="Job queue"
        description="Deploys, backups and housekeeping run from a table in the panel's own database — no Redis needed. Host metrics are sampled outside the queue, so every row here is real work."
      />

      <div className="mb-4 grid gap-3 sm:grid-cols-3">
        <Card className="min-h-20">
          <CardContent className="pt-4">
            <div className="text-[22px] font-semibold tabular-nums text-ink">{pending.length}</div>
            <div className="text-[11px] font-medium text-muted">In flight</div>
          </CardContent>
        </Card>
        <Card className="min-h-20">
          <CardContent className="pt-4">
            <div className="text-[22px] font-semibold tabular-nums text-danger">{failed}</div>
            <div className="text-[11px] font-medium text-muted">Failed</div>
          </CardContent>
        </Card>
        <Card className="min-h-20">
          <CardContent className="pt-4">
            <div className="text-[22px] font-semibold tabular-nums text-ok">{done}</div>
            <div className="text-[11px] font-medium text-muted">Completed</div>
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
                <Td>
                  <div className="text-[12.5px] font-medium text-ink">
                    {KIND_LABELS[job.kind] ?? job.kind}
                  </div>
                  <div className="font-mono text-[10.5px] text-faint">{job.kind}</div>
                </Td>
                <Td>
                  <StatusBadge status={job.status} />
                </Td>
                <Td className="tabular-nums text-muted">
                  {job.attempts}/{job.maxAttempts}
                </Td>
                <Td className="max-w-xs truncate text-[11px] text-danger">{job.lastError || "-"}</Td>
                <Td align="right" className="text-faint">
                  {new Date(job.createdAt).toLocaleTimeString()}
                </Td>
              </Tr>
            ))}
            {(jobs.data ?? []).length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-[12px] text-muted">
                  The queue is empty. Jobs appear here while they run.
                </td>
              </tr>
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
