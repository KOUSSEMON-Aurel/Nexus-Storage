import { useState, useEffect, useRef } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { Search, Plus, HardDrive, Shield, Clock, Star, Trash2,
  Grid3X3, List, FileText, FileImage, Archive, Lock,
  X, MoreVertical, Moon, Sun, CloudLightning, ChevronRight,
  Upload, Minus, Square, RefreshCw, Check, Settings
} from "lucide-react";
import { open as openDialog } from "@tauri-apps/plugin-dialog";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { useNavigate } from "react-router-dom";
import PasswordModal from "./components/PasswordModal";

// ─── Types ────────────────────────────────────────────────────────────────────

type ViewMode = "grid" | "list";
type Section = "my-drive" | "recent" | "starred" | "security" | "trash";

interface NFile {
  id: string;
  name: string;
  size: string;
  type: "archive" | "image" | "doc" | "key" | "video";
  modified: string;
  shardId: string;
  starred: boolean;
  encrypted: boolean;
  owner: string;
  deleted: boolean;
  rawDate: number;
  sha256: string;
  parentId?: number;
  videoID: string;
}

// ─── Constants ────────────────────────────────────────────────────────────────

const API_BASE = "http://127.0.0.1:8081/api";

// Persistent session state to avoid re-showing splash screen when returning from Settings
let hasInitializedSession = false;

interface BackendFile {
  ID: number;
  Path: string;
  VideoID: string;
  Size: number;
  Hash: string;
  Key: string;
  LastUpdate: string;
  Starred: boolean;
  DeletedAt: string | null;
  ParentID: number | null;
  SHA256: string;
}

interface BackendTask {
  id: string;
  type: number;
  filePath: string;
  status: string;
  progress: number;
  createdAt: string;
}

interface Stats {
  file_count: number;
  total_size: number;
  starred_count?: number;
  trash_count?: number;
  active_tasks?: number;
}

interface AuthStatus {
  authenticated: boolean;
  user: string;
}

function mapBackendToFile(bf: BackendFile): NFile {
  const name = bf.Path.split(/[/\\]/).pop() || bf.Path;
  const ext = name.split('.').pop()?.toLowerCase();
  
  let type: NFile["type"] = "archive";
  if (["png", "jpg", "jpeg", "gif", "webp", "svg"].includes(ext || "")) type = "image";
  else if (["pdf", "txt", "doc", "docx", "odt", "md"].includes(ext || "")) type = "doc";
  else if (["enc", "key"].includes(ext || "")) type = "key";
  else if (["mp4", "mkv", "mov", "avi"].includes(ext || "")) type = "video";

  return {
    id: String(bf.ID),
    name: name,
    size: formatSize(bf.Size ?? 0),
    type: type,
    modified: bf.LastUpdate ? new Date(bf.LastUpdate).toLocaleDateString() : "-",
    shardId: (bf.VideoID || "").substring(0, 8),
    videoID: bf.VideoID || "",
    starred: bf.Starred ?? false,
    encrypted: true,
    owner: "me",
    deleted: !!bf.DeletedAt,
    rawDate: bf.LastUpdate ? new Date(bf.LastUpdate).getTime() : 0,
    sha256: bf.SHA256 ?? "",
    parentId: bf.ParentID ?? undefined,
  };
}

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
}

// Each file type gets a distinct icon, background color, and icon color
const TYPE_CONFIG = {
  archive: { icon: Archive,     bg: "#FEF3C7", color: "#D97706" },
  image:   { icon: FileImage,   bg: "#FCE7F3", color: "#DB2777" },
  doc:     { icon: FileText,    bg: "#DBEAFE", color: "#2563EB" },
  key:     { icon: Lock,        bg: "#D1FAE5", color: "#059669" },
  video:   { icon: FileText,    bg: "#EDE9FE", color: "#7C3AED" },
};

// ─── App ─────────────────────────────────────────────────────────────────────

