import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ChevronRight, FolderKanban, Plus } from "lucide-react";
import { useState } from "react";
import { api } from "@/lib/api";
import { useSession } from "@/lib/session";
import { PageHeader } from "@/components/app-shell";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { EmptyState } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import type { Project } from "@/lib/types";
import { createFileRoute } from "@tanstack/react-router";

export function ProjectsPage() {
  const queryClient = useQueryClient();
  const { user } = useSession();
  const isAdmin = user?.role === "admin";
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
          isAdmin ? (
            <Button variant="primary" onClick={() => setOpen(true)}>
              <Plus className="h-4 w-4" />
              New project
            </Button>
          ) : undefined
        }
      />

      {(projects.data?.length ?? 0) === 0 ? (
        <Card>
          <EmptyState
            icon={<FolderKanban className="h-5 w-5" />}
            title="No projects yet"
            description={
              isAdmin
                ? "A project holds one or more environments. Create one, then add an application or a database."
                : "No projects are available yet. An administrator can create one."
            }
            action={
              isAdmin ? (
                <Button variant="primary" onClick={() => setOpen(true)}>
                  Create your first project
                </Button>
              ) : undefined
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
              className="group"
            >
              <Card className="h-full transition-[border-color,transform] group-hover:-translate-y-px group-hover:border-brand/45">
                <CardContent className="flex h-full flex-col p-4">
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-brand/12 text-[11px] font-semibold text-brand">
                      {project.name.slice(0, 2).toUpperCase()}
                    </div>
                    <ChevronRight className="h-4 w-4 text-faint transition-transform group-hover:translate-x-0.5 group-hover:text-ink" />
                  </div>
                  <div className="mt-3 text-[14px] font-semibold text-ink">{project.name}</div>
                  <div className="mt-1 line-clamp-2 flex-1 text-[12px] text-muted">
                    {project.description || "No description"}
                  </div>
                  <div className="mt-3 border-t border-line/45 pt-2.5 text-[10.5px] text-faint">
                    Created {new Date(project.createdAt).toLocaleDateString()}
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

export const Route = createFileRoute("/projects/")({
  component: ProjectsPage,
});
