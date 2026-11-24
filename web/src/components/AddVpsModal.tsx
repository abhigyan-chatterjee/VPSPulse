import { useState } from "react";
import { Check, Copy, X } from "lucide-react";
import { api } from "../api/client";
import type { VPS } from "../types";

interface AddVpsModalProps {
  open: boolean;
  publicUrl: string;
  onClose: () => void;
  onCreated: (vps: VPS) => void;
  onToast: (msg: string) => void;
}

export function AddVpsModal({ open, publicUrl, onClose, onCreated, onToast }: AddVpsModalProps) {
  const [name, setName] = useState("");
  const [tag, setTag] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  if (!open) return null;

  const baseUrl = publicUrl || window.location.origin;
  const installCmd = createdToken
    ? `curl -fsSL ${baseUrl}/install.sh | sudo sh -s -- \\\n  --url ${baseUrl} --token ${createdToken}`
    : "";

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api.createVPS(name, tag);
      setCreatedToken(res.token);
      onCreated(res.vps);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create VPS");
    } finally {
      setBusy(false);
    }
  };

  const handleCopy = async () => {
    if (!installCmd) return;
    await navigator.clipboard.writeText(installCmd).catch(() => {});
    setCopied(true);
    onToast("Install command copied");
    setTimeout(() => setCopied(false), 2000);
  };

  const resetAndClose = () => {
    setName("");
    setTag("");
    setCreatedToken(null);
    setError("");
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs">
      <div className="w-full max-w-lg rounded-xl border border-[var(--border)] bg-[var(--card)] shadow-2xl overflow-hidden">
        <div className="px-5 py-4 border-b border-[var(--border)] flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold text-[var(--fg)]">Connect a New VPS</h2>
            <p className="text-xs text-[var(--muted)] mt-0.5">
              Creates a per-VPS token and one-line Linux agent installer.
            </p>
          </div>
          <button
            onClick={resetAndClose}
            className="p-1.5 rounded-md text-[var(--muted)] hover:text-[var(--fg)] hover:bg-[var(--secondary)] cursor-pointer"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {!createdToken ? (
          <form onSubmit={handleSubmit} className="p-5 space-y-4">
            {error && (
              <div className="p-2.5 rounded-md bg-[var(--error)]/15 text-[var(--error)] text-xs">{error}</div>
            )}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label className="block text-xs font-medium text-[var(--muted)] mb-1">Display Name</label>
                <input
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="prod-eu-1"
                  className="w-full px-3 py-2 rounded-md text-xs bg-[var(--bg)] border border-[var(--border)] text-[var(--fg)] focus:outline-none focus:border-[var(--primary)]"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-[var(--muted)] mb-1">Region / Tag</label>
                <input
                  value={tag}
                  onChange={(e) => setTag(e.target.value)}
                  placeholder="eu-central"
                  className="w-full px-3 py-2 rounded-md text-xs bg-[var(--bg)] border border-[var(--border)] text-[var(--fg)] focus:outline-none focus:border-[var(--primary)]"
                />
              </div>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={resetAndClose}
                className="px-3 py-1.5 rounded-md text-xs border border-[var(--border)] hover:bg-[var(--secondary)] cursor-pointer"
              >
                Cancel
              </button>
              <button
                disabled={busy}
                type="submit"
                className="px-3.5 py-1.5 rounded-md text-xs font-medium bg-[var(--primary)] text-[var(--primary-fg)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
              >
                {busy ? "Creating..." : "Create & Generate Token"}
              </button>
            </div>
          </form>
        ) : (
          <div className="p-5 space-y-4">
            <div className="rounded-lg border border-[var(--border)] bg-[var(--bg)] p-3.5 space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium">Run on your VPS (Linux amd64 / arm64)</span>
                <span className="text-xs font-mono text-[var(--muted)]">systemd · ~12 MB RAM</span>
              </div>
              <pre className="text-xs font-mono text-[var(--fg)] overflow-x-auto whitespace-pre-wrap break-all leading-relaxed select-all">
                {installCmd}
              </pre>
              <div className="flex items-center justify-between pt-1">
                <span className="text-xs text-[var(--warning)]">Token shown once — copy it now.</span>
                <button
                  type="button"
                  onClick={handleCopy}
                  className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded text-xs font-medium bg-[var(--secondary)] border border-[var(--border)] hover:opacity-90 cursor-pointer"
                >
                  {copied ? <Check className="w-3.5 h-3.5 text-[var(--success)]" /> : <Copy className="w-3.5 h-3.5" />}
                  {copied ? "Copied" : "Copy"}
                </button>
              </div>
            </div>
            <div className="flex justify-end">
              <button
                type="button"
                onClick={resetAndClose}
                className="px-3.5 py-1.5 rounded-md text-xs font-medium bg-[var(--primary)] text-[var(--primary-fg)] cursor-pointer"
              >
                Done
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