export default function Dashboard() {
  const [section, setSection] = useState<Section>("my-drive");
  const [viewMode, setViewMode] = useState<ViewMode>("grid");
  const [search, setSearch] = useState("");
  const [dark, setDark] = useState(false);
  const [selected, setSelected] = useState<NFile | null>(null);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [downloadPasswordOpen, setDownloadPasswordOpen] = useState(false);
  const [pendingDownloadFile] = useState<NFile | null>(null);
  const [downloadPassword, setDownloadPassword] = useState("");
  const [toast, setToast] = useState<{ msg: string; type: "success" | "error" | "info" } | null>(null);
  
  const [dbFiles, setDbFiles] = useState<NFile[]>([]);
  const [tasks, setTasks] = useState<Record<string, BackendTask>>({});
  const [stats, setStats] = useState<Stats>({ file_count: 0, total_size: 0 });
  const navigate = useNavigate();
  const [auth, setAuth] = useState<AuthStatus>({ authenticated: false, user: "" });
  const [quota, setQuota] = useState({ used: 0, limit: 10000, source: "local" });
  const [accountOpen, setAccountOpen] = useState(false);
  const [isMounted, setIsMounted] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [isAppReady, setIsAppReady] = useState(hasInitializedSession);
  const [isInitialLoading, setIsInitialLoading] = useState(!hasInitializedSession);
  const [passwordModalOpen, setPasswordModalOpen] = useState(false);
  const [pendingPasswordOperation, setPendingPasswordOperation] = useState<{
    title: string;
    description: string;
    callback: (password: string) => Promise<void>;
  } | null>(null);

  const handleLogout = async () => {
    try {
      await fetch(`${API_BASE}/auth/logout`, { method: "POST" });
      setAuth({ authenticated: false, user: "" });
      setAccountOpen(false);
      showToast("🚪 Logged out successfully", "info");
    } catch (e) {
      console.error("Logout failed", e);
    }
  };

  const handleMountDisk = async () => {
    try {
      await fetch("http://127.0.0.1:8081/api/mount");
      showToast("📡 Virtual Disk Mount requested...", "info");
      setTimeout(() => setIsMounted(true), 2000);
    } catch (e) {
      console.error("Mount failed", e);
    }
  };

  const handleUnmountDisk = async () => {
    try {
      await fetch("http://127.0.0.1:8081/api/unmount");
      showToast("🔌 Virtual Disk Unmounted", "info");
      setIsMounted(false);
    } catch (e) {
      console.error("Unmount failed", e);
    }
  };

  const handleSyncManifest = async () => {
    if (syncing) return;
    setSyncing(true);
    try {
      showToast("🔄 Syncing with YouTube Cloud...", "info");
      const res = await fetch(`${API_BASE}/cloud/sync`, { method: "POST" });
      if (res.ok) {
        showToast("✅ Cloud Sync Completed", "success");
        // Force immediate refresh
        const filesRes = await fetch(section === "trash" ? `${API_BASE}/trash` : `${API_BASE}/files`);
        if (filesRes.ok) {
          const data: BackendFile[] = await filesRes.json();
          setDbFiles(data.map(mapBackendToFile));
        }
      } else {
        const errText = await res.text();
        showToast(`❌ Sync Failed: ${errText}`, "error");
      }
    } catch (e) {
      console.error("Sync failed", e);
      showToast("❌ Network Error during Sync", "error");
    } finally {
      setSyncing(false);
    }
  };

  const handleCalibrateQuota = async () => {
    try {
      showToast("🔄 Refreshing metrics...", "info");
      // Force live quota refresh
      const fetchQuota = async () => {
        const res = await fetch(`${API_BASE}/quota?force=true`);
        if (res.ok) setQuota(await res.json());
      };
      await fetchQuota();
      showToast("✅ Metrics synchronized", "success");
    } catch (e) {
      console.error("Refresh failed", e);
    }
  };

  const showToast = (msg: string, type: "success" | "error" | "info" = "success") => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 3000);
  };

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
  }, [dark]);

  // ─── INITIALIZATION & GATING ──────────────────────────────────────────────
  useEffect(() => {
    let unmount = false;
    const startTime = Date.now();

    const initialize = async () => {
      // 1. Core Readiness (API + Auth)
      try {
        const fetchOpts: RequestInit = { cache: "no-store", headers: { "Pragma": "no-cache", "Cache-Control": "no-cache" } };
        
        // Wait for basic stats (API ready)
        const statsRes = await fetch(`${API_BASE}/stats`, fetchOpts);
        if (!statsRes.ok) throw new Error("Backend not available");
        
        // CRITICAL: Fetch Auth status BEFORE lifting splash
        const authRes = await fetch(`${API_BASE}/auth/status?_t=${Date.now()}`, fetchOpts);
        if (authRes.ok) {
           const authData = await authRes.json();
           if (!unmount) setAuth(authData);
        }

        // Fetch first batch of files for skeletons
        const filesRes = await fetch(section === "trash" ? `${API_BASE}/trash` : `${API_BASE}/files`, fetchOpts);
        if (filesRes.ok && !unmount) {
           const data: BackendFile[] = await filesRes.json();
           setDbFiles(data.map(mapBackendToFile));
        }

        // 2. Handle Splash Logic
        if (hasInitializedSession) {
          if (!unmount) {
            setIsAppReady(true);
            setIsInitialLoading(false);
          }
          return;
        }

        // Minimum delay (2s) to show the premium splash screen on first load
        const elapsed = Date.now() - startTime;
        const wait = Math.max(0, 2000 - elapsed);
        
        setTimeout(() => {
          if (!unmount) {
            setIsAppReady(true);
            hasInitializedSession = true; // Mark as initialized for the rest of the session
            // Show skeletons for another 800ms while UI transitions
            setTimeout(() => { if (!unmount) setIsInitialLoading(false); }, 800);
          }
        }, wait);

      } catch (err) {
        if (!unmount) {
           console.log("Waiting for backend...", err);
           // Retry after 1s
           setTimeout(initialize, 1000);
        }
      }
    };

    initialize();
    return () => { unmount = true; };
  }, [section]);

  // ─── BACKGROUND POLLING ───────────────────────────────────────────────────
  useEffect(() => {
    let tick = 0;
    const poll = async () => {
      if (!isAppReady) return; // Don't poll until initialized

      try {
        const fetchFilesUrl = section === "trash" ? `${API_BASE}/trash` : `${API_BASE}/files`;
        const fetchOpts: RequestInit = { cache: "no-store", headers: { "Pragma": "no-cache", "Cache-Control": "no-cache" } };
        const [tasksRes, statsRes, quotaRes, mountRes] = await Promise.all([
          fetch(`${API_BASE}/tasks`, fetchOpts),
          fetch(`${API_BASE}/stats`, fetchOpts),
          fetch(`${API_BASE}/quota`, fetchOpts),
          fetch(`${API_BASE}/mount/status`, fetchOpts)
        ]);
        
        if (tasksRes.ok) setTasks(await tasksRes.json());
        if (statsRes.ok) setStats(await statsRes.json());
        if (quotaRes.ok) setQuota(await quotaRes.json());
        if (mountRes.ok) setIsMounted((await mountRes.json()).mounted);

        // Files polling
        if (tick % 2 === 0) { // Every 4s
          const filesRes = await fetch(fetchFilesUrl, fetchOpts);
          if (filesRes.ok) {
            const data: BackendFile[] = await filesRes.json();
            setDbFiles(data.map(mapBackendToFile));
          }
        }

        // Auth polling (aggressively if not authenticated, else every 60s)
        if (!auth.authenticated || tick % 30 === 0) {
          const authRes = await fetch(`${API_BASE}/auth/status?_t=${Date.now()}`, fetchOpts);
          if (authRes.ok) {
            const authData = await authRes.json();
            if (authData.authenticated !== auth.authenticated || authData.user !== auth.user) {
              setAuth(authData);
            }
          }
        }
        tick++;
      } catch (err) {
        console.error("Polling Error:", err);
      }
    };

    poll();
    const interval = setInterval(poll, 2000);
    return () => clearInterval(interval);
  }, [section, auth.authenticated, auth.user, isAppReady]);

  // Quota Alert logic
  useEffect(() => {
    if (quota.limit > 0 && quota.used / quota.limit > 0.9) {
      showToast(`⚠️ Quota Critical: ${Math.round((quota.used / quota.limit) * 100)}% used`, "error");
    }
  }, [quota.used, quota.limit]);

  // Handle Dynamic Search (V2 FTS5)
  useEffect(() => {
    if (!search) return;
    const delay = setTimeout(async () => {
      try {
        const res = await fetch(`${API_BASE}/search?q=${encodeURIComponent(search)}`, { cache: "no-store" });
        if (res.ok) {
          const data: BackendFile[] = await res.json();
          setDbFiles(data.map(mapBackendToFile));
        }
      } catch (e) {
        console.error("Search failed", e);
      }
    }, 300);
    return () => clearTimeout(delay);
  }, [search]);

  const handleUploadClick = async (path: string, mode: string, password?: string, isFolder?: boolean) => {
    try {
      if (path) {
        // Security check: ensure encryption secret exists
        const hasMasterPassword = localStorage.getItem('nexus_master_password') !== null;
        if (!password && !hasMasterPassword) {
          showToast("❌ No encryption password provided. Please enter one or set a master password in Settings.", "error");
          return;
        }

        setUploadOpen(false);
        showToast(isFolder ? "Archiving & starting upload..." : "Starting upload...", "info");
        
        // Use provided password or fallback to master password
        const finalPassword = password || localStorage.getItem('nexus_master_password') || "";
        
        const res = await fetch(`${API_BASE}/upload`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ 
            path, 
            mode, 
            password: finalPassword
          })
        });
        if (res.ok) {
          showToast("Upload task added to queue.", "success");
        } else {
          const errorText = await res.text();
          if (errorText.includes("no encryption secret available")) {
            showToast("❌ Encryption setup required. Go to Settings > Security & Password to set a master password.", "error");
          } else {
            showToast(`Error: ${errorText}`, "error");
          }
        }
      }
    } catch (err: any) {
      console.error("Upload error:", err);
      showToast(`Upload error: ${err.message || err}`, "error");
    }
  };

  const handleAction = async (action: "download" | "delete" | "star" | "restore" | "permanent" | "evict", file: NFile) => {
    try {
      if (action === "download") {
        showToast("Starting download...", "success");
        const res = await fetch(`${API_BASE}/download`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ video_id: file.videoID, path: file.name })
        });
        if (res.ok) showToast("Download started", "success");
        else showToast("Download failed", "error");
      }
      else if (action === "delete") {
        if (!confirm(`Move ${file.name} to trash?`)) return;
        const res = await fetch(`${API_BASE}/files/${file.id}`, { method: "DELETE" });
        if (res.ok) { showToast("Moved to trash"); setDbFiles(dbFiles.map(f => f.id === file.id ? { ...f, deleted: true } : f)); setSelected(null); }
      }
      else if (action === "permanent") {
        if (!confirm(`Permanently delete ${file.name}? This cannot be undone.`)) return;
        const res = await fetch(`${API_BASE}/files/${file.id}/permanent`, { method: "DELETE" });
        if (res.ok) { showToast("Permanently deleted"); setDbFiles(dbFiles.filter(f => f.id !== file.id)); setSelected(null); }
      }
      else if (action === "evict") {
        const res = await fetch(`${API_BASE}/files/${file.id}/evict`, { method: "POST" });
        if (res.ok) { showToast("Local cache freed"); }
      }
      else if (action === "restore") {
        const res = await fetch(`${API_BASE}/files/${file.id}/restore`, { method: "POST" });
        if (res.ok) { showToast("File restored"); setDbFiles(dbFiles.map(f => f.id === file.id ? { ...f, deleted: false } : f)); }
      }
      else if (action === "star") {
        const newStarred = !file.starred;
        const res = await fetch(`${API_BASE}/files/${file.id}/star`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ starred: newStarred })
        });
        if (res.ok) {
          showToast(newStarred ? "Starred" : "Unstarred");
          const updatedFiles = dbFiles.map(f => f.id === file.id ? { ...f, starred: newStarred } : f);
          setDbFiles(updatedFiles);
          if (selected?.id === file.id) {
            setSelected({ ...file, starred: newStarred });
          }
        }
      }
    } catch (err: any) {
      showToast(`Action failed: ${err.message}`, "error");
    }
  };

  let files = dbFiles.filter(f => {
    const q = search.toLowerCase();
    const matchSearch = f.name.toLowerCase().includes(q);
    if (!matchSearch) return false;

    if (section === "trash") return f.deleted;
    if (f.deleted) return false;
    
    if (section === "starred") return f.starred;
    return true;
  });

  if (section === "recent") {
    files = [...files].sort((a, b) => b.rawDate - a.rawDate).slice(0, 20);
  }

  // Colors derived from dark/light mode
  const c = dark ? DARK : LIGHT;

  const SECTION_LABELS: Record<Section, string> = {
    "my-drive": "My Drive",
    recent:     "Recent",
    starred:    "Starred",
    security:   "Security",
    trash:      "Trash",
  };

  return (
    <>
      <SplashScreen b={isAppReady} loading={isInitialLoading} c={c} />
      <div 
        data-tauri-drag-region
        style={{ background: c.bgApp, color: c.textPrimary, fontFamily: "'Inter', system-ui, sans-serif", height: "100vh", display: "flex", flexDirection: "column", overflow: "hidden" }}>
      
      {/* ━━━━ HEADER ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <header
        data-tauri-drag-region
        style={{
          height: 64,
          display: "flex",
          alignItems: "center",
          padding: "0 24px",
          gap: 16,
          background: c.bgApp,
          flexShrink: 0,
          cursor: "default",
          zIndex: 50,
        }}
      >
        {/* Logo - 256px to match sidebar. Settings gear placed right of logo for quick access */}
        <div style={{ width: 256, display: "flex", alignItems: "center", gap: 10, flexShrink: 0, userSelect: "none" }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flex: 1 }}>
            <div style={{ width: 40, height: 40, borderRadius: 12, background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", pointerEvents: 'none' }}>
              <CloudLightning size={22} color="white" />
            </div>
            <span style={{ fontSize: 22, fontWeight: 400, color: c.textPrimary, letterSpacing: -0.3, pointerEvents: 'none' }}>Nexus</span>
          </div>
          <button
            onClick={() => navigate('/settings')}
            title="Settings"
            style={{
              width: 40,
              height: 40,
              borderRadius: 10,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              background: 'transparent',
              border: 'none',
              cursor: 'pointer',
              color: c.textPrimary
            }}
          >
            <Settings size={18} />
          </button>
        </div>

        {/* Search - FTS5 Optimized */}
        <div style={{ flex: 1, display: "flex", alignItems: "center", gap: 12, background: c.bgSearch, borderRadius: 24, padding: "0 20px", height: 46 }}>
          <Search size={20} color={c.textSecondary} style={{ flexShrink: 0, pointerEvents: "none" }} />
          <input
            type="text"
            placeholder="Search across thousands of shards (V2 Instant)..."
            value={search}
            onChange={e => setSearch(e.target.value)}
            style={{
              flex: 1,
              background: "transparent",
              border: "none",
              outline: "none",
              fontSize: 16,
              color: c.textPrimary,
              lineHeight: "1.5",
              pointerEvents: "auto",
            }}
          />
        </div>

        {/* Actions on the right */}
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexShrink: 0 }}>
          <IconBtn onClick={() => setDark(d => !d)} title="Toggle theme" dark={dark}>
            {dark ? <Sun size={20} /> : <Moon size={20} />}
          </IconBtn>
          {/* Avatar */}
          <div 
            onClick={() => setAccountOpen(true)}
            style={{ width: 36, height: 36, borderRadius: "50%", background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", color: "white", fontSize: 14, fontWeight: 600, marginLeft: 8, cursor: "pointer", pointerEvents: "auto" }}>
            {auth.authenticated ? auth.user.charAt(0).toUpperCase() : "!"}
          </div>
          <div style={{ pointerEvents: "auto", display: "flex", alignItems: "center", gap: 4 }}>
            <IconBtn onClick={() => getCurrentWindow().minimize()} title="Minimize" dark={dark}>
              <Minus size={20} />
            </IconBtn>
            <IconBtn onClick={async () => await getCurrentWindow().toggleMaximize()} title="Maximize" dark={dark}>
              <Square size={16} />
            </IconBtn>
            <IconBtn onClick={() => getCurrentWindow().close()} title="Close" dark={dark}>
              <X size={20} />
            </IconBtn>
          </div>
        </div>
      </header>

      {/* ━━━━ BODY ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      {!auth.authenticated ? (
        <SignInView c={c} />
      ) : (
        <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
        
        {/* ━━━━ SIDEBAR ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
        <aside 
          data-tauri-drag-region
          style={{ 
          width: 256, flexShrink: 0, 
          background: c.bgApp,
          display: "flex", flexDirection: "column", paddingTop: 16, paddingBottom: 16, overflow: "hidden",
        }}>
          
          {/* New Button */}
          <div style={{ padding: "0 16px 16px" }}>
            <button
              onClick={() => setUploadOpen(true)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 12,
                padding: "14px 20px",
                borderRadius: 16,
                background: c.bgSurface,
                border: "none",
                boxShadow: c.btnShadow,
                cursor: "pointer",
                fontSize: 14,
                fontWeight: 500,
                color: c.textPrimary,
                width: "100%",
                transition: "box-shadow 0.15s",
              }}
            >
              <Plus size={20} color="#1A73E8" />
              New
            </button>
          </div>

          {/* Nav */}
          <nav style={{ flex: 1, padding: "0 8px", display: "flex", flexDirection: "column", gap: 2 }}>
            {[
              { id: "my-drive" as Section, icon: HardDrive, label: "My Drive" },
              { id: "recent"   as Section, icon: Clock,     label: "Recent" },
              { id: "starred"  as Section, icon: Star,      label: "Starred" },
            ].map(item => (
              <NavItem key={item.id} item={item} active={section === item.id} onClick={() => { setSection(item.id); setSelected(null); }} c={c} />
            ))}

            <div style={{ height: 1, background: c.border, margin: "8px 8px" }} />

            {[
              { id: "security" as Section, icon: Shield, label: "Security Hub" },
              { id: "trash"    as Section, icon: Trash2, label: "Trash" },
            ].map(item => (
              <NavItem key={item.id} item={item} active={section === item.id} onClick={() => { setSection(item.id); setSelected(null); }} c={c} />
            ))}

            <div style={{ height: 1, background: c.border, margin: "8px 8px" }} />

            <div
              onClick={async () => {
                try {
                  await fetch(`${API_BASE}/studio`);
                } catch (e) {
                  console.error("Studio link failed", e);
                }
              }}
              style={{
                display: "flex", alignItems: "center", gap: 14,
                padding: "9px 16px", borderRadius: 24,
                color: c.textPrimary,
                fontSize: 14, cursor: "pointer", transition: "background 0.15s",
                userSelect: "none",
              }}
              onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = c.bgHover}
              onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = "transparent"}
            >
              <CloudLightning size={20} color="#FF0000" />
              YouTube Studio
            </div>

            <div 
              onClick={handleSyncManifest}
              style={{
                display: "flex", alignItems: "center", gap: 14,
                padding: "9px 16px", borderRadius: 24,
                color: syncing ? c.textSecondary : c.textPrimary,
                fontSize: 14, cursor: syncing ? "not-allowed" : "pointer", transition: "background 0.15s",
                userSelect: "none",
                opacity: syncing ? 0.6 : 1,
              }}
              onMouseEnter={e => { if (!syncing) (e.currentTarget as HTMLDivElement).style.background = c.bgHover; }}
              onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = "transparent"; }}
            >
              <RefreshCw size={20} color={c.textSecondary} style={{ animation: syncing ? "spin 2s linear infinite" : "none" }} />
              {syncing ? "Syncing..." : "Sync with YouTube"}
            </div>
          </nav>

          {/* Virtual Disk Mount */}
          <div style={{ padding: "0 16px", marginBottom: 24 }}>
            <div style={{ padding: 16, background: c.bgSurface, border: `1px solid ${c.border}`, borderRadius: 16 }}>
              <div style={{ fontSize: 13, color: c.textSecondary, marginBottom: 8 }}>VIRTUAL DISK</div>
              {isMounted ? (
                <button 
                  onClick={handleUnmountDisk}
                  style={{ width: "100%", padding: "10px", borderRadius: 10, background: "#ef444420", border: `1px solid #ef444450`, color: "#ef4444", cursor: "pointer", fontSize: 13, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 8 }}
                >
                  <span>🔌</span> Quitter FUSE
                </button>
              ) : (
                <button 
                  onClick={handleMountDisk}
                  style={{ width: "100%", padding: "10px", borderRadius: 10, background: c.bgApp, border: `1px solid ${c.border}`, color: c.textPrimary, cursor: "pointer", fontSize: 13, fontWeight: 500, display: "flex", alignItems: "center", justifyContent: "center", gap: 8 }}
                >
                  <span>📡</span> Connect
                </button>
              )}
            </div>
          </div>

          {/* Storage & Quota */}
          <div style={{ padding: "12px 24px", borderTop: `1px solid ${c.border}` }}>
            {/* Real Storage Stats */}
            <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 8 }}>
              <HardDrive size={18} color={c.textSecondary} />
              <span style={{ fontSize: 13, color: c.textSecondary, fontWeight: 500 }}>{formatSize(stats.total_size)} Secured</span>
            </div>
            
            {/* Quota Progress */}
            <div style={{ display: "flex", alignItems: "center", justifyItems: "space-between", gap: 10, marginBottom: 8, marginTop: 12 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                <CloudLightning size={18} color="#1A73E8" />
                <span style={{ fontSize: 13, color: c.textPrimary, fontWeight: 600 }}>YouTube Quota</span>
                {quota.source === "monitoring" && (
                  <span style={{ fontSize: 9, background: "#10b98120", color: "#10b981", padding: "1px 6px", borderRadius: 10, fontWeight: 700, marginLeft: 4 }}>LIVE</span>
                )}
              </div>
              <div style={{ display: "flex", alignItems: "center", gap: 6, marginLeft: "auto" }}>
                <span style={{ fontSize: 11, color: c.textSecondary }}>{quota.used}/{Math.round(quota.limit/1000)}k</span>
                <button 
                  onClick={handleCalibrateQuota}
                  style={{ border: "none", background: "transparent", cursor: "pointer", color: c.textSecondary, padding: 0 }}
                  title="Calibrate Usage"
                >
                  <RefreshCw size={10} />
                </button>
              </div>
            </div>
            <div style={{ height: 6, background: c.border, borderRadius: 4, overflow: "hidden", marginBottom: 6 }}>
              <div style={{ width: `${Math.min(100, (quota.used / quota.limit) * 100)}%`, height: "100%", background: (quota.used / quota.limit > 0.9) ? "#EA4335" : "#1A73E8" }} />
            </div>
            <p style={{ fontSize: 10, color: c.textSecondary, lineHeight: 1.4 }}>Daily limit resets at midnight PT.</p>
          </div>

        </aside>

        {/* ━━━━ MAIN CONTENT ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
        <main
          style={{
            flex: 1,
            display: "flex",
            flexDirection: "column",
            background: c.bgSurface,
            margin: "8px 16px 8px 0",
            borderRadius: 16,
            border: `1px solid ${c.border}`,
            overflow: "hidden",
            position: "relative"
          }}
        >
          <TaskOverlay tasks={tasks} c={c} />
          {/* Toolbar */}
          <div style={{
            height: 56,
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "0 20px",
            borderBottom: `1px solid ${c.border}`,
            flexShrink: 0,
          }}>
            {/* Breadcrumb */}
            <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <span style={{ fontSize: 18, fontWeight: 500, color: c.textPrimary }}>
                {SECTION_LABELS[section]}
              </span>
              <ChevronRight size={18} color={c.textSecondary} />
            </div>

            {/* View toggles */}
            <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <ViewToggleBtn active={viewMode === "list"} onClick={() => setViewMode("list")} title="List view" c={c}>
                <List size={20} />
              </ViewToggleBtn>
              <ViewToggleBtn active={viewMode === "grid"} onClick={() => setViewMode("grid")} title="Grid view" c={c}>
                <Grid3X3 size={20} />
              </ViewToggleBtn>
            </div>
          </div>

          {/* File area */}
          <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
            {/* File content */}
            <div style={{ flex: 1, overflowY: "auto", padding: 20 }}>
              <AnimatePresence mode="wait">
                <motion.div key={section} initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.12 }}>
                  {section === "security" ? (
                    <SecuritySection c={c} />
                  ) : files.length === 0 ? (
                    <EmptyState 
                      icon={section === "trash" ? <Trash2 size={64} color={c.textSecondary} /> : <Search size={64} />} 
                      title={section === "trash" ? "Trash is empty" : "No files match your search"} 
                      sub={section === "trash" ? "Items deleted from Nexus are stored here temporarily." : "Try a different search term."} 
                      c={c} 
                    />
                  ) : (
                    <>
                      <p style={{ fontSize: 14, fontWeight: 500, color: c.textSecondary, marginBottom: 16 }}>
                        {section === "starred" ? "Starred" : "Suggested"}
                      </p>
                      {isInitialLoading ? (
                        <>
                          <div style={{ display: "flex", alignItems: "center", gap: 4, marginBottom: 16 }}>
                            <Skeleton width={120} height={18} />
                          </div>
                          <div style={{ display: viewMode === "grid" ? "grid" : "block", gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))", gap: 20 }}>
                            {[1,2,3,4,5,6].map(i => <FileSkeleton key={i} viewMode={viewMode} c={c} />)}
                          </div>
                        </>
                      ) : viewMode === "grid" ? (
                        <FileGrid files={files} onSelect={setSelected} selected={selected!} c={c} dark={dark} />
                      ) : (
                        <FileList files={files} onSelect={setSelected} selected={selected!} c={c} dark={dark} />
                      )}
                    </>
                  )}
                </motion.div>
              </AnimatePresence>
            </div>

            {/* Detail panel */}
            <AnimatePresence>
              {selected && (
                <motion.div
                  initial={{ width: 0, opacity: 0 }}
                  animate={{ width: 280, opacity: 1 }}
                  exit={{ width: 0, opacity: 0 }}
                  transition={{ duration: 0.2, ease: "easeInOut" }}
                  style={{ borderLeft: `1px solid ${c.border}`, overflow: "hidden", flexShrink: 0 }}
                >
                  <DetailPanel file={selected} onClose={() => setSelected(null)} onAction={handleAction} c={c} section={section} />
                </motion.div>
              )}
            </AnimatePresence>
          </div>
        </main>
      </div>
    )}

      {/* ━━━━ UPLOAD MODAL ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <AnimatePresence>
        {uploadOpen && (
          <>
            <motion.div
              initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              onClick={() => setUploadOpen(false)}
              style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.5)", zIndex: 100 }}
            />
            <motion.div
              initial={{ opacity: 0, scale: 0.96, x: "-50%", y: "-40%" }}
              animate={{ opacity: 1, scale: 1, x: "-50%", y: "-50%" }}
              exit={{ opacity: 0, scale: 0.96, x: "-50%", y: "-40%" }}
              transition={{ duration: 0.18 }}
              style={{
                position: "fixed", top: "50%", left: "50%",
                width: 460, zIndex: 101,
                background: c.bgSurface,
                border: `1px solid ${c.border}`,
                borderRadius: 24,
                overflow: "hidden",
                boxShadow: "0 24px 60px rgba(0,0,0,0.2)",
              }}
            >
              <UploadModal onClose={() => setUploadOpen(false)} onUpload={handleUploadClick} c={c} />
            </motion.div>
          </>
        )}
      </AnimatePresence>

      {/* ━━━━ DOWNLOAD PASSWORD MODAL ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <AnimatePresence>
        {downloadPasswordOpen && (
          <>
            <motion.div
              initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              onClick={() => setDownloadPasswordOpen(false)}
              style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.5)", zIndex: 150 }}
            />
            <motion.div
              initial={{ opacity: 0, scale: 0.96, x: "-50%", y: "-40%" }}
              animate={{ opacity: 1, scale: 1, x: "-50%", y: "-50%" }}
              exit={{ opacity: 0, scale: 0.96, x: "-50%", y: "-40%" }}
              style={{
                position: "fixed", top: "50%", left: "50%",
                width: 400, zIndex: 151,
                background: c.bgSurface,
                border: `1px solid ${c.border}`,
                borderRadius: 24,
                overflow: "hidden",
                boxShadow: "0 24px 60px rgba(0,0,0,0.3)",
                padding: 32,
              }}
            >
               <h3 style={{ fontSize: 18, fontWeight: 600, color: c.textPrimary, marginBottom: 8 }}>Authentication Required</h3>
               <p style={{ fontSize: 14, color: c.textSecondary, marginBottom: 24 }}>
                 Enter the decryption password for <b>{pendingDownloadFile?.name}</b>.
               </p>
               <div style={{ position: "relative", marginBottom: 24 }}>
                 <Lock size={18} color={c.textSecondary} style={{ position: "absolute", left: 12, top: "50%", transform: "translateY(-50%)" }} />
                 <input 
                   type="password" 
                   autoFocus
                   placeholder="Decryption password..."
                   value={downloadPassword}
                   onChange={(e) => setDownloadPassword(e.target.value)}
                   onKeyDown={(e) => e.key === "Enter" && handleAction("download", pendingDownloadFile!)}
                   style={{ width: "100%", padding: "14px 14px 14px 44px", borderRadius: 12, background: c.bgApp, border: `1px solid ${c.border}`, color: c.textPrimary, fontSize: 14 }}
                 />
               </div>
               <div style={{ display: "flex", gap: 12 }}>
                 <button 
                   onClick={() => setDownloadPasswordOpen(false)}
                   style={{ flex: 1, padding: "12px", borderRadius: 12, background: "transparent", border: `1px solid ${c.border}`, color: c.textSecondary, cursor: "pointer", fontWeight: 500 }}
                 >
                   Cancel
                 </button>
                 <button 
                   onClick={() => handleAction("download", pendingDownloadFile!)}
                   style={{ flex: 1, padding: "12px", borderRadius: 12, background: "#1A73E8", color: "white", border: "none", cursor: "pointer", fontWeight: 600 }}
                 >
                   Start Recovery
                 </button>
               </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>

      {/* ━━━━ ACCOUNT MODAL ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <AnimatePresence>
        {accountOpen && (
          <>
            <motion.div
              initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              onClick={() => setAccountOpen(false)}
              style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.5)", zIndex: 200 }}
            />
            <motion.div
              initial={{ opacity: 0, scale: 0.96, x: "-50%", y: "-40%" }}
              animate={{ opacity: 1, scale: 1, x: "-50%", y: "-50%" }}
              exit={{ opacity: 0, scale: 0.96, x: "-50%", y: "-40%" }}
              style={{
                position: "fixed", top: "50%", left: "50%",
                width: 380, zIndex: 201,
                background: c.bgSurface,
                border: `1px solid ${c.border}`,
                borderRadius: 24,
                overflow: "hidden",
                boxShadow: "0 24px 60px rgba(0,0,0,0.3)",
                padding: 32,
                display: "flex", flexDirection: "column", alignItems: "center"
              }}
            >
              <div style={{ width: 80, height: 80, borderRadius: "50%", background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", color: "white", fontSize: 32, fontWeight: 600, marginBottom: 16 }}>
                {auth.authenticated ? auth.user.charAt(0).toUpperCase() : "!"}
              </div>
              <h2 style={{ fontSize: 20, fontWeight: 600, color: c.textPrimary, marginBottom: 4 }}>
                {auth.authenticated ? (auth.user || "Aurel") : "Guest User"}
              </h2>
              <p style={{ fontSize: 14, color: c.textSecondary, marginBottom: 24 }}>
                {auth.authenticated ? auth.user : "No YouTube account connected"}
              </p>
              
              <div style={{ width: "100%", height: 1, background: c.border, marginBottom: 24 }} />
              
              <div style={{ width: "100%", display: "flex", flexDirection: "column", gap: 12 }}>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ fontSize: 13, color: c.textSecondary }}>System Access</span>
                  <span style={{ fontSize: 13, color: "#34A853", fontWeight: 600 }}>Active</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ fontSize: 13, color: c.textSecondary }}>Encryption</span>
                  <span style={{ fontSize: 13, color: c.textPrimary }}>XChaCha20</span>
                </div>
                {auth.authenticated && (
                  <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 8 }}>
                    <button 
                      onClick={() => {
                        navigate('/settings');
                        setAccountOpen(false);
                      }}
                      style={{ padding: "8px", borderRadius: 8, background: "#667eea20", border: `1px solid #667eea40`, color: "#667eea", cursor: "pointer", fontSize: 12, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 8 }}
                    >
                      <Settings size={16} /> Settings
                    </button>
                    <button 
                      onClick={handleSyncManifest}
                      style={{ padding: "8px", borderRadius: 8, background: "#1A73E820", border: `1px solid #1A73E840`, color: "#1A73E8", cursor: "pointer", fontSize: 12, fontWeight: 600 }}
                    >
                      Sync Manifest Now
                    </button>
                    <button 
                      onClick={async () => {
                        setAccountOpen(false);
                        await fetch(`${API_BASE}/studio`);
                      }}
                      style={{ padding: "8px", borderRadius: 8, background: "#FF000015", border: `1px solid #FF000030`, color: "#CC0000", cursor: "pointer", fontSize: 12, fontWeight: 600, display: "flex", alignItems: "center", justifyContent: "center", gap: 8 }}
                    >
                      <CloudLightning size={16} /> Open YouTube Studio
                    </button>
                  </div>
                )}
                <button 
                  onClick={handleLogout}
                  style={{ padding: "8px", borderRadius: 8, background: "#EA433520", border: `1px solid #EA433540`, color: "#EA4335", cursor: "pointer", fontSize: 12, fontWeight: 600 }}
                >
                  Logout
                </button>
              </div>
              
              <button 
                onClick={() => setAccountOpen(false)}
                style={{ marginTop: 32, width: "100%", padding: "12px", borderRadius: 12, background: c.bgApp, border: `1px solid ${c.border}`, color: c.textPrimary, cursor: "pointer", fontWeight: 500 }}
              >
                Close Profile
              </button>
            </motion.div>
          </>
        )}
      </AnimatePresence>

      {/* ━━━━ PASSWORD MODAL ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <PasswordModal
        isOpen={passwordModalOpen}
        onClose={() => {
          setPasswordModalOpen(false);
          setPendingPasswordOperation(null);
        }}
        onSubmit={async (password: string) => {
          if (pendingPasswordOperation) {
            await pendingPasswordOperation.callback(password);
          }
        }}
        title={pendingPasswordOperation?.title || "Enter Master Password"}
        description={pendingPasswordOperation?.description || "This operation requires your master password."}
        dark={dark}
        c={c}
      />

      {/* ━━━━ TOAST ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <AnimatePresence>
        {toast && (
          <motion.div
            initial={{ opacity: 0, y: 50, scale: 0.9 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, scale: 0.9, y: 20 }}
            style={{
              position: "fixed", bottom: 24, left: "50%", transform: "translateX(-50%)",
              background: toast?.type === "error" ? "#EA4335" : (toast?.type === "info" ? "#1A73E8" : "#323232"),
              color: "white", padding: "12px 24px", borderRadius: 8,
              fontSize: 14, fontWeight: 500, boxShadow: "0 4px 12px rgba(0,0,0,0.15)",
              zIndex: 9999, display: "flex", alignItems: "center", gap: 12
            }}
          >
            {toast?.msg}
          </motion.div>
        )}
      </AnimatePresence>
      </div>
    </>
  );
}

// ─── SignInView ──────────────────────────────────────────────────────────────

function SignInView({ c }: { c: ColorSet }) {
  const [loading, setLoading] = useState(false);
  const lock = useRef(false);

  const handleLogin = async () => {
    if (lock.current) return;
    lock.current = true;
    setLoading(true);
    try {
      const res = await fetch(`${API_BASE}/auth/login`, { method: "POST" });
      if (res.ok) {
        // Backend now handles securely launching the browser
        console.log("Login flow started by backend");
      }
    } catch (e) {
      console.error(e);
    } finally {
      // Keep it locked for 5 seconds to prevent any weird bounce
      setTimeout(() => {
        lock.current = false;
        setLoading(false);
      }, 5000);
    }
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", background: c.bgApp, padding: 40, textAlign: "center" }}>
      <div style={{ width: 80, height: 80, borderRadius: 24, background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", marginBottom: 32, boxShadow: "0 20px 40px rgba(26,115,232,0.3)" }}>
        <CloudLightning size={44} color="white" />
      </div>
      <h1 style={{ fontSize: 32, fontWeight: 700, color: c.textPrimary, marginBottom: 12 }}>Welcome to Nexus</h1>
      <p style={{ fontSize: 16, color: c.textSecondary, maxWidth: 400, marginBottom: 40, lineHeight: 1.6 }}>
        Your ultra-secure, decentralized storage backed by high-resilience YouTube archival.
      </p>
      
      <button 
        onClick={handleLogin}
        disabled={loading}
        style={{
          display: "flex", alignItems: "center", gap: 12,
          padding: "16px 32px", borderRadius: 16,
          background: "#1A73E8", color: "white",
          border: "none", fontSize: 16, fontWeight: 600,
          cursor: "pointer", boxShadow: "0 8px 16px rgba(26,115,232,0.2)",
          transition: "transform 0.1s, opacity 0.1s",
          opacity: loading ? 0.7 : 1,
        }}
      >
        <Plus size={20} color="white" />
        {loading ? "Check your browser..." : "Connect YouTube Account"}
      </button>
      
      <p style={{ marginTop: 32, fontSize: 13, color: c.textSecondary }}>
        Safe. Private. Unlimited.
      </p>
    </div>
  );
}

// ─── File Grid ────────────────────────────────────────────────────────────────

function FileGrid({ files, onSelect, selected, c, dark }: { files: NFile[]; onSelect: (f: NFile | null) => void; selected: NFile | null; c: ColorSet; dark: boolean }) {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))", gap: 12 }}>
      {files.map((f, i) => {
        const cfg = TYPE_CONFIG[f.type];
        const Ico = cfg.icon;
        const isSelected = selected?.id === f.id;
        return (
          <motion.div
            key={f.id}
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: i * 0.03, duration: 0.15 }}
            onClick={() => onSelect(isSelected ? null : f)}
            title={f.name}
            style={{
              borderRadius: 12,
              border: `1px solid ${isSelected ? "#1A73E8" : c.border}`,
              background: isSelected ? (dark ? "#1A3456" : "#E8F0FE") : c.bgSurface,
              cursor: "pointer",
              overflow: "hidden",
              transition: "border-color 0.15s, background 0.15s",
            }}
          >
            {/* Thumbnail area */}
            <div style={{ height: 100, background: cfg.bg, display: "flex", alignItems: "center", justifyContent: "center" }}>
              <Ico size={40} color={cfg.color} strokeWidth={1.5} />
            </div>
            {/* Info area */}
            <div style={{ padding: "10px 12px 12px", borderTop: `1px solid ${c.border}` }}>
              <p style={{ fontSize: 13, fontWeight: 500, color: c.textPrimary, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", marginBottom: 4 }}>
                {f.name}
              </p>
              <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                {f.encrypted && <Lock size={11} color="#059669" />}
                {f.starred && <Star size={11} color="#F59E0B" fill="#F59E0B" />}
                <span style={{ fontSize: 12, color: c.textSecondary, marginLeft: "auto" }}>{f.size}</span>
              </div>
            </div>
          </motion.div>
        );
      })}
    </div>
  );
}

