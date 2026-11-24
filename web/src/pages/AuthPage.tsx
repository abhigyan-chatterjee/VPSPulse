import { useState } from "react";
import { api } from "../api/client";
import type { AuthStatus } from "../types";

interface AuthPageProps {
  status: AuthStatus;
  onSuccess: () => void;
}

export function AuthPage({ status, onSuccess }: AuthPageProps) {
  const [username, setUsername] = useState(status.demo_mode ? "admin" : "admin");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      if (status.needs_setup) {
        await api.setupAdmin(username, password);
      } else {
        await api.login(username, password);
      }
      onSuccess();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Authentication failed");
    } finally {
      setBusy(false);
    }
  };

  const fillDemo = () => {
    setUsername("admin");
    setPassword("demo1234");
  };

  return (
    <div className="min-h-screen flex items-center justify-center p-4 bg-[var(--bg)]">
      <div className="w-full max-w-sm rounded-xl border border-[var(--border)] bg-[var(--card)] p-6 shadow-xl space-y-5">
        <div className="flex items-center gap-2.5">
          <span className="h-3 w-3 rounded-full bg-[var(--success)]" />
          <div>
            <h1 className="text-base font-semibold">VPSPulse</h1>
            <p className="text-xs text-[var(--muted)]">
              {status.needs_setup ? "Create your admin account" : "Sign in to your monitoring hub"}
            </p>
          </div>
        </div>

        {status.demo_mode && !status.needs_setup && (
          <div className="rounded-lg border border-[var(--primary)]/40 bg-[var(--primary)]/10 p-3 flex items-center justify-between gap-2 text-xs">
            <div>
              <div className="font-medium text-[var(--fg)]">Demo mode active</div>
              <div className="font-mono text-[var(--muted)]">admin / demo1234</div>
            </div>
            <button
              type="button"
              onClick={fillDemo}
              className="px-2.5 py-1 rounded bg-[var(--primary)] text-[var(--primary-fg)] font-medium cursor-pointer"
            >
              Fill
            </button>
          </div>
        )}

        {error && (
          <div className="p-2.5 rounded-md bg-[var(--error)]/15 text-[var(--error)] text-xs">{error}</div>
        )}

        <form onSubmit={submit} className="space-y-3 text-xs">
          <div>
            <label className="block text-[var(--muted)] mb-1">Username</label>
            <input
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full px-3 py-2 rounded-md bg-[var(--bg)] border border-[var(--border)] focus:outline-none focus:border-[var(--primary)]"
            />
          </div>
          <div>
            <label className="block text-[var(--muted)] mb-1">Password</label>
            <input
              required
              type="password"
              minLength={6}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={status.needs_setup ? "At least 6 characters" : "Enter password"}
              className="w-full px-3 py-2 rounded-md bg-[var(--bg)] border border-[var(--border)] focus:outline-none focus:border-[var(--primary)]"
            />
          </div>
          <button
            disabled={busy}
            type="submit"
            className="w-full py-2 rounded-md font-medium bg-[var(--primary)] text-[var(--primary-fg)] hover:opacity-90 disabled:opacity-50 cursor-pointer"
          >
            {busy ? "Please wait..." : status.needs_setup ? "Create Admin & Enter" : "Sign In"}
          </button>
        </form>
      </div>
    </div>
  );
}
