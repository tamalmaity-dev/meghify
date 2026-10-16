import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useState } from "react";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { EmptyState } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import type { Project } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

export function ProjectsPage() {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  const projects = useQuery({
    queryKey: ["projects"],
    queryFn: () => api.get<Project[]>("/projects"),
  });

  const create = useMutation({
    mutationFn: () => api.post<Project>("/projects", { name, description }),
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: ["projects"] });
      setOpen(false);
      setName("");
      setDescription("");
      return navigate({ to: "/projects/$projectId", params: { projectId: project.id } });
    },
  });

  const navigate = useNavigate();

  return (
    <>
      <PageHeader
        title="Projects"
        description="Group environments, applications and databases by product."
        actions={
          <Button variant="primary" onClick={() => setOpen(true)}>
            <Plus className="h-4 w-4" />
            New project
          </Button>
        }
      />

      {(projects.data?.length ?? 0) === 0 ? (
        <Card>
          <EmptyState
            title="No projects yet"
            description="A project holds one or more environments. Create one, then add an application or a database."
            action={
              <Button variant="primary" onClick={() => setOpen(true)}>
                Create your first project
              </Button>
            }
          />
        </Card>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {(projects.data ?? []).map((project) => (
            <Link
              key={project.id}
              to="/projects/$projectId"
              params={{ projectId: project.id }}
            >
              <Card className="min-h-28 transition-transform hover:-translate-y-px">
                <CardHeader className="pb-1">
                  <div className="flex h-6 w-6 items-center justify-center rounded-md bg-white/[0.06] text-[11px] font-medium">
                    {project.name.slice(0, 2).toUpperCase()}
                  </div>
                </CardHeader>
                <CardContent className="pt-0">
                  <div className="text-[13px] font-medium">{project.name}</div>
                  <div className="mt-0.5 line-clamp-2 text-[11px] text-black/45 dark:text-white/40">
                    {project.description || "No description"}
                  </div>
                  <div className="mt-2 text-[10px] text-black/40 dark:text-white/35">
                    created {new Date(project.createdAt).toLocaleDateString()}
                  </div>
                </CardContent>
              </Card>
            </Link>
          ))}
        </div>
      )}

      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="New project"
        description="Every project starts with a production environment."
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={!name.trim() || create.isPending}
              onClick={() => create.mutate()}
            >
              Create project
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div>
            <Label htmlFor="project-name">Name</Label>
            <Input
              id="project-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Acme storefront"
              autoFocus
            />
          </div>
          <div>
            <Label htmlFor="project-description">Description</Label>
            <Input
              id="project-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Customer facing storefront and API"
            />
          </div>
          {create.isError && (
            <p className="text-[11px] text-danger">{String(create.error)}</p>
          )}
        </div>
      </Modal>
    </>
  );
}

export const Route = createFileRoute("/projects")({
  component: ProjectsPage,
});