// ─── File List ────────────────────────────────────────────────────────────────

function FileList({ files, onSelect, selected, c, dark }: { files: NFile[]; onSelect: (f: NFile | null) => void; selected: NFile | null; c: ColorSet; dark: boolean }) {
  return (
    <div style={{ borderRadius: 12, border: `1px solid ${c.border}`, overflow: "hidden" }}>
      {/* Header row */}
      <div style={{ display: "grid", gridTemplateColumns: "1fr 120px 160px 100px 40px", alignItems: "center", height: 44, padding: "0 16px", background: c.bgApp, borderBottom: `1px solid ${c.border}` }}>
        {["Name", "Shard ID", "Modified", "Size", ""].map(col => (
          <span key={col} style={{ fontSize: 12, fontWeight: 600, color: c.textSecondary, letterSpacing: 0.3 }}>{col}</span>
        ))}
      </div>
      {files.map((f, i) => {
        const cfg = TYPE_CONFIG[f.type];
        const Ico = cfg.icon;
        const isSelected = selected?.id === f.id;
        return (
          <div
            key={f.id}
            onClick={() => onSelect(isSelected ? null : f)}
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 120px 160px 100px 40px",
              alignItems: "center",
              height: 52,
              padding: "0 16px",
              background: isSelected ? (dark ? "#1A3456" : "#E8F0FE") : "transparent",
              borderBottom: i < files.length - 1 ? `1px solid ${c.border}` : "none",
              cursor: "pointer",
              transition: "background 0.1s",
            }}
            onMouseEnter={e => { if (!isSelected) (e.currentTarget as HTMLDivElement).style.background = c.bgHover; }}
            onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = isSelected ? (dark ? "#1A3456" : "#E8F0FE") : "transparent"; }}
          >
            {/* Name */}
            <div style={{ display: "flex", alignItems: "center", gap: 12, minWidth: 0 }}>
              <div style={{ width: 32, height: 32, borderRadius: 8, background: cfg.bg, display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0 }}>
                <Ico size={16} color={cfg.color} />
              </div>
              <span style={{ fontSize: 14, color: c.textPrimary, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{f.name}</span>
              {f.encrypted && <Lock size={12} color="#059669" style={{ flexShrink: 0 }} />}
              {f.starred && <Star size={12} color="#F59E0B" fill="#F59E0B" style={{ flexShrink: 0 }} />}
            </div>
            {/* Shard */}
            <span style={{ fontSize: 12, fontFamily: "monospace", color: c.textSecondary }}>{f.shardId}</span>
            {/* Modified */}
            <span style={{ fontSize: 13, color: c.textSecondary }}>{f.modified}</span>
            {/* Size */}
            <span style={{ fontSize: 13, color: c.textSecondary }}>{f.size}</span>
            {/* Actions */}
            <button style={{ border: "none", background: "transparent", cursor: "pointer", padding: 4, borderRadius: 8, color: c.textSecondary }}>
              <MoreVertical size={16} />
            </button>
          </div>
        );
      })}
    </div>
  );
}

