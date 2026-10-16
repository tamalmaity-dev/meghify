import { createRootRoute } from "@tanstack/react-router";
import { AppShell } from "@/components/app-shell";

export function RootLayout() {
  return <AppShell />;
}

export const Route = createRootRoute({ component: RootLayout });
