import { useQuery } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { APIToken, User } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

export function TeamPage() {
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<User[]>("/users") });
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: () => api.get<APIToken[]>("/tokens") });

  return (
    <>
      <PageHeader
        title="Team"
        description="Members who can sign in to this panel. Invite people from Settings once roles exist."
      />

      <Card>
        <CardHeader>
          <CardTitle>Members</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Member</Th>
                <Th>Role</Th>
                <Th>2FA</Th>
                <Th align="right">Joined</Th>
              </>
            }
          >
            {(users.data ?? []).map((user) => (
              <Tr key={user.id}>
                <Td>
                  <div className="font-medium">{user.name || user.email}</div>
                  <div className="text-[11px] text-black/45 dark:text-white/40">{user.email}</div>
                </Td>
                <Td>{user.role}</Td>
                <Td>{user.totpEnabled ? "enabled" : "off"}</Td>
                <Td align="right" className="text-black/45 dark:text-white/40">
                  {new Date(user.createdAt).toLocaleDateString()}
                </Td>
              </Tr>
            ))}
          </Table>
        </CardContent>
      </Card>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle>API tokens</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Name</Th>
                <Th>Prefix</Th>
                <Th align="right">Created</Th>
              </>
            }
          >
            {(tokens.data ?? []).map((token) => (
              <Tr key={token.id}>
                <Td>
                  <div className="flex items-center gap-1.5">
                    <KeyRound className="h-3.5 w-3.5 opacity-50" />
                    {token.name || "unnamed"}
                  </div>
                </Td>
                <Td className="font-mono text-[12px] text-black/55 dark:text-white/45">
                  {token.prefix}...
                </Td>
                <Td align="right" className="text-black/45 dark:text-white/40">
                  {new Date(token.createdAt).toLocaleDateString()}
                </Td>
              </Tr>
            ))}
            {(tokens.data ?? []).length === 0 && (
              <Tr>
                <Td align="center" className="text-center text-black/45 dark:text-white/40">
                  No API tokens issued.
                </Td>
              </Tr>
            )}
          </Table>
        </CardContent>
      </Card>
    </>
  );
}

export const Route = createFileRoute("/team")({
  component: TeamPage,
});
