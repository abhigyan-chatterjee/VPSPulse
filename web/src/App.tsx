import { useCallback, useEffect, useRef, useState } from "react";
import { api, setUnauthorizedHandler } from "./api/client";
import type { AuthStatus, Incident, Probe, VPS } from "./types";
import { Layout, type TopView } from "./components/Layout";
import { AddVpsModal } from "./components/AddVpsModal";
import { AddProbeModal } from "./components/AddProbeModal";
import { AuthPage } from "./pages/AuthPage";
import { OverviewPage } from "./pages/OverviewPage";
import { VpsDetailPage } from "./pages/VpsDetailPage";
import { IncidentsPage } from "./pages/IncidentsPage";
import { SettingsPage } from "./pages/SettingsPage";

function upsertById<T extends { id: string }>(list: T[], item: T): T[] {
  let replaced = false;
  const out: T[] = [];
  for (const existing of list) {
    if (existing.id === item.id) {
      if (!replaced) {
        out.push(item);
        replaced = true;
      }
    } else {
      out.push(existing);
    }
  }
  if (!replaced) {
    out.push(item);
  }
  return out;
}

export function App() {
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [bootError, setBootError] = useState<string | null>(null);
  const [view, setView] = useState<TopView>("overview");
  const [selectedVpsId, setSelectedVpsId] = useState<string | null>(null);
  const [vpsList, setVpsList] = useState<VPS[]>([]);
  const [standaloneProbes, setStandaloneProbes] = useState<Probe[]>([]);
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [sseConnected, setSseConnected] = useState(false);
  const [addVpsOpen, setAddVpsOpen] = useState(false);
  const [probeModalVpsId, setProbeModalVpsId] = useState<string | null | undefined>(undefined);
  const [toast, setToast] = useState<string | null>(null);
  const toastTimerRef = useRef<number | null>(null);
  const [dark, setDark] = useState(() => {
    try {
      const saved = localStorage.getItem("vpspulse_theme");
      return saved ? saved === "dark" : true;
    } catch {
      return true;
    }
  });

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    try {
      localStorage.setItem("vpspulse_theme", dark ? "dark" : "light");
    } catch {
      // ignore storage errors in private contexts
    }
  }, [dark]);

  useEffect(() => {
    return () => {
      if (toastTimerRef.current !== null) window.clearTimeout(toastTimerRef.current);
    };
  }, []);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    if (toastTimerRef.current !== null) window.clearTimeout(toastTimerRef.current);
    toastTimerRef.current = window.setTimeout(() => {
      setToast(null);
    }, 2800);
  }, []);

  const loadAuth = useCallback(async () => {
    const st = await api.authStatus();
    setAuth(st);
    setBootError(null);
    return st;
  }, []);

  const loadData = useCallback(async () => {
    const [v, sp, inc] = await Promise.all([
      api.listVPS(),
      api.listProbes(""),
      api.listIncidents("all"),
    ]);
    setVpsList(v);
    setStandaloneProbes(sp);
    setIncidents(inc);
  }, []);

  const bootstrap = useCallback(async () => {
    setBootError(null);
    try {
      const st = await loadAuth();
      if (st.authenticated) {
        await loadData();
      }
    } catch (err) {
      setBootError(err instanceof Error ? err.message : "Failed to connect to VPSPulse hub");
    }
  }, [loadAuth, loadData]);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      setAuth((prev) => (prev ? { ...prev, authenticated: false, username: "" } : prev));
      setSseConnected(false);
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  useEffect(() => {
    bootstrap();
  }, [bootstrap]);

  // Live SSE updates when authenticated.
  useEffect(() => {
    if (!auth?.authenticated) return;
    let hadError = false;
    let lastAuthCheck = 0;

    const es = new EventSource("/api/v1/events", { withCredentials: true });
    es.onopen = () => {
      setSseConnected(true);
      if (hadError) {
        hadError = false;
        loadData().catch(() => {});
      }
    };
    es.onerror = () => {
      setSseConnected(false);
      hadError = true;
      const now = Date.now();
      if (now - lastAuthCheck > 5000) {
        lastAuthCheck = now;
        loadAuth().catch(() => {});
      }
    };

    es.addEventListener("connected", () => setSseConnected(true));
    es.addEventListener("vps_update", (ev) => {
      try {
        const updated = JSON.parse((ev as MessageEvent).data) as VPS;
        setVpsList((prev) => upsertById(prev, updated));
      } catch {
        // ignore malformed event
      }
    });
    es.addEventListener("vps_deleted", (ev) => {
      try {
        const { id } = JSON.parse((ev as MessageEvent).data) as { id: string };
        setVpsList((prev) => prev.filter((x) => x.id !== id));
        setSelectedVpsId((cur) => {
          if (cur === id) {
            setView("overview");
            showToast("VPS was deleted");
            return null;
          }
          return cur;
        });
      } catch {
        // ignore
      }
    });
    es.addEventListener("probe_update", (ev) => {
      try {
        const prb = JSON.parse((ev as MessageEvent).data) as Probe;
        if (!prb.vps_id) {
          setStandaloneProbes((prev) => upsertById(prev, prb));
        } else {
          setVpsList((prev) =>
            prev.map((v) =>
              v.id === prb.vps_id ? { ...v, probes: upsertById(v.probes, prb) } : v,
            ),
          );
        }
      } catch {
        // ignore
      }
    });
    es.addEventListener("probe_deleted", (ev) => {
      try {
        const { id } = JSON.parse((ev as MessageEvent).data) as { id: string };
        setStandaloneProbes((prev) => prev.filter((p) => p.id !== id));
        setVpsList((prev) =>
          prev.map((v) => ({ ...v, probes: v.probes.filter((p) => p.id !== id) })),
        );
      } catch {
        // ignore
      }
    });
    es.addEventListener("incident_update", (ev) => {
      try {
        const inc = JSON.parse((ev as MessageEvent).data) as Incident;
        setIncidents((prev) => {
          const rest = prev.filter((x) => x.id !== inc.id);
          return [inc, ...rest];
        });
      } catch {
        // ignore
      }
    });

    return () => {
      es.close();
      setSseConnected(false);
    };
  }, [auth?.authenticated, loadAuth, loadData, showToast]);

  if (bootError && !auth) {
    return (
      <div className="min-h-screen flex items-center justify-center p-4 bg-[var(--bg)]">
        <div className="max-w-sm w-full rounded-xl border border-[var(--border)] bg-[var(--card)] p-6 space-y-3 text-xs text-center">
          <p className="font-semibold text-[var(--error)]">Unable to reach VPSPulse</p>
          <p className="text-[var(--muted)]">{bootError}</p>
          <button
            onClick={bootstrap}
            className="px-3.5 py-1.5 rounded bg-[var(--primary)] text-[var(--primary-fg)] font-medium cursor-pointer"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!auth) {
    return (
      <div className="min-h-screen flex items-center justify-center text-xs text-[var(--muted)]">
        Loading VPSPulse...
      </div>
    );
  }

  if (!auth.authenticated) {
    return (
      <AuthPage
        status={auth}
        onSuccess={async () => {
          await loadAuth();
          await loadData();
        }}
      />
    );
  }

  const activeVps = vpsList.find((v) => v.id === selectedVpsId) || null;
  const openIncidents = incidents.filter((i) => i.status === "open").length;

  const handleLogout = async () => {
    await api.logout().catch(() => {});
    setSseConnected(false);
    await loadAuth();
  };

  return (
    <Layout
      view={view}
      onNavigate={(next) => {
        setView(next);
        if (next !== "detail") setSelectedVpsId(null);
      }}
      openIncidents={openIncidents}
      sseConnected={sseConnected}
      dark={dark}
      onToggleTheme={() => setDark((d) => !d)}
      onOpenAddVps={() => setAddVpsOpen(true)}
      onLogout={handleLogout}
    >
      {bootError && (
        <div className="mb-4 rounded-lg border border-[var(--error)]/40 bg-[var(--error)]/10 px-3.5 py-2 text-xs text-[var(--error)] flex items-center justify-between">
          <span>{bootError}</span>
          <button onClick={bootstrap} className="underline cursor-pointer">
            Retry
          </button>
        </div>
      )}

      {view === "overview" && (
        <OverviewPage
          vpsList={vpsList}
          standaloneProbes={standaloneProbes}
          incidents={incidents}
          onSelectVps={(id) => {
            setSelectedVpsId(id);
            setView("detail");
          }}
          onAddStandaloneProbe={() => setProbeModalVpsId(null)}
          onAddVps={() => setAddVpsOpen(true)}
          onProbeUpdated={(prb) => setStandaloneProbes((prev) => upsertById(prev, prb))}
          onProbeDeleted={(id) => setStandaloneProbes((prev) => prev.filter((p) => p.id !== id))}
          onToast={showToast}
        />
      )}

      {view === "detail" && activeVps && (
        <VpsDetailPage
          key={activeVps.id}
          vps={activeVps}
          publicUrl={auth.public_url}
          onBack={() => {
            setSelectedVpsId(null);
            setView("overview");
          }}
          onUpdated={(updated) => setVpsList((prev) => upsertById(prev, updated))}
          onDeleted={(id) => {
            setVpsList((prev) => prev.filter((x) => x.id !== id));
            setSelectedVpsId(null);
            setView("overview");
            showToast("VPS deleted");
          }}
          onAddProbe={(vpsId) => setProbeModalVpsId(vpsId)}
          onToast={showToast}
        />
      )}

      {view === "incidents" && (
        <IncidentsPage
          incidents={incidents}
          vpsList={vpsList}
          onRefresh={() => api.listIncidents("all").then(setIncidents).catch(() => {})}
          onToast={showToast}
        />
      )}

      {view === "settings" && <SettingsPage onToast={showToast} />}

      <AddVpsModal
        open={addVpsOpen}
        publicUrl={auth.public_url}
        onClose={() => setAddVpsOpen(false)}
        onCreated={(v) => setVpsList((prev) => upsertById(prev, v))}
        onToast={showToast}
      />

      {probeModalVpsId !== undefined && (
        <AddProbeModal
          vpsId={probeModalVpsId}
          onClose={() => setProbeModalVpsId(undefined)}
          onCreated={(prb) => {
            if (!prb.vps_id) {
              setStandaloneProbes((prev) => upsertById(prev, prb));
            } else {
              setVpsList((prev) =>
                prev.map((v) =>
                  v.id === prb.vps_id ? { ...v, probes: upsertById(v.probes, prb) } : v,
                ),
              );
            }
            showToast(`Probe ${prb.name} created`);
          }}
        />
      )}

      {toast && (
        <div className="fixed bottom-5 right-5 z-50 px-3.5 py-2.5 rounded-lg border border-[var(--border)] bg-[var(--card)] text-xs shadow-xl flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-[var(--success)]" />
          <span>{toast}</span>
        </div>
      )}
    </Layout>
  );
}

export default App;
