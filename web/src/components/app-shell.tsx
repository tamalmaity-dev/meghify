import * as React from "react";
import { Link, Outlet } from "@tanstack/react-router";
import {
  Boxes,
  CircleHelp,
  Database,
  FolderKanban,
  Github,
  LayoutDashboard,
  LogOut,
  Moon,
  Server,
  Settings,
  Sun,
  TerminalSquare,
  Users,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useSession } from "@/lib/session";

/**
 * Authentication gate. The panel authenticates with a cookie, so every page load
 * has to ask the server who the caller is before rendering anything protected.
 */
export function AuthWall({ children }: { children: React.ReactNode }) {
  const { authenticated, isLoading } = useSession();

  if (isLoading) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="flex items-center gap-2 text-[13px] text-black/50 dark:text-white/45">
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-brand" />
          Loading Meghify
        </div>
      </div>
    );
  }

  if (!authenticated) {
    if (window.location.pathname !== "/login") {
      window.location.assign("/login");
    }
    return null;
  }

  return <>{children}</>;
}

const NAV = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, exact: true, adminOnly: false },
  { to: "/projects", label: "Projects", icon: FolderKanban, adminOnly: false },
  { to: "/databases", label: "Databases", icon: Database, adminOnly: false },
  { to: "/sources", label: "Sources", icon: Github, adminOnly: true },
  { to: "/nodes", label: "Servers", icon: Server, adminOnly: true },
  { to: "/terminal", label: "Terminal", icon: TerminalSquare, adminOnly: true },
  { to: "/jobs", label: "Job queue", icon: Boxes, adminOnly: true },
  { to: "/team", label: "Team", icon: Users, adminOnly: true },
  { to: "/help", label: "Help", icon: CircleHelp, adminOnly: false },
  { to: "/settings", label: "Settings", icon: Settings, adminOnly: false },
] as const;

/**
 * The global shell. Sidebar and topbar are a separate surface from the content
 * canvas so chrome reads as its own layer, and active rows are flat neutral
 * pills rather than accent gradients.
 */
export function AppShell() {
  const { user } = useSession();
  const [theme, setTheme] = useTheme();
  const nav = NAV.filter((item) => !item.adminOnly || user?.role === "admin");

  const logout = async () => {
    await api.post("/auth/logout");
    window.location.assign("/login");
  };

  return (
    <div className="flex h-screen overflow-hidden surface">
      <aside className="hidden w-52 shrink-0 flex-col border-r border-line/60 md:flex">
        <div className="flex h-12 items-center gap-2 px-3">
          <div className="flex h-6 w-6 items-center justify-center rounded-md bg-brand/15 text-brand">
            <TerminalSquare className="h-3.5 w-3.5" />
          </div>
          <span className="text-[13px] font-semibold tracking-tight">Meghify</span>
        </div>

        <nav className="flex-1 space-y-0.5 overflow-y-auto p-2">
          {nav.map(({ to, label, icon: Icon, adminOnly: _adminOnly, ...options }) => (
            <Link
              key={to}
              to={to}
              {...options}
              className="flex h-8 items-center gap-2 rounded-md px-2 text-[13px] transition-colors hover:bg-white/[0.05]"
              activeProps={{ className: "bg-white/[0.06] font-medium text-current" }}
              inactiveProps={{ className: "text-black/65 dark:text-white/55" }}
            >
              <Icon className="h-4 w-4 shrink-0 opacity-70" />
              {label}
            </Link>
          ))}
        </nav>

        <div className="border-t border-line/60 p-2">
          <div className="flex items-center gap-2 px-2 py-1.5">
            <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-white/10 text-[10px] font-medium uppercase">
              {user?.email?.slice(0, 2) ?? "?"}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[12px] font-medium">
                {user?.name || user?.email || "Signed in"}
              </div>
              <div className="truncate text-[10px] text-black/45 dark:text-white/40">
                {user?.role}
              </div>
            </div>
          </div>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line/60 px-4">
          <div className="flex items-center gap-1.5 md:hidden">
            <div className="flex h-6 w-6 items-center justify-center rounded-md bg-brand/15 text-brand">
              <TerminalSquare className="h-3.5 w-3.5" />
            </div>
            <span className="text-[13px] font-semibold">Meghify</span>
          </div>

          <MobileNav />

          <div className="flex-1" />

          <Button
            variant="ghost"
            size="icon"
            onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
            aria-label="Toggle theme"
          >
            {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </Button>

          <Button variant="ghost" size="icon" onClick={logout} aria-label="Log out">
            <LogOut className="h-4 w-4" />
          </Button>
        </header>

        <main className="min-h-0 flex-1 overflow-y-auto p-4 lg:p-6">
          <div className="mx-auto max-w-6xl">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}

function MobileNav() {
  const [open, setOpen] = React.useState(false);
  const { user } = useSession();
  const nav = NAV.filter((item) => !item.adminOnly || user?.role === "admin");
  return (
    <div className="relative md:hidden">
      <Button variant="ghost" size="icon" onClick={() => setOpen(!open)} aria-label="Menu">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M3 6h18M3 12h18M3 18h18" />
        </svg>
      </Button>
      {open && (
        <div className="absolute top-full left-0 z-40 mt-1 w-48 rounded-md bg-base p-1 shadow-[var(--shadow-dropdown)] ring-1 ring-line/70">
          {nav.map(({ to, label, icon: Icon }) => (
            <Link
              key={to}
              to={to}
              onClick={() => setOpen(false)}
              className="flex h-8 items-center gap-2 rounded px-2 text-[13px] hover:bg-white/5"
            >
              <Icon className="h-4 w-4 opacity-70" />
              {label}
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

/**
 * Theme state lives on <html> so the CSS variable ladder can switch without a
 * React re-render of the whole tree.
 */
function useTheme(): ["dark" | "light", (next: "dark" | "light") => void] {
  const [theme, setTheme] = React.useState<"dark" | "light">(() => {
    const stored = localStorage.getItem("meghify-theme");
    return stored === "light" ? "light" : "dark";
  });

  React.useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("dark", theme === "dark");
    root.classList.toggle("light", theme === "light");
    localStorage.setItem("meghify-theme", theme);
  }, [theme]);

  return [theme, setTheme];
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-[24px] font-semibold tracking-tight">{title}</h1>
        {description && (
          <p className="mt-0.5 text-[13px] text-black/50 dark:text-white/45">{description}</p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2">{actions}</div>
    </div>
  );
}
