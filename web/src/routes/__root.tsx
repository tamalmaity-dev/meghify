import { Outlet, createRootRoute } from "@tanstack/react-router";
import { AuthWall, AppShell } from "@/components/app-shell";

export function RootLayout() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}

export const Route = createRootRoute({ component: RootLayout });
