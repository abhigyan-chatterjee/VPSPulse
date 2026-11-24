import { LogOut, Moon, Plus, Sun } from "lucide-react";

export type TopView = "overview" | "detail" | "incidents" | "settings";

interface LayoutProps {
  view: TopView;
  onNavigate: (view: TopView) => void;
  openIncidents: number;
  sseConnected: boolean;
  dark: boolean;
  onToggleTheme: () => void;
  onOpenAddVps: () => void;
  onLogout: () => void;
  children: React.ReactNode;
}

export function Layout({
  view,
  onNavigate,
  openIncidents,
  sseConnected,
  dark,
  onToggleTheme,
  onOpenAddVps,
  onLogout,
  children,
}: LayoutProps) {
  const navBtn = (id: TopView, label: string, badge?: number) => {
    const active = view === id || (view === "detail" && id === "overview");
    return (
      <button
        onClick={() => onNavigate(id)}
        className={`px-3 py-1.5 rounded-md text-xs font-medium cursor-pointer transition-colors flex items-center gap-1.5 ${
          active
            ? "bg-[var(--secondary)] text-[var(--fg)] border border-[var(--border)]"
            : "text-[var(--muted)] hover:text-[var(--fg)] border border-transparent"
        }`}
      >
        {label}
        {badge !== undefined && badge > 0 && (
          <span className="px-1.5 py-0.2 rounded-full text-xs font-mono bg-[var(--warning)] text-[var(--primary-fg)]">
            {badge}
          </span>
        )}
      </button>
    );
  };

  return (
    <div className="min-h-screen flex flex-col">
      <header className="sticky top-0 z-30 border-b border-[var(--border)] bg-[var(--sidebar)]">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 h-14 flex items-center justify-between gap-4">
          <div className="flex items-center gap-6">
            <button
              onClick={() => onNavigate("overview")}
              className="flex items-center gap-2.5 text-left cursor-pointer"
            >
              <span className="relative flex h-3 w-3">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-[var(--success)] opacity-60" />
                <span className="relative inline-flex rounded-full h-3 w-3 bg-[var(--success)]" />
              </span>
              <span className="font-semibold text-base tracking-tight">VPSPulse</span>
            </button>

            <nav className="flex items-center gap-1">
              {navBtn("overview", "Overview")}
              {navBtn("incidents", "Incidents", openIncidents)}
              {navBtn("settings", "Settings")}
            </nav>
          </div>

          <div className="flex items-center gap-2">
            <span
              title={sseConnected ? "Live SSE stream connected" : "Reconnecting live stream..."}
              className="hidden sm:inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs border border-[var(--border)] bg-[var(--card)]"
            >
              <span
                className={`h-2 w-2 rounded-full ${sseConnected ? "bg-[var(--success)]" : "bg-[var(--warning)]"}`}
              />
              <span>{sseConnected ? "Live" : "Syncing"}</span>
            </span>

            <button
              onClick={onToggleTheme}
              title="Toggle light/dark theme"
              className="p-2 rounded-md border border-[var(--border)] bg-[var(--card)] text-[var(--muted)] hover:text-[var(--fg)] cursor-pointer"
            >
              {dark ? <Sun className="w-3.5 h-3.5" /> : <Moon className="w-3.5 h-3.5" />}
            </button>

            <button
              onClick={onOpenAddVps}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-[var(--primary)] text-[var(--primary-fg)] hover:opacity-90 cursor-pointer"
            >
              <Plus className="w-3.5 h-3.5" />
              Add VPS
            </button>

            <button
              onClick={onLogout}
              title="Sign out"
              className="p-2 rounded-md border border-[var(--border)] bg-[var(--card)] text-[var(--muted)] hover:text-[var(--fg)] cursor-pointer"
            >
              <LogOut className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      </header>

      <main className="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 py-6">{children}</main>
    </div>
  );
}