// ─── Detail Panel ─────────────────────────────────────────────────────────────

function DetailPanel({ file, onClose, onAction, c, section }: { file: NFile; onClose: () => void; onAction: (action: "download" | "delete" | "star" | "restore" | "permanent" | "evict", file: NFile) => void; c: ColorSet; section: Section }) {
  const cfg = TYPE_CONFIG[file.type];
  const Ico = cfg.icon;
  return (
    <div style={{ width: 280, height: "100%", display: "flex", flexDirection: "column", padding: 20, overflowY: "auto" }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 20 }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", letterSpacing: 0.5 }}>File info</span>
        <button onClick={onClose} style={{ border: "none", background: "transparent", cursor: "pointer", color: c.textSecondary, padding: 4, borderRadius: 8 }}>
          <X size={18} />
        </button>
      </div>

      {/* Preview */}
      <div style={{ height: 140, borderRadius: 12, background: cfg.bg, display: "flex", alignItems: "center", justifyContent: "center", marginBottom: 16, border: `1px solid ${c.border}` }}>
        <Ico size={56} color={cfg.color} strokeWidth={1.5} />
      </div>

      {/* Filename */}
      <p style={{ fontSize: 15, fontWeight: 600, color: c.textPrimary, marginBottom: 4, wordBreak: "break-all" }}>{file.name}</p>
      <p style={{ fontSize: 13, color: c.textSecondary, marginBottom: 20 }}>{file.size}</p>

      {/* Properties */}
      <div style={{ display: "flex", flexDirection: "column", gap: 14, borderTop: `1px solid ${c.border}`, paddingTop: 16 }}>
        {[
          { label: "Shard ID", value: file.shardId },
          { label: "Last modified", value: file.modified },
          { label: "Encryption", value: file.encrypted ? "XChaCha20-Poly1305" : "None" },
          { label: "Status", value: "Verified ✓" },
          { label: "Owner", value: file.owner },
        ].map(row => (
          <div key={row.label}>
            <p style={{ fontSize: 11, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 2 }}>{row.label}</p>
            <p style={{ fontSize: 13, color: c.textPrimary, fontFamily: row.label === "Shard ID" ? "monospace" : "inherit" }}>{row.value}</p>
          </div>
        ))}
      </div>

      {/* Actions */}
      <div style={{ marginTop: "auto", paddingTop: 20, display: "flex", flexDirection: "column", gap: 8 }}>
        {file.deleted || section === "trash" ? (
          <>
            <button onClick={() => onAction("restore", file)} style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "#34A853", color: "white", border: "none", fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
              Restore File
            </button>
            <button onClick={() => onAction("permanent", file)} style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "transparent", color: "#EA4335", border: `1px solid ${c.border}`, fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
              Delete Permanently
            </button>
          </>
        ) : (
          <>
            <button onClick={() => onAction("download", file)} style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "#1A73E8", color: "white", border: "none", fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
              Open Shard
            </button>
            <button onClick={() => onAction("evict", file)} style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "transparent", color: c.textSecondary, border: `1px solid ${c.border}`, fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
              Free up space (clear local cache)
            </button>
            <button onClick={() => onAction("star", file)} style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "transparent", color: file.starred ? "#F59E0B" : c.textPrimary, border: `1px solid ${c.border}`, fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
              {file.starred ? "Unstar" : "Star"}
            </button>
            <button onClick={() => onAction("delete", file)} style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "transparent", color: "#EA4335", border: `1px solid ${c.border}`, fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
              Move to Trash
            </button>
          </>
        )}
      </div>
    </div>
  );
}

