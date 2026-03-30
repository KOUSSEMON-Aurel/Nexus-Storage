import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./index.css";

// ─── Error Boundary ──────────────────────────────────────────────────────────
class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { hasError: boolean; error: Error | null }
> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error("[Nexus] Uncaught React error:", error, info);
  }

  render() {
    if (this.state.hasError) {
      return (
        <div style={{
          display: "flex", flexDirection: "column", alignItems: "center",
          justifyContent: "center", height: "100vh", background: "#0f172a",
          color: "#f8fafc", fontFamily: "system-ui, sans-serif", gap: "16px",
          padding: "32px", textAlign: "center",
        }}>
          <div style={{ fontSize: "48px" }}>⚡</div>
          <h1 style={{ fontSize: "24px", margin: 0 }}>Nexus — Erreur de rendu</h1>
          <p style={{ color: "#94a3b8", maxWidth: "480px", margin: 0 }}>
            {this.state.error?.message || "Erreur inattendue."}
          </p>
          <button
            onClick={() => this.setState({ hasError: false, error: null })}
            style={{
              marginTop: "8px", padding: "10px 24px", borderRadius: "8px",
              background: "#6366f1", color: "#fff", border: "none",
              cursor: "pointer", fontSize: "14px", fontWeight: 600,
            }}
          >
            🔄 Réessayer
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);
