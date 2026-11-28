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
import { useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

/** Isometric cube brand mark matching favicon.svg. */
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 32 32"
      fill="none"
      className={cn("h-6 w-6 shrink-0", className)}
      aria-hidden="true"
    >
      <rect width="32" height="32" rx="8" fill="#7c3aed" />
      <path
        d="M9 11.5 16 15l7-3.5v9L16 23l-7-3.5v-9Z"
        stroke="#fff"
        strokeWidth="1.75"
        strokeLinejoin="round"
      />
      <path
        d="M9 11.5 16 8l7 3.5L16 15l-7-3.5Z"
        stroke="#fff"
        strokeWidth="1.75"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/** Instant light/dark mode toggle button usable in header and login screen. */
export function ThemeToggle({ className }: { className?: string }) {
  const { resolvedTheme, toggleTheme } = useTheme();
  const isDark = resolvedTheme === "dark";

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={toggleTheme}
      className={className}
      title={isDark ? "Switch to light mode" : "Switch to dark mode"}
      aria-label={isDark ? "Switch to light mode" : "Switch to dark mode"}
    >
      {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
    </Button>
  );
}

/**
 * Authentication gate. The panel authenticates with a cookie, so every page load
 * has to ask the server who the caller is before rendering anything protected.
 */
export function AuthWall({ children }: { children: React.ReactNode }) {
  const { authenticated, isLoading } = useSession();

  if (isLoading) {
    return (
      <div className="flex h-screen items-center justify-center bg-app">
        <div className="flex items-center gap-2.5 rounded-full border border-line/70 bg-base px-4 py-2 text-[13px] text-muted shadow-xs">
          <span className="h-2 w-2 animate-pulse rounded-full bg-brand" />
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

interface NavItem {
  to: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  exact?: boolean;
  adminOnly: boolean;
  group: "workspace" | "infrastructure" | "panel";
}

const NAV: readonly NavItem[] = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, exact: true, adminOnly: false, group: "workspace" },
  { to: "/projects", label: "Projects", icon: FolderKanban, adminOnly: false, group: "workspace" },
  { to: "/databases", label: "Databases", icon: Database, adminOnly: false, group: "workspace" },
  { to: "/sources", label: "Sources", icon: Github, adminOnly: true, group: "infrastructure" },
  { to: "/nodes", label: "Servers", icon: Server, adminOnly: true, group: "infrastructure" },
  { to: "/terminal", label: "Terminal", icon: TerminalSquare, adminOnly: true, group: "infrastructure" },
  { to: "/jobs", label: "Job queue", icon: Boxes, adminOnly: true, group: "infrastructure" },
  { to: "/team", label: "Team", icon: Users, adminOnly: true, group: "panel" },
  { to: "/help", label: "Help", icon: CircleHelp, adminOnly: false, group: "panel" },
  { to: "/settings", label: "Settings", icon: Settings, adminOnly: false, group: "panel" },
];

const GROUP_LABELS: Record<NavItem["group"], string> = {
  workspace: "Workspace",
  infrastructure: "Infrastructure",
  panel: "Panel",
};

/**
 * The global shell. Sidebar and topbar sit on `bg-surface` while the scrollable
 * content canvas sits on `bg-app` and cards sit on `bg-base`, giving a crisp
 * three-layer hierarchy in both light and dark modes.
 */
export function AppShell() {
  const { user, version } = useSession();
  const nav = NAV.filter((item) => !item.adminOnly || user?.role === "admin");

  const groups = (["workspace", "infrastructure", "panel"] as const)
    .map((group) => ({
      key: group,
      label: GROUP_LABELS[group],
      items: nav.filter((item) => item.group === group),
    }))
    .filter((group) => group.items.length > 0);

  const logout = async () => {
    await api.post("/auth/logout");
    window.location.assign("/login");
  };

  return (
    <div className="flex h-screen overflow-hidden bg-app text-ink">
      <aside className="hidden w-56 shrink-0 flex-col border-r border-line/70 bg-surface md:flex">
        <div className="flex h-13 items-center justify-between border-b border-line/50 px-3.5">
          <Link to="/" className="flex items-center gap-2.5">
            <BrandMark />
            <div className="leading-none">
              <div className="text-[13.5px] font-semibold tracking-tight text-ink">Meghify</div>
              {version && (
                <div className="mt-0.5 font-mono text-[10px] text-faint">v{version}</div>
              )}
            </div>
          </Link>
        </div>

        <nav className="flex-1 space-y-4 overflow-y-auto px-2.5 py-3">
          {groups.map((group) => (
            <div key={group.key}>
              <div className="mb-1 px-2 text-[10px] font-semibold uppercase tracking-wider text-faint">
                {group.label}
              </div>
              <div className="space-y-0.5">
                {group.items.map(({ to, label, icon: Icon, exact }) => (
                  <Link
                    key={to}
                    to={to}
                    activeOptions={exact ? { exact: true } : undefined}
                    className="flex h-8.5 items-center gap-2.5 rounded-lg px-2.5 text-[13px] transition-colors"
                    activeProps={{
                      className: "bg-brand/12 font-medium text-brand",
                    }}
                    inactiveProps={{
                      className: "text-muted hover:bg-recessed hover:text-ink",
                    }}
                  >
                    <Icon className="h-4 w-4 shrink-0" />
                    {label}
                  </Link>
                ))}
              </div>
            </div>
          ))}
        </nav>

        <div className="border-t border-line/60 p-2.5">
          <Link
            to="/settings"
            className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 transition-colors hover:bg-recessed"
          >
            <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-line/70 bg-recessed text-[10.5px] font-semibold uppercase text-ink">
              {(user?.name || user?.email || "?").slice(0, 2)}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[12px] font-medium text-ink">
                {user?.name || user?.email || "Signed in"}
              </div>
              <div className="truncate text-[10.5px] capitalize text-faint">{user?.role}</div>
            </div>
          </Link>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-13 shrink-0 items-center gap-2 border-b border-line/70 bg-surface/85 px-4 backdrop-blur-md">
          <div className="flex items-center gap-2 md:hidden">
            <BrandMark className="h-6 w-6" />
            <span className="text-[13.5px] font-semibold tracking-tight">Meghify</span>
          </div>

          <MobileNav />

          <div className="flex-1" />

          <ThemeToggle />

          <Button
            variant="ghost"
            size="icon"
            onClick={logout}
            title="Sign out"
            aria-label="Log out"
          >
            <LogOut className="h-4 w-4" />
          </Button>
        </header>

        <main className="min-h-0 flex-1 overflow-y-auto bg-app p-4 lg:p-6">
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
  const ref = React.useRef<HTMLDivElement>(null);
  const nav = NAV.filter((item) => !item.adminOnly || user?.role === "admin");

  React.useEffect(() => {
    if (!open) return;
    const onMouseDown = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onMouseDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onMouseDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  return (
    <div className="relative md:hidden" ref={ref}>
      <Button variant="ghost" size="icon" onClick={() => setOpen(!open)} aria-label="Menu">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M3 6h18M3 12h18M3 18h18" />
        </svg>
      </Button>
      {open && (
        <div className="absolute top-full left-0 z-40 mt-1.5 w-52 rounded-xl border border-line/80 bg-base p-1.5 shadow-[var(--shadow-dropdown)]">
          {nav.map(({ to, label, icon: Icon, exact }) => (
            <Link
              key={to}
              to={to}
              activeOptions={exact ? { exact: true } : undefined}
              onClick={() => setOpen(false)}
              className="flex h-8.5 items-center gap-2.5 rounded-lg px-2.5 text-[13px] transition-colors"
              activeProps={{ className: "bg-brand/12 font-medium text-brand" }}
              inactiveProps={{ className: "text-muted hover:bg-recessed hover:text-ink" }}
            >
              <Icon className="h-4 w-4 shrink-0" />
              {label}
            </Link>
          ))}
        </div>
      )}
    </div>
  );
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
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-[22px] font-semibold tracking-tight text-ink">{title}</h1>
        {description && (
          <p className="mt-1 text-[13px] text-muted">{description}</p>
        )}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}
