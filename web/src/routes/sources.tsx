import { Outlet, createFileRoute } from "@tanstack/react-router";

/**
 * The /sources branch. The list of Git providers lives at the index route and
 * each GitHub App gets a setup page, so this layout only passes through.
 */
export const Route = createFileRoute("/sources")({
  component: () => <Outlet />,
});
