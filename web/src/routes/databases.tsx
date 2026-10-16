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
            className="inline-flex items-center gap-1.5 rounded-full bg-white/[0.05] px-2.5 py-1 text-[11px]"
          >
            <DatabaseIcon className="h-3 w-3 opacity-60" />
            {count} {kind}
          </span>
        ))}
        {Object.keys(byKind).length === 0 && (
          <span className="text-[12px] text-black/45 dark:text-white/40">No databases yet.</span>
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
                <Td className="font-medium">{database.name}</Td>
                <Td className="text-black/55 dark:text-white/45">
                  {database.kind}
                  {database.version ? ` ${database.version}` : ""}
                </Td>
                <Td>
                  <StatusBadge status={database.status} />
                </Td>
                <Td align="right" className="font-mono text-[12px] text-black/55 dark:text-white/45">
                  {database.publicPort || "not published"}
                </Td>
                <Td align="right" className="text-black/45 dark:text-white/40">
                  {new Date(database.createdAt).toLocaleDateString()}
                </Td>
              </Tr>
            ))}
            {(databases.data ?? []).length === 0 && (
              <Tr>
                <Td align="center" className="text-center text-black/45 dark:text-white/40">
                  Provision a database from any project page.
                </Td>
              </Tr>
            )}
          </Table>
        </CardContent>
      </Card>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle>Storage</CardTitle>
        </CardHeader>
        <CardContent className="text-[12px] text-black/55 dark:text-white/45">
          Every database keeps its data in a named volume on its node, so a database
          can be redeployed without losing data. Total panel data directory:{" "}
          <span className="font-mono">{formatBytes(undefined)}</span> once populated.
        </CardContent>
      </Card>
    </>
  );
}

export const Route = createFileRoute("/databases")({
  component: DatabasesPage,
});
