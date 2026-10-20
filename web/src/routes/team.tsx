import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useSession } from "@/lib/session";
import { PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input, Label, Select } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { APIToken, User } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

export function TeamPage() {
  const queryClient = useQueryClient();
  const { user: currentUser } = useSession();
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<User[]>("/users") });
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: () => api.get<APIToken[]>("/tokens") });

  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({
    name: "",
    email: "",
    password: "",
    role: "member",
  });

  const createUser = useMutation({
    mutationFn: () =>
      api.post<User>("/users", {
        name: form.name.trim(),
        email: form.email.trim(),
        password: form.password,
        role: form.role,
      }),
    onSuccess: async () => {
      toast.success("Member added");
      setOpen(false);
      setForm({ name: "", email: "", password: "", role: "member" });
      await queryClient.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const updateRole = useMutation({
    mutationFn: ({ user, role }: { user: User; role: string }) =>
      api.put<{ status: string }>(`/users/${user.id}`, { name: user.name, role }),
    onSuccess: async (_data, variables) => {
      toast.success("Role updated");
      await queryClient.invalidateQueries({ queryKey: ["users"] });
      // Demoting yourself must update the navigation and action gates in this
      // tab immediately; the API already enforces the new role on every call.
      if (variables.user.id === currentUser?.id) {
        await queryClient.invalidateQueries({ queryKey: ["session"] });
      }
    },
    onError: (error) => toast.error(errorText(error)),
  });

  const removeUser = useMutation({
    mutationFn: (id: string) => api.delete(`/users/${id}`),
    onSuccess: async () => {
      toast.success("Member removed");
      await queryClient.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (error) => toast.error(errorText(error)),
  });

  return (
    <>
      <PageHeader
        title="Team"
        description="Members who can sign in to this panel. Admins manage infrastructure; members deploy and operate existing applications."
        actions={
          <Button variant="primary" onClick={() => setOpen(true)}>
            <Plus className="h-4 w-4" />
            Add member
          </Button>
        }
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
                <Th align="right">Actions</Th>
              </>
            }
          >
            {(users.data ?? []).map((user) => {
              const isSelf = user.id === currentUser?.id;
              return (
                <Tr key={user.id}>
                  <Td>
                    <div className="font-medium">
                      {user.name || user.email}
                      {isSelf && (
                        <span className="ml-1.5 text-[10px] text-black/35 dark:text-white/30">you</span>
                      )}
                    </div>
                    <div className="text-[11px] text-black/45 dark:text-white/40">{user.email}</div>
                  </Td>
                  <Td>
                    <Select
                      value={user.role}
                      disabled={updateRole.isPending}
                      onChange={(event) =>
                        updateRole.mutate({ user, role: event.target.value })
                      }
                    >
                      <option value="member">member</option>
                      <option value="admin">admin</option>
                    </Select>
                  </Td>
                  <Td>{user.totpEnabled ? "enabled" : "off"}</Td>
                  <Td align="right" className="text-black/45 dark:text-white/40">
                    {new Date(user.createdAt).toLocaleDateString()}
                  </Td>
                  <Td align="right">
                    <Button
                      variant="ghost"
                      size="icon"
                      title={isSelf ? "Use another admin to remove your own account" : "Remove member"}
                      disabled={isSelf || removeUser.isPending}
                      onClick={() => removeUser.mutate(user.id)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </Td>
                </Tr>
              );
            })}
          </Table>
        </CardContent>
      </Card>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle>Your API tokens</CardTitle>
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

      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="Add a member"
        description="They can sign in immediately with this password. Ask them to change it from Settings."
        footer={
          <>
            <Button variant="neutral" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={
                createUser.isPending ||
                form.email.trim() === "" ||
                form.password.length < 10
              }
              onClick={() => createUser.mutate()}
            >
              Add member
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div>
            <Label htmlFor="member-name">Name</Label>
            <Input
              id="member-name"
              value={form.name}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
            />
          </div>
          <div>
            <Label htmlFor="member-email">Email</Label>
            <Input
              id="member-email"
              type="email"
              value={form.email}
              onChange={(event) => setForm({ ...form, email: event.target.value })}
            />
          </div>
          <div>
            <Label htmlFor="member-password">Initial password</Label>
            <Input
              id="member-password"
              type="password"
              value={form.password}
              onChange={(event) => setForm({ ...form, password: event.target.value })}
            />
            <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/35">
              At least 10 characters. Share it over a trusted channel.
            </p>
          </div>
          <div>
            <Label htmlFor="member-role">Role</Label>
            <Select
              id="member-role"
              value={form.role}
              onChange={(event) => setForm({ ...form, role: event.target.value })}
            >
              <option value="member">member — deploy and operate applications</option>
              <option value="admin">admin — manage infrastructure and users</option>
            </Select>
          </div>
        </div>
      </Modal>
    </>
  );
}

export const Route = createFileRoute("/team")({
  component: TeamPage,
});
