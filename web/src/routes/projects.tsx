import { Outlet, createFileRoute } from "@tanstack/react-router";

/**
 * The /projects branch. The list lives at the index route and each project's
 * detail page is a child route, so this layout only passes through — without
 * an Outlet the detail page would never mount.
 */
export const Route = createFileRoute("/projects")({
  component: () => <Outlet />,
});
