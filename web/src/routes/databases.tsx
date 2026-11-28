import { useQuery } from "@tanstack/react-query";
import { Database as DatabaseIcon } from "lucide-react";
import { api, formatBytes } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { Database } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

export function DatabasesPage() {
  const databases = useQuery({
    queryKey: ["databases"],
    queryFn: () => api.get<Database[]>("/databases"),
  });

  const byKind = (databases.data ?? []).reduce<Record<string, number>>((acc, db) => {
    acc[db.kind] = (acc[db.kind] ?? 0) + 1;
    return acc;
  }, {});

  return (
    <>
      <PageHeader
        title="Databases"
        description="Provisioned database instances with generated credentials. Create them from a project page."
      />

      <div className="mb-4 flex flex-wrap gap-2">
        {Object.entries(byKind).map(([kind, count]) => (
          <span
            key={kind}
            className="inline-flex items-center gap-1.5 rounded-full border border-line/70 bg-base px-3 py-1 text-[11.5px] font-medium text-ink shadow-xs"
          >
            <DatabaseIcon className="h-3.5 w-3.5 text-brand" />
            {count} {kind}
          </span>
        ))}
        {Object.keys(byKind).length === 0 && (
          <span className="text-[12px] text-muted">No databases yet.</span>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>All databases</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Name</Th>
                <Th>Kind</Th>
                <Th>Status</Th>
                <Th align="right">Public port</Th>
                <Th align="right">Created</Th>
              </>
            }
          >
            {(databases.data ?? []).map((database) => (
              <Tr key={database.id}>
                <Td className="font-medium text-ink">{database.name}</Td>
                <Td className="text-muted">
                  {database.kind}
                  {database.version ? ` ${database.version}` : ""}
                </Td>
                <Td>
                  <StatusBadge status={database.status} />
                </Td>
                <Td align="right" className="font-mono text-[12px] text-muted">
                  {database.publicPort || "not published"}
                </Td>
                <Td align="right" className="text-faint">
                  {new Date(database.createdAt).toLocaleDateString()}
                </Td>
              </Tr>
            ))}
            {(databases.data ?? []).length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-[12px] text-muted">
                  Provision a database from any project page.
                </td>
              </tr>
            )}
          </Table>
        </CardContent>
      </Card>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle>Storage</CardTitle>
        </CardHeader>
        <CardContent className="text-[12px] leading-relaxed text-muted">
          Every database keeps its data in a named volume on its node, so a database
          can be redeployed without losing data. Total panel data directory:{" "}
          <span className="font-mono text-ink">{formatBytes(undefined)}</span> once populated.
        </CardContent>
      </Card>
    </>
  );
}

export const Route = createFileRoute("/databases")({
  component: DatabasesPage,
});
