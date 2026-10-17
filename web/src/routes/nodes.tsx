import { Outlet, createFileRoute } from "@tanstack/react-router";

/**
 * The /nodes branch. The server fleet lives at the index route and each
 * server gets a detail page with its overview, connection and terminal.
 */
export const Route = createFileRoute("/nodes")({
  component: () => <Outlet />,
});
