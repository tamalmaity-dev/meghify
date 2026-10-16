import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import { Table, Td, Th, Tr } from "@/components/ui/table";
import type { Node } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

export function NodesPage() {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ name: "", address: "", sshUser: "root", sshPort: 22 });

  const nodes = useQuery({ queryKey: ["nodes"], queryFn: () => api.get<Node[]>("/nodes") });
  const keys = useQuery({ queryKey: ["ssh-keys"], queryFn: () => api.get<{ id: string; fingerprint: string }[]>("/ssh-keys") });

  const [sshKeyId, setSSHKeyId] = useState("");

  const create = useMutation({
    mutationFn: () => api.post<Node>("/nodes", { ...form, sshKeyId }),
    onSuccess: () => {
      toast.success("Server registered");
      queryClient.invalidateQueries({ queryKey: ["nodes"] });
      setOpen(false);
      setForm({ name: "", address: "", sshUser: "root", sshPort: 22 });
    },
  });

  const provision = useMutation({
    mutationFn: (id: string) => api.post(`/nodes/${id}/provision`),
    onSuccess: () => toast.success("Provisioning started"),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/nodes/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["nodes"] }),
  });

  return (
    <>
      <PageHeader
        title="Servers"
        description="Each server the panel controls through SSH and the Docker Engine API."
        actions={
          <Button variant="primary" onClick={() => setOpen(true)} disabled={keys.data?.length === 0}>
            <Plus className="h-4 w-4" />
            Add server
          </Button>
        }
      />

      {keys.data?.length === 0 && (
        <Card className="mb-4">
          <CardContent className="py-4 text-[12px] text-black/55 dark:text-white/45">
            Generate an SSH key first, then install its public half on the server you
            want to add.
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Registered servers</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Name</Th>
                <Th>Address</Th>
                <Th>Status</Th>
                <Th>Platform</Th>
                <Th align="right">Resources</Th>
                <Th align="right">Actions</Th>
              </>
            }
          >
            {(nodes.data ?? []).map((node) => (
              <Tr key={node.id}>
                <Td className="font-medium">{node.name}</Td>
                <Td className="font-mono text-[12px] text-black/55 dark:text-white/45">
                  {node.sshUser}@{node.address}:{node.sshPort}
                </Td>
                <Td>
                  <StatusBadge status={node.status} />
                </Td>
                <Td className="text-black/55 dark:text-white/45">
                  {node.os || "-"} {node.dockerVersion ? `· docker ${node.dockerVersion}` : ""}
                </Td>
                <Td align="right" className="text-black/55 dark:text-white/45">
                  {node.cpus || "-"} vCPU
                </Td>
                <Td align="right">
                  <div className="flex items-center justify-end gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => provision.mutate(node.id)}
                      title="Install Docker and connect"
                    >
                      <RefreshCw className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => remove.mutate(node.id)}
                      title="Remove server"
                    >
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                        <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" />
                      </svg>
                    </Button>
                  </div>
                </Td>
              </Tr>
            ))}
            {(nodes.data ?? []).length === 0 && (
              <Tr>
                <Td align="center" className="text-center text-black/45 dark:text-white/40">
                  No servers registered. The panel can manage the machine it runs on.
                </Td>
              </Tr>
            )}
          </Table>
        </CardContent>
      </Card>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle>SSH keys</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table
            head={
              <>
                <Th>Fingerprint</Th>
                <Th align="right">Created</Th>
              </>
            }
          >
            {(keys.data ?? []).map((key) => (
              <Tr key={key.id}>
                <Td className="font-mono text-[12px]">{key.fingerprint}</Td>
                <Td align="right" className="text-black/45 dark:text-white/40">
                  {new Date((key as unknown as { createdAt: string }).createdAt).toLocaleDateString()}
                </Td>
              </Tr>
            ))}
            {(keys.data ?? []).length === 0 && (
              <Tr>
                <Td align="center" className="text-center text-black/45 dark:text-white/40">
                  No keys generated yet.
                </Td>
              </Tr>
            )}
          </Table>
        </CardContent>
      </Card>

      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="Add a server"
        description="The panel installs Docker over SSH, then talks to the remote engine through a tunnel."
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={!form.name.trim() || !form.address.trim() || !sshKeyId}
              onClick={() => create.mutate()}
            >
              Register server
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div>
            <Label htmlFor="node-name">Name</Label>
            <Input
              id="node-name"
              value={form.name}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
              placeholder="eu-worker-1"
              autoFocus
            />
          </div>
          <div className="grid grid-cols-[1fr_120px] gap-3">
            <div>
              <Label htmlFor="node-address">Address</Label>
              <Input
                id="node-address"
                value={form.address}
                onChange={(event) => setForm({ ...form, address: event.target.value })}
                placeholder="203.0.113.10"
              />
            </div>
            <div>
              <Label htmlFor="node-port">SSH port</Label>
              <Input
                id="node-port"
                type="number"
                value={form.sshPort}
                onChange={(event) => setForm({ ...form, sshPort: Number(event.target.value) })}
              />
            </div>
          </div>
          <div className="grid grid-cols-[1fr_140px] gap-3">
            <div>
              <Label htmlFor="node-user">SSH user</Label>
              <Input
                id="node-user"
                value={form.sshUser}
                onChange={(event) => setForm({ ...form, sshUser: event.target.value })}
              />
            </div>
            <div>
              <Label htmlFor="node-key">SSH key</Label>
              <select
                id="node-key"
                value={sshKeyId}
                onChange={(event) => setSSHKeyId(event.target.value)}
                className="h-8 w-full rounded-md bg-recessed px-2 text-[13px] ring-1 ring-line/70 outline-none focus:ring-2 focus:ring-brand"
              >
                <option value="">Select a key</option>
                {(keys.data ?? []).map((key) => (
                  <option key={key.id} value={key.id}>
                    {key.fingerprint.slice(0, 18)}
                  </option>
                ))}
              </select>
            </div>
          </div>
        </div>
      </Modal>
    </>
  );
}

export const Route = createFileRoute("/nodes")({
  component: NodesPage,
});
