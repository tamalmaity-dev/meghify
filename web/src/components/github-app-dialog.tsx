import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input, Label, Select } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import type { GitHubApp } from "@/lib/types";

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error));

/** GitHub app names are capped at 34 characters and must be globally unique. */
function suggestedName() {
  const host = window.location.hostname.replace(/[^a-z0-9-]/gi, "-").toLowerCase();
  return `meghify-${host}`.slice(0, 34);
}

/**
 * The "New GitHub App" dialog shared by the Sources page and the project
 * resource chooser. Creating registers a placeholder source and sends the
 * browser to its setup page, which walks through GitHub.
 */
export function GitHubAppDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const [selfHosted, setSelfHosted] = useState(false);
  const [form, setForm] = useState({ name: suggestedName(), organization: "", baseUrl: "" });

  const create = useMutation({
    mutationFn: () =>
      api.post<GitHubApp>("/github/apps", {
        name: form.name.trim(),
        organization: form.organization.trim(),
        baseUrl: selfHosted ? form.baseUrl.trim() : "",
      }),
    onSuccess: async (app) => {
      await queryClient.invalidateQueries({ queryKey: ["github-apps"] });
      setForm({ name: suggestedName(), organization: "", baseUrl: "" });
      setSelfHosted(false);
      onClose();
      navigate({ to: "/sources/$appId", params: { appId: app.id } });
    },
  });

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="New GitHub App"
      description="Meghify creates the app on GitHub for you with the permissions it needs — nothing is installed until you confirm on GitHub."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={
              form.name.trim() === "" || (selfHosted && form.baseUrl.trim() === "") || create.isPending
            }
            onClick={() => create.mutate()}
          >
            Continue
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div>
          <Label htmlFor="github-app-name">Name</Label>
          <Input
            id="github-app-name"
            value={form.name}
            maxLength={34}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            placeholder="meghify-my-vps"
            autoFocus
          />
          <p className="mt-1 text-[10.5px] text-black/40 dark:text-white/30">
            The app name must be unique on GitHub and cannot be longer than 34 characters.
          </p>
        </div>
        <div>
          <Label htmlFor="github-app-org">Organization (optional)</Label>
          <Input
            id="github-app-org"
            value={form.organization}
            onChange={(event) => setForm({ ...form, organization: event.target.value })}
            placeholder="Leave empty to use your personal account"
          />
        </div>
        <div>
          <Label htmlFor="github-app-host">GitHub host</Label>
          <Select
            id="github-app-host"
            value={selfHosted ? "self" : "github"}
            onChange={(event) => setSelfHosted(event.target.value === "self")}
          >
            <option value="github">github.com</option>
            <option value="self">Self-hosted GitHub (Enterprise)</option>
          </Select>
          {selfHosted && (
            <Input
              className="mt-2"
              value={form.baseUrl}
              onChange={(event) => setForm({ ...form, baseUrl: event.target.value })}
              placeholder="https://git.example.com"
            />
          )}
        </div>
        {create.isError && <p className="text-[11px] text-danger">{errorText(create.error)}</p>}
      </div>
    </Modal>
  );
}
