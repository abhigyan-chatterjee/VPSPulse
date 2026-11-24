import { useEffect, useState } from "react";
import { Plus, Send, Trash2 } from "lucide-react";
import { api, formatBytes } from "../api/client";
import type { NotificationChannel, SettingsInfo } from "../types";

interface SettingsPageProps {
  onToast: (msg: string) => void;
}

export function SettingsPage({ onToast }: SettingsPageProps) {
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [info, setInfo] = useState<SettingsInfo | null>(null);

  // New channel form
  const [showAdd, setShowAdd] = useState(false);
  const [chName, setChName] = useState("");
  const [chType, setChType] = useState<"discord" | "telegram" | "slack" | "webhook">("discord");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [botToken, setBotToken] = useState("");
  const [chatId, setChatId] = useState("");
  const [secret, setSecret] = useState("");

  // Password form
  const [curPass, setCurPass] = useState("");
  const [newPass, setNewPass] = useState("");

  const load = async () => {
    const [ch, st] = await Promise.all([api.listChannels(), api.getSettings()]);
    setChannels(ch);
    setInfo(st);
  };

  useEffect(() => {
    load().catch(() => {});
  }, []);

  const handleCreateChannel = async (e: React.FormEvent) => {
    e.preventDefault();
    const config: Record<string, string> =
      chType === "telegram"
        ? { bot_token: botToken, chat_id: chatId }
        : chType === "webhook"
          ? { url: webhookUrl, secret }
          : { webhook_url: webhookUrl };

    try {
      await api.createChannel({
        name: chName,
        type: chType,
        config,
        events: { offline: true, service: true, resource: true, tls: true },
        enabled: true,
      });
      setChName("");
      setWebhookUrl("");
      setBotToken("");
      setChatId("");
      setSecret("");
      setShowAdd(false);
      await load();
      onToast("Notification channel added");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to create channel");
    }
  };

  const handleToggleChannel = async (ch: NotificationChannel) => {
    try {
      await api.updateChannel(ch.id, { ...ch, enabled: !ch.enabled });
      await load();
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to update channel");
    }
  };

  const handleToggleEvent = async (
    ch: NotificationChannel,
    evKey: keyof NotificationChannel["events"],
  ) => {
    try {
      await api.updateChannel(ch.id, {
        ...ch,
        events: { ...ch.events, [evKey]: !ch.events[evKey] },
      });
      await load();
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to update event toggle");
    }
  };

  const handleTest = async (ch: NotificationChannel) => {
    try {
      await api.testChannel(ch.id);
      onToast(`Test alert sent to ${ch.name}`);
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Test delivery failed");
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await api.deleteChannel(id);
      await load();
      onToast("Channel deleted");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to delete channel");
    }
  };

  const handleChangePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await api.changePassword(curPass, newPass);
      setCurPass("");
      setNewPass("");
      onToast("Password updated");
    } catch (err) {
      onToast(err instanceof Error ? err.message : "Failed to update password");
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-base font-semibold">Global Settings &amp; Alert Channels</h1>
          <p className="text-xs text-[var(--muted)]">
            Alerts broadcast to all enabled channels matching the event category.
          </p>
        </div>
        <button
          onClick={() => setShowAdd(!showAdd)}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-[var(--primary)] text-[var(--primary-fg)] cursor-pointer"
        >
          <Plus className="w-3.5 h-3.5" /> Add Channel
        </button>
      </div>

      {showAdd && (
        <form
          onSubmit={handleCreateChannel}
          className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4 space-y-3 text-xs"
        >
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className="block text-[var(--muted)] mb-1">Channel Name</label>
              <input
                required
                value={chName}
                onChange={(e) => setChName(e.target.value)}
                placeholder="Ops #alerts"
                className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)]"
              />
            </div>
            <div>
              <label className="block text-[var(--muted)] mb-1">Type</label>
              <select
                value={chType}
                onChange={(e) => setChType(e.target.value as typeof chType)}
                className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)]"
              >
                <option value="discord">Discord Webhook</option>
                <option value="telegram">Telegram Bot</option>
                <option value="slack">Slack Incoming Webhook</option>
                <option value="webhook">Generic JSON Webhook</option>
              </select>
            </div>
          </div>

          {chType === "telegram" ? (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label className="block text-[var(--muted)] mb-1">Bot Token</label>
                <input
                  required
                  value={botToken}
                  onChange={(e) => setBotToken(e.target.value)}
                  className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
                />
              </div>
              <div>
                <label className="block text-[var(--muted)] mb-1">Chat ID</label>
                <input
                  required
                  value={chatId}
                  onChange={(e) => setChatId(e.target.value)}
                  className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
                />
              </div>
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className={chType === "webhook" ? "" : "sm:col-span-2"}>
                <label className="block text-[var(--muted)] mb-1">Webhook URL</label>
                <input
                  required
                  type="url"
                  value={webhookUrl}
                  onChange={(e) => setWebhookUrl(e.target.value)}
                  placeholder="https://..."
                  className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
                />
              </div>
              {chType === "webhook" && (
                <div>
                  <label className="block text-[var(--muted)] mb-1">X-VPSPulse-Secret (optional)</label>
                  <input
                    value={secret}
                    onChange={(e) => setSecret(e.target.value)}
                    className="w-full px-3 py-2 rounded bg-[var(--bg)] border border-[var(--border)] font-mono"
                  />
                </div>
              )}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setShowAdd(false)}
              className="px-3 py-1.5 rounded border border-[var(--border)] cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="submit"
              className="px-3.5 py-1.5 rounded bg-[var(--primary)] text-[var(--primary-fg)] font-medium cursor-pointer"
            >
              Save Channel
            </button>
          </div>
        </form>
      )}

      {/* Channel List */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {channels.map((ch) => (
          <div key={ch.id} className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4 space-y-3 text-xs">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="px-2 py-0.5 rounded font-mono uppercase bg-[var(--secondary)]">{ch.type}</span>
                <span className="font-semibold">{ch.name}</span>
              </div>
              <label className="inline-flex items-center gap-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={ch.enabled}
                  onChange={() => handleToggleChannel(ch)}
                  className="accent-[var(--primary)]"
                />
                <span>{ch.enabled ? "Enabled" : "Disabled"}</span>
              </label>
            </div>
            <div className="font-mono text-[var(--muted)] truncate">
              {ch.config.webhook_url || ch.config.url || (ch.config.chat_id ? `chat_id: ${ch.config.chat_id}` : "")}
            </div>
            <div className="flex items-center justify-between gap-2 pt-1 flex-wrap">
              <div className="flex items-center gap-1.5 flex-wrap">
                {(["offline", "service", "resource", "tls"] as const).map((k) => (
                  <button
                    key={k}
                    type="button"
                    onClick={() => handleToggleEvent(ch, k)}
                    className={`px-2 py-0.5 rounded border cursor-pointer ${
                      ch.events[k]
                        ? "border-[var(--primary)]/50 bg-[var(--bg)] text-[var(--fg)]"
                        : "border-[var(--border)] text-[var(--muted)] opacity-50"
                    }`}
                  >
                    {k}
                  </button>
                ))}
              </div>
              <div className="flex items-center gap-1.5">
                <button
                  onClick={() => handleTest(ch)}
                  className="inline-flex items-center gap-1 px-2.5 py-1 rounded bg-[var(--secondary)] border border-[var(--border)] cursor-pointer"
                >
                  <Send className="w-3 h-3" /> Test
                </button>
                <button
                  onClick={() => handleDelete(ch.id)}
                  className="p-1.5 rounded border border-[var(--border)] text-[var(--error)] cursor-pointer"
                  title="Delete channel"
                >
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Storage & Password */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
        <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4 space-y-2">
          <h3 className="font-semibold">SQLite Storage &amp; Retention</h3>
          <p className="text-[var(--muted)]">{info?.retention_policy || "Loading..."}</p>
          <p className="font-mono">
            Database size: {info ? formatBytes(info.db_size_bytes) : "—"} · Public URL:{" "}
            {info?.public_url || "auto"}
          </p>
        </div>

        <form
          onSubmit={handleChangePassword}
          className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-4 space-y-2.5"
        >
          <h3 className="font-semibold">Change Admin Password</h3>
          <div className="grid grid-cols-2 gap-2">
            <input
              required
              type="password"
              value={curPass}
              onChange={(e) => setCurPass(e.target.value)}
              placeholder="Current password"
              className="px-2.5 py-1.5 rounded bg-[var(--bg)] border border-[var(--border)]"
            />
            <input
              required
              type="password"
              minLength={6}
              value={newPass}
              onChange={(e) => setNewPass(e.target.value)}
              placeholder="New password (6+ chars)"
              className="px-2.5 py-1.5 rounded bg-[var(--bg)] border border-[var(--border)]"
            />
          </div>
          <button
            type="submit"
            className="px-3 py-1.5 rounded bg-[var(--secondary)] border border-[var(--border)] font-medium cursor-pointer"
          >
            Update Password
          </button>
        </form>
      </div>
    </div>
  );
}
