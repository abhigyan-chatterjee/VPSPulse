import { useState } from "react";
import { X } from "lucide-react";
import { api } from "../api/client";
import type { Probe } from "../types";

interface AddProbeModalProps {
  open: boolean;
  vpsId: string | null;
  onClose: () => void;
  onCreated: (probe: Probe) => void;
}

export function AddProbeModal({ open, vpsId, onClose, onCreated }: AddProbeModalProps) {
  const [name, setName] = useState("");
  const [type, setType] = useState<"http" | "tcp" | "icmp">("http");
  const [target, setTarget] = useState("");
  const [keyword, setKeyword] = useState("");
  const [intervalSec, setIntervalSec] = useState(30);
  const [failureThreshold, setFailureThreshold] = useState(2);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  if (!open) return null;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const created = await api.createProbe({
        vps_id: vpsId,
        name,
        type,
        target,
        keyword,
        interval_sec: Number(intervalSec),
        failure_threshold: Number(failureThreshold),
        enabled: true,
      });
      setName("");
      setTarget("");
      setKeyword("");
      onCreated(created);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create probe");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs">
      <div className="w-full max-w-md rounded-xl border border-[var(--border)] bg-[var(--card)] shadow-2xl overflow-hidden">
        <div className="px-5 py-4 border-b border-[var(--border)] flex items-center justify-between">
          <h2 className="text-sm font-semibold">Add External Service Probe</h2>
          <button onClick={onClose} className="p-1 text-[var(--muted)] hover:text-[var(--fg)] cursor-pointer">
            <X className="w-4 h-4" />
          </button>
        </div>
        <form onSubmit={submit} className="p-5 space-y-3 text-xs">
          {error && <div className="p-2 rounded bg-[var(--error)]/15 text-[var(--error)]">{error}</div>}
          <div>
            <label className="block text-[var(--muted)] mb-1">Service Name</label>
            <input
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="api.myapp.dev"
              className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)]"
            />
          </div>
          <div className="grid grid-cols-3 gap-2">
            <div>
              <label className="block text-[var(--muted)] mb-1">Type</label>
              <select
                value={type}
                onChange={(e) => setType(e.target.value as "http" | "tcp" | "icmp")}
                className="w-full px-2.5 py-2 rounded bg-[var(--bg)] border border-[var(--border)]"
              >
                <option value="http">HTTP / HTTPS</option>
                <option value="tcp">TCP Port</option>
                <option value="icmp">ICMP Ping</option>
              </select>
            </div>
            <div className="col-span-2">
              <label className="block text-[var(--muted)] mb-1">
                {type === "http" ? "URL" : type === "tcp" ? "Host:Port" : "Host / IP"}
              </label>
              <input
                required
                value={target}
                onChange={(e) => setTarget(e.target.value)}
                placeholder={type === "http" ? "https://api.myapp.dev/health" : type === "tcp" ? "1.2.3.4:5432" : "1.2.3.4"}
                className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
              />
            </div>
          </div>
          {type === "http" && (
            <div>
              <label className="block text-[var(--muted)] mb-1">Response Body Keyword (optional)</label>
              <input
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
                placeholder="ok"
                className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
              />
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[var(--muted)] mb-1">Check Interval (sec)</label>
              <input
                type="number"
                min={10}
                value={intervalSec}
                onChange={(e) => setIntervalSec(Number(e.target.value))}
                className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
              />
            </div>
            <div>
              <label className="block text-[var(--muted)] mb-1">Failures Before Alert</label>
              <input
                type="number"
                min={1}
                value={failureThreshold}
                onChange={(e) => setFailureThreshold(Number(e.target.value))}
                className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
              />
            </div>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 rounded border border-[var(--border)] cursor-pointer"
            >
              Cancel
            </button>
            <button
              disabled={busy}
              type="submit"
              className="px-3.5 py-1.5 rounded bg-[var(--primary)] text-[var(--primary-fg)] font-medium cursor-pointer"
            >
              {busy ? "Creating..." : "Create Probe"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
