import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("VPSPulse UI error:", error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="min-h-screen flex items-center justify-center p-6 bg-[var(--bg)] text-[var(--fg)]">
          <div className="max-w-md w-full rounded-xl border border-[var(--border)] bg-[var(--card)] p-6 space-y-3 text-xs">
            <h1 className="text-sm font-semibold text-[var(--error)]">Something went wrong</h1>
            <p className="text-[var(--muted)] font-mono break-words">{this.state.error.message}</p>
            <button
              onClick={() => window.location.reload()}
              className="px-3 py-1.5 rounded bg-[var(--primary)] text-[var(--primary-fg)] font-medium cursor-pointer"
            >
              Reload Dashboard
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
