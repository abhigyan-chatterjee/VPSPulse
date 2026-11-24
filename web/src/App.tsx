import { useCallback, useEffect, useState } from "react";
import { api } from "./api/client";
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
  const [view, setView] = useState<TopView>("overview");
  const [selectedVpsId, setSelectedVpsId] = useState<string | null>(null);
  const [vpsList, setVpsList] = useState<VPS[]>([]);
  const [standaloneProbes, setStandaloneProbes] = useState<Probe[]>([]);
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [sseConnected, setSseConnected] = useState(false);
  const [addVpsOpen, setAddVpsOpen] = useState(false);
  const [probeModalVpsId, setProbeModalVpsId] = useState<string | null | undefined>(undefined);
  const [toast, setToast] = useState<string | null>(null);
  const [dark, setDark] = useState(() => {
    const saved = localStorage.getItem("vpspulse_theme");
    return saved ? saved === "dark" : true;
  });

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("vpspulse_theme", dark ? "dark" : "light");
  }, [dark]);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    setTimeout(() => {
      setToast((cur) => (cur === msg ? null : cur));
    }, 2800);
  }, []);

  const loadAuth = useCallback(async () => {
    const st = await api.authStatus();
    setAuth(st);
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

  useEffect(() => {
    loadAuth()
      .then((st) => {
        if (st.authenticated) return loadData();
      })
      .catch(() => {});
  }, [loadAuth, loadData]);

  // Live SSE updates when authenticated.
  useEffect(() => {
    if (!auth?.authenticated) return;
    const es = new EventSource("/api/v1/events", { withCredentials: true });
    es.onopen = () => setSseConnected(true);
    es.onerror = () => setSseConnected(false);

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
      } catch {}
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
      } catch {}
    });
    es.addEventListener("probe_deleted", (ev) => {
      try {
        const { id } = JSON.parse((ev as MessageEvent).data) as { id: string };
        setStandaloneProbes((prev) => prev.filter((p) => p.id !== id));
        setVpsList((prev) =>
          prev.map((v) => ({ ...v, probes: v.probes.filter((p) => p.id !== id) })),
        );
      } catch {}
    });
    es.addEventListener("incident_update", (ev) => {
      try {
        const inc = JSON.parse((ev as MessageEvent).data) as Incident;
        setIncidents((prev) => {
          const rest = prev.filter((x) => x.id !== inc.id);
          return [inc, ...rest];
        });
      } catch {}
    });

    return () => {
      es.close();
      setSseConnected(false);
    };
  }, [auth?.authenticated]);

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
        />
      )}

      {view === "detail" && activeVps && (
        <VpsDetailPage
          vps={activeVps}
          publicUrl={auth.public_url}
          onBack={() => setView("overview")}
          onUpdated={(updated) => setVpsList((prev) => prev.map((x) => (x.id === updated.id ? updated : x)))}
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

      <AddProbeModal
        open={probeModalVpsId !== undefined}
        vpsId={probeModalVpsId ?? null}
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