// ─── Task Overlay ─────────────────────────────────────────────────────────────

function TaskOverlay({ tasks, c }: { tasks: Record<string, BackendTask>; c: ColorSet }) {
  const [closedTasks, setClosedTasks] = useState<string[]>([]);
  
  // Hide internal manifest backup tasks — user doesn't need to see those
  const allTasks = Object.values(tasks)
    .filter(t => !t.id.startsWith("manifest-"))
    .filter(t => !closedTasks.includes(t.id));
  const activeTasks = allTasks.filter(t => t.status !== "Completed" && !t.status.startsWith("Error"));
  const finishedTasks = allTasks.filter(t => t.status === "Completed" || t.status.startsWith("Error"));

  if (activeTasks.length === 0 && finishedTasks.length === 0) return null;

  return (
    <div style={{
      position: "absolute", bottom: 16, right: 16,
      width: 320, background: c.bgSurface,
      borderRadius: 12, border: `1px solid ${c.border}`,
      boxShadow: "0 12px 32px rgba(0,0,0,0.15)",
      zIndex: 100, overflow: "hidden"
    }}>
      <div style={{ padding: "12px 16px", background: c.bgApp, borderBottom: `1px solid ${c.border}`, display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: c.textPrimary }}>Processing Files</span>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          {activeTasks.length > 0 && <span style={{ fontSize: 11, background: "#1A73E8", color: "white", padding: "2px 6px", borderRadius: 10 }}>{activeTasks.length}</span>}
          {finishedTasks.length > 0 && (
            <button 
              onClick={() => setClosedTasks([...closedTasks, ...finishedTasks.map(t => t.id)])}
              style={{ border: "none", background: "transparent", color: "#1A73E8", fontSize: 12, cursor: "pointer", fontWeight: 500 }}>
              Clear all
            </button>
          )}
        </div>
      </div>
      <div style={{ maxHeight: 320, overflowY: "auto" }}>
        {[...activeTasks, ...finishedTasks].map(t => {
          const isError = t.status.startsWith("Error");
          const isDone = t.status === "Completed";

          return (
            <div key={t.id} style={{ padding: "12px 16px", borderBottom: `1px solid ${c.border}`, position: "relative" }}>
              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 6, paddingRight: 20 }}>
                <span style={{ fontSize: 12, fontWeight: 500, color: c.textPrimary, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>
                  {t.filePath.split('/').pop()}
                </span>
                <span style={{ fontSize: 11, color: isError ? "#EA4335" : (isDone ? "#34A853" : c.textSecondary), fontWeight: (isError || isDone) ? 600 : 400 }}>
                  {t.status}
                </span>
              </div>
              <button 
                onClick={() => setClosedTasks([...closedTasks, t.id])}
                style={{ position: "absolute", top: 10, right: 10, border: "none", background: "transparent", cursor: "pointer", color: c.textSecondary, padding: 2 }}>
                <X size={14} />
              </button>
              <div style={{ height: 4, background: c.border, borderRadius: 2, overflow: "hidden" }}>
                <motion.div
                  initial={{ width: 0 }}
                  animate={{ width: `${t.progress}%` }}
                  style={{ height: "100%", background: isError ? "#EA4335" : (isDone ? "#34A853" : "#1A73E8") }}
                />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ─── Security Section ─────────────────────────────────────────────────────────

function SecuritySection({ c }: { c: ColorSet }) {
  const [protocols, setProtocols] = useState<{name: string, detail: string, active: boolean}[]>([]);
  const [, setPurgeDays] = useState(30);

  useEffect(() => {
    fetch(`${API_BASE}/security`)
      .then(res => res.json())
      .then(data => setProtocols(data))
      .catch(console.error);

    fetch(`${API_BASE}/settings/trash`)
      .then(res => res.json())
      .then(data => setPurgeDays(data.purge_days))
      .catch(console.error);
  }, []);

  /*
  const handleUpdatePurge = async (days: number) => {
    setPurgeDays(days);
    await fetch(`${API_BASE}/settings/trash`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ purge_days: days })
    });
  }
  */

  if (protocols.length === 0) return <div style={{ color: c.textSecondary }}>Loading security info...</div>;

  return (
    <div style={{ maxWidth: 640 }}>
      <p style={{ fontSize: 14, fontWeight: 500, color: c.textSecondary, marginBottom: 20 }}>Security Protocols</p>
      <div style={{ borderRadius: 12, border: `1px solid ${c.border}`, overflow: "hidden" }}>
        {protocols.map((p, i) => (
          <div key={p.name} style={{
            display: "flex",
            alignItems: "center",
            gap: 16,
            padding: "16px 20px",
            borderBottom: i < protocols.length - 1 ? `1px solid ${c.border}` : "none",
          }}>
            <div style={{
              width: 8, height: 8, borderRadius: "50%",
              background: p.active ? "#34A853" : c.border,
              boxShadow: p.active ? "0 0 6px #34A853" : "none",
              flexShrink: 0,
            }} />
            <div style={{ flex: 1 }}>
              <p style={{ fontSize: 14, fontWeight: 500, color: c.textPrimary, marginBottom: 2 }}>{p.name}</p>
              <p style={{ fontSize: 13, color: c.textSecondary }}>{p.detail}</p>
            </div>
            <span style={{
              fontSize: 11, fontWeight: 700,
              padding: "3px 8px", borderRadius: 6,
              background: p.active ? "#E6F4EA" : c.bgHover,
              color: p.active ? "#137333" : c.textSecondary,
              letterSpacing: 0.5,
              textTransform: "uppercase",
            }}>
              {p.active ? "Active" : "Pending"}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ─── Upload Modal ─────────────────────────────────────────────────────────────

function UploadModal({ onClose, onUpload, c }: { onClose: () => void; onUpload: (path: string, mode: string, password?: string, isFolder?: boolean) => void; c: ColorSet }) {
  const [mode, setMode] = useState<string>("base");
  const [password, setPassword] = useState("");
  const [isFolder, setIsFolder] = useState(false);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);

  const handleSelect = async () => {
    try {
      const res = await openDialog({
        multiple: false,
        directory: isFolder,
        title: isFolder ? "Select Folder to Upload" : "Select File to Upload",
      });
      if (res) {
        setSelectedPath(typeof res === 'string' ? res : res[0]);
      }
    } catch (err) {
      console.error(err);
    }
  };

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "20px 24px", borderBottom: `1px solid ${c.border}` }}>
        <span style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary }}>Upload to Nexus</span>
        <button onClick={onClose} style={{ border: "none", background: "transparent", cursor: "pointer", color: c.textSecondary }}>
          <X size={20} />
        </button>
      </div>
      <div style={{ padding: 24, display: "flex", flexDirection: "column", gap: 20 }}>
        
        {/* Toggle File/Folder */}
        <div style={{ display: "flex", background: c.bgApp, padding: 4, borderRadius: 12, border: `1px solid ${c.border}` }}>
           <button 
             onClick={() => { setIsFolder(false); setSelectedPath(null); }}
             style={{ flex: 1, padding: "8px", borderRadius: 8, border: "none", background: !isFolder ? c.bgSurface : "transparent", color: !isFolder ? c.textPrimary : c.textSecondary, fontWeight: 600, cursor: "pointer", fontSize: 13 }}
           >
             Single File
           </button>
           <button 
             onClick={() => { setIsFolder(true); setSelectedPath(null); }}
             style={{ flex: 1, padding: "8px", borderRadius: 8, border: "none", background: isFolder ? c.bgSurface : "transparent", color: isFolder ? c.textPrimary : c.textSecondary, fontWeight: 600, cursor: "pointer", fontSize: 13 }}
           >
             Folder (Archive)
           </button>
        </div>

        {/* Drop zone / Selector */}
        <div 
          onClick={handleSelect}
          style={{
            border: `2px dashed ${selectedPath ? "#34A853" : c.border}`, borderRadius: 16,
            padding: "30px 24px", display: "flex", flexDirection: "column",
            alignItems: "center", gap: 12, cursor: "pointer", textAlign: "center",
            background: selectedPath ? "rgba(52, 168, 83, 0.05)" : "transparent",
            transition: "all 0.2s",
          }}>
          <div style={{ width: 56, height: 56, borderRadius: 16, background: selectedPath ? "#E6F4EA" : "#E8F0FE", display: "flex", alignItems: "center", justifyContent: "center" }}>
            {selectedPath ? <Check size={28} color="#34A853" /> : (isFolder ? <Archive size={28} color="#1A73E8" /> : <Upload size={28} color="#1A73E8" />)}
          </div>
          <div style={{ flex: 1 }}>
            <p style={{ fontSize: 15, fontWeight: 500, color: c.textPrimary }}>
              {selectedPath ? (selectedPath.split('/').pop() || selectedPath) : (isFolder ? "Select Folder to Archive" : "Select File to Upload")}
            </p>
            <p style={{ fontSize: 13, color: c.textSecondary }}>
              {selectedPath ? "Path: " + selectedPath : "Nexus 2.0 Unified Channel Pipeline"}
            </p>
          </div>
        </div>

        {/* Custom Encryption Password */}
        <div>
           {(() => {
             const hasMasterPassword = localStorage.getItem('nexus_master_password') !== null;
             return (
               <>
                 <p style={{ fontSize: 12, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 10 }}>Custom Encryption Password (Optional)</p>
                 <div style={{ position: "relative" }}>
                   <Lock size={16} color={c.textSecondary} style={{ position: "absolute", left: 12, top: "50%", transform: "translateY(-50%)" }} />
                   <input 
                     type="password" 
                     placeholder={hasMasterPassword ? "Leave empty to use master password" : "Required: Set master password first or enter here"}
                     value={password}
                     onChange={(e) => setPassword(e.target.value)}
                     style={{ width: "100%", padding: "12px 12px 12px 40px", borderRadius: 10, background: c.bgSurface, border: `1px solid ${c.border}`, color: c.textPrimary, fontSize: 13 }}
                   />
                 </div>
                 
                 {/* Status indicator */}
                 <div style={{ marginTop: 8, fontSize: 11, color: hasMasterPassword ? "#4CAF50" : "#FF9800" }}>
                   {hasMasterPassword ? "✅ Master password is set (will be used if custom password is empty)" : "⚠️ No master password set — enter one below or set it in Settings"}
                 </div>

                 {!hasMasterPassword && (
                   <button
                     onClick={() => window.location.href = '/settings'}
                     style={{
                       marginTop: 12,
                       padding: "8px 12px",
                       fontSize: 12,
                       background: "#1A73E8",
                       color: "white",
                       border: "none",
                       borderRadius: 6,
                       cursor: "pointer",
                       transition: "all 0.15s",
                       width: "100%",
                       fontWeight: 500
                     }}
                     onMouseOver={(e) => (e.currentTarget.style.background = "#1565C0")}
                     onMouseOut={(e) => (e.currentTarget.style.background = "#1A73E8")}
                   >
                     🔒 Go to Settings to Set Master Password
                   </button>
                 )}
               </>
             );
           })()}
        </div>

        {/* Mode */}
        <div>
          <p style={{ fontSize: 12, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 10 }}>Encoding Mode</p>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
            {[
              { id: "base", name: "Base (Safe)", desc: "4×4 B&W — Max Resilience" },
              { id: "high", name: "High (Fast)", desc: "4×4 Gray — 3× Density" },
            ].map(m => (
              <button
                key={m.id}
                onClick={() => setMode(m.id)}
                style={{
                  padding: "12px 14px", borderRadius: 10, textAlign: "left",
                  background: mode === m.id ? "#E8F0FE" : "transparent",
                  border: `1.5px solid ${mode === m.id ? "#1A73E8" : c.border}`,
                  cursor: "pointer", transition: "all 0.15s",
                }}
              >
                <p style={{ fontSize: 13, fontWeight: 600, color: mode === m.id ? "#1A73E8" : c.textPrimary, marginBottom: 3 }}>{m.name}</p>
                <p style={{ fontSize: 12, color: c.textSecondary }}>{m.desc}</p>
              </button>
            ))}
          </div>
        </div>
        <button 
          disabled={!selectedPath}
          onClick={() => selectedPath && onUpload(selectedPath, mode, password, isFolder)}
          style={{ 
            width: "100%", padding: "13px 20px", borderRadius: 12, 
            background: !selectedPath ? c.border : "#1A73E8", 
            color: "white", border: "none", fontSize: 14, fontWeight: 500, 
            cursor: !selectedPath ? "not-allowed" : "pointer",
            opacity: !selectedPath ? 0.7 : 1,
            transition: "all 0.2s"
          }}>
          Start {isFolder ? "Archival & Upload" : "Upload"}
        </button>
      </div>
    </div>
  );
}

// ─── Small Helpers ────────────────────────────────────────────────────────────

function NavItem({ item, active, onClick, c }: { item: { id: string; icon: any; label: string }; active: boolean; onClick: () => void; c: ColorSet }) {
  const Ico = item.icon;
  return (
    <div
      onClick={onClick}
      style={{
        display: "flex", alignItems: "center", gap: 14,
        padding: "9px 16px", borderRadius: 24,
        background: active ? c.bgActive : "transparent",
        color: active ? c.textActive : c.textPrimary,
        fontSize: 14, fontWeight: active ? 600 : 400,
        cursor: "pointer", transition: "background 0.15s",
        userSelect: "none",
      }}
      onMouseEnter={e => { if (!active) (e.currentTarget as HTMLDivElement).style.background = c.bgHover; }}
      onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = active ? c.bgActive : "transparent"; }}
    >
      <Ico size={20} color={active ? c.iconActive : c.textSecondary} />
      {item.label}
    </div>
  );
}

function IconBtn({ onClick, title, children, dark, danger }: { onClick: () => void; title: string; children: React.ReactNode; dark: boolean; danger?: boolean }) {
  return (
    <button
      onClick={onClick}
      title={title}
      style={{
        width: 40, height: 40, borderRadius: "50%",
        border: "none", background: "transparent",
        display: "flex", alignItems: "center", justifyContent: "center",
        cursor: "pointer", color: danger ? "#EA4335" : (dark ? "#E3E3E3" : "#444746"),
        transition: "background 0.15s",
      }}
    >
      {children}
    </button>
  );
}

function ViewToggleBtn({ active, onClick, title, children, c }: { active: boolean; onClick: () => void; title: string; children: React.ReactNode; c: ColorSet }) {
  return (
    <button
      onClick={onClick}
      title={title}
      style={{
        width: 36, height: 36, borderRadius: 8,
        border: "none",
        background: active ? c.bgActive : "transparent",
        color: active ? c.iconActive : c.textSecondary,
        display: "flex", alignItems: "center", justifyContent: "center",
        cursor: "pointer", transition: "background 0.15s",
      }}
    >
      {children}
    </button>
  );
}

function EmptyState({ icon, title, sub, c }: { icon: React.ReactNode; title: string; sub: string; c: ColorSet }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", padding: "80px 40px", textAlign: "center", color: c.textSecondary }}>
      <div style={{ marginBottom: 16, opacity: 0.35 }}>{icon}</div>
      <p style={{ fontSize: 16, fontWeight: 500, color: c.textPrimary, marginBottom: 8 }}>{title}</p>
      <p style={{ fontSize: 14, maxWidth: 320 }}>{sub}</p>
    </div>
  );
}

// ─── Color Palettes ───────────────────────────────────────────────────────────

interface ColorSet {
  bgApp: string; bgSurface: string; bgSearch: string; bgHover: string; bgActive: string;
  textPrimary: string; textSecondary: string; textActive: string; iconActive: string;
  border: string; btnShadow: string;
}

const LIGHT: ColorSet = {
  bgApp:         "#F0F4F9",
  bgSurface:     "#FFFFFF",
  bgSearch:      "#DDE3EA",
  bgHover:       "#E2E8F0",
  bgActive:      "#C2E7FF",
  textPrimary:   "#1F1F1F",
  textSecondary: "#444746",
  textActive:    "#001D35",
  iconActive:    "#0842A0",
  border:        "#E0E0E0",
  btnShadow:     "0 1px 3px rgba(60,64,67,.3), 0 4px 8px rgba(60,64,67,.15)",
};

const DARK: ColorSet = {
  bgApp:         "#131314",
  bgSurface:     "#1E1F20",
  bgSearch:      "#2A2B2C",
  bgHover:       "#2D2E30",
  bgActive:      "#004A77",
  textPrimary:   "#E3E3E3",
  textSecondary: "#9AA0A6",
  textActive:    "#C2E7FF",
  iconActive:    "#8AB4F8",
  border:        "#3C4043",
  btnShadow:     "0 1px 3px rgba(0,0,0,.5), 0 4px 8px rgba(0,0,0,.3)",
};
const shimmer = `
  @keyframes shimmer {
    0% { background-position: -468px 0; }
    100% { background-position: 468px 0; }
  }
`;

function Skeleton({ width, height, borderRadius = 8, style = {} }: { width: string | number; height: string | number; borderRadius?: any; style?: any }) {
  return (
    <div className="skeleton-shimmer" style={{
      width, height, borderRadius,
      background: "#f6f7f8",
      backgroundImage: "linear-gradient(to right, #f6f7f8 0%, #edeef1 20%, #f6f7f8 40%, #f6f7f8 100%)",
      backgroundRepeat: "no-repeat",
      backgroundSize: "800px 104px",
      animation: "shimmer 1.2s linear infinite forwards",
      ...style
    }} />
  );
}

function FileSkeleton({ viewMode, c }: { viewMode: ViewMode; c: ColorSet }) {
  if (viewMode === "list") {
    return (
      <div style={{ display: "grid", gridTemplateColumns: "1fr 120px 160px 100px 40px", alignItems: "center", height: 52, padding: "0 16px", borderBottom: `1px solid ${c.border}` }}>
        <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
          <Skeleton width={32} height={32} />
          <Skeleton width={180} height={16} />
        </div>
        <Skeleton width={60} height={14} />
        <Skeleton width={100} height={14} />
        <Skeleton width={40} height={14} />
        <div style={{ display: "flex", justifyContent: "center" }}><Skeleton width={20} height={20} borderRadius="50%" /></div>
      </div>
    );
  }
  return (
    <div style={{ background: c.bgSurface, borderRadius: 16, border: `1px solid ${c.border}`, padding: 16, display: "flex", flexDirection: "column", gap: 12 }}>
      <Skeleton width="100%" height={140} borderRadius={12} />
      <Skeleton width="80%" height={18} />
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <Skeleton width="40%" height={12} />
        <Skeleton width={20} height={20} borderRadius="50%" />
      </div>
    </div>
  );
}

function SplashScreen({ b, loading, c }: { b: boolean; loading: boolean; c: ColorSet }) {
  return (
    <AnimatePresence>
      {!b && (
        <motion.div 
          initial={{ opacity: 1 }}
          exit={{ opacity: 0, scale: 1.05 }}
          transition={{ duration: 0.6, ease: "easeInOut" }}
          style={{ position: "fixed", inset: 0, background: c.bgApp, zIndex: 9999, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center" }}
        >
          <style>{shimmer}</style>
          <motion.div 
            animate={{ scale: [1, 1.1, 1], opacity: [0.8, 1, 0.8] }} 
            transition={{ duration: 2, repeat: Infinity, ease: "easeInOut" }}
            style={{ width: 80, height: 80, borderRadius: 24, background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", marginBottom: 24, boxShadow: "0 20px 40px rgba(26, 115, 232, 0.3)" }}
          >
            <CloudLightning size={44} color="white" />
          </motion.div>
          <h1 style={{ fontSize: 28, fontWeight: 600, color: c.textPrimary, letterSpacing: -0.5, marginBottom: 8, textAlign: "center" }}>Nexus Storage</h1>
          <div style={{ width: 40, height: 4, background: "#1A73E840", borderRadius: 2, overflow: "hidden", marginBottom: 16 }}>
             <motion.div animate={{ x: [-40, 40] }} transition={{ duration: 1.5, repeat: Infinity, ease: "linear" }} style={{ width: 20, height: "100%", background: "#1A73E8" }} />
          </div>
          <p style={{ fontSize: 13, color: c.textSecondary, letterSpacing: 1.5, fontWeight: 600 }}>
            {loading ? "FETCHING CLOUD SHARDS..." : "SECURE INITIALIZATION"}
          </p>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
