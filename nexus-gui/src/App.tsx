import { useState, useEffect } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  Search, Plus, HardDrive, Shield, Clock, Star, Trash2,
  Grid3X3, List, FileText, FileImage, Archive, Lock,
  X, MoreVertical, Moon, Sun, CloudLightning, ChevronRight,
  Upload
} from "lucide-react";
import { open } from "@tauri-apps/plugin-dialog";

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
}

// ─── Constants ────────────────────────────────────────────────────────────────

const API_BASE = "http://localhost:8081/api";

interface BackendFile {
  ID: number;
  Path: string;
  VideoID: string;
  Size: number;
  Hash: string;
  Key: string;
  LastUpdate: string;
}

interface BackendTask {
  ID: string;
  Type: number;
  FilePath: string;
  Status: string;
  Progress: number;
  CreatedAt: string;
}

interface Stats {
  file_count: number;
  total_size: number;
}

function mapBackendToFile(bf: BackendFile): NFile {
  const name = bf.Path.split(/[/\\]/).pop() || bf.Path;
  const ext = name.split('.').pop()?.toLowerCase();
  
  let type: NFile["type"] = "archive";
  if (["png", "jpg", "jpeg", "gif"].includes(ext || "")) type = "image";
  else if (["pdf", "txt", "doc", "docx"].includes(ext || "")) type = "doc";
  else if (["enc", "key"].includes(ext || "")) type = "key";
  else if (["mp4", "mkv", "mov"].includes(ext || "")) type = "video";

  return {
    id: String(bf.ID),
    name: name,
    size: formatSize(bf.Size),
    type: type,
    modified: new Date(bf.LastUpdate).toLocaleDateString(),
    shardId: bf.VideoID.substring(0, 8),
    starred: false,
    encrypted: true,
    owner: "me"
  };
}

function formatSize(bytes: number): string {
  if (bytes === 0) return "0 B";
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

export default function App() {
  const [section, setSection] = useState<Section>("my-drive");
  const [viewMode, setViewMode] = useState<ViewMode>("grid");
  const [search, setSearch] = useState("");
  const [dark, setDark] = useState(false);
  const [selected, setSelected] = useState<NFile | null>(null);
  const [uploadOpen, setUploadOpen] = useState(false);
  
  const [dbFiles, setDbFiles] = useState<NFile[]>([]);
  const [tasks, setTasks] = useState<Record<string, BackendTask>>({});
  const [stats, setStats] = useState<Stats>({ file_count: 0, total_size: 0 });

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
  }, [dark]);

  // Polling for files, tasks, and stats
  useEffect(() => {
    const poll = async () => {
      try {
        const [filesRes, tasksRes, statsRes] = await Promise.all([
          fetch(`${API_BASE}/files`),
          fetch(`${API_BASE}/tasks`),
          fetch(`${API_BASE}/stats`)
        ]);
        
        if (filesRes.ok) {
          const data: BackendFile[] = await filesRes.json();
          setDbFiles(data.map(mapBackendToFile));
        }

        if (tasksRes.ok) {
          setTasks(await tasksRes.json());
        }

        if (statsRes.ok) {
          setStats(await statsRes.json());
        }
      } catch (err) {
        console.error("API Error:", err);
      }
    };

    poll();
    const interval = setInterval(poll, 5000);
    return () => clearInterval(interval);
  }, []);

  const handleUploadClick = async () => {
    try {
      const selectedPath = await open({
        multiple: false,
        directory: false,
      });

      if (selectedPath) {
        const res = await fetch(`${API_BASE}/upload`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: selectedPath })
        });
        if (res.ok) {
          setUploadOpen(false);
          // Trigger immediate poll
        }
      }
    } catch (err) {
      console.error("Upload error:", err);
    }
  };

  const files = dbFiles.filter(f => {
    const q = search.toLowerCase();
    const matchSearch = f.name.toLowerCase().includes(q);
    const matchSection =
      section === "starred" ? f.starred :
      section === "trash"   ? false : true;
    return matchSearch && matchSection;
  });

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
    <div style={{ background: c.bgApp, color: c.textPrimary, fontFamily: "'Inter', system-ui, sans-serif", height: "100vh", display: "flex", flexDirection: "column", overflow: "hidden" }}>
      
      {/* ━━━━ HEADER ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <header
        data-tauri-drag-region
        style={{
          height: 64,
          display: "flex",
          alignItems: "center",
          padding: "0 24px",
          gap: 16,
          background: dark ? "rgba(30,31,32,0.7)" : "rgba(240,244,249,0.7)",
          backdropFilter: "blur(12px)",
          borderBottom: `1px solid ${c.border}`,
          flexShrink: 0,
          cursor: "default",
          zIndex: 50,
        }}
      >
        {/* Logo - 256px to match sidebar */}
        <div style={{ width: 256, display: "flex", alignItems: "center", gap: 10, flexShrink: 0 }}>
          <div style={{ width: 40, height: 40, borderRadius: 12, background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center" }}>
            <CloudLightning size={22} color="white" />
          </div>
          <span style={{ fontSize: 22, fontWeight: 400, color: c.textPrimary, letterSpacing: -0.3 }}>Nexus</span>
        </div>

        {/* Search - grows to fill center */}
        <div style={{ flex: 1, display: "flex", alignItems: "center", gap: 12, background: c.bgSearch, borderRadius: 24, padding: "0 20px", height: 46 }}>
          <Search size={20} color={c.textSecondary} style={{ flexShrink: 0 }} />
          <input
            type="text"
            placeholder="Search in Nexus"
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
            }}
          />
        </div>

        {/* Actions on the right */}
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexShrink: 0 }}>
          <IconBtn onClick={() => setDark(d => !d)} title="Toggle theme" dark={dark}>
            {dark ? <Sun size={20} /> : <Moon size={20} />}
          </IconBtn>
          <IconBtn onClick={() => window.close()} title="Close" dark={dark} danger>
            <X size={20} />
          </IconBtn>
          {/* Avatar */}
          <div style={{ width: 36, height: 36, borderRadius: "50%", background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", color: "white", fontSize: 14, fontWeight: 600, marginLeft: 8, cursor: "pointer" }}>
            AK
          </div>
        </div>
      </header>

      {/* ━━━━ BODY ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
        
        {/* ━━━━ SIDEBAR ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
        <aside style={{ 
          width: 256, flexShrink: 0, 
          background: dark ? "rgba(30,31,32,0.4)" : "rgba(240,244,249,0.4)", 
          backdropFilter: "blur(12px)",
          display: "flex", flexDirection: "column", paddingTop: 16, paddingBottom: 16, overflow: "hidden",
          borderRight: `1px solid ${c.border}`
        }}>
          
          {/* New Button */}
          <div style={{ padding: "0 16px 16px" }}>
            <button
              onClick={handleUploadClick}
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
          </nav>

          {/* Storage */}
          <div style={{ padding: "12px 24px", borderTop: `1px solid ${c.border}` }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 8 }}>
              <HardDrive size={18} color={c.textSecondary} />
              <span style={{ fontSize: 13, color: c.textSecondary, fontWeight: 500 }}>Storage</span>
            </div>
            <div style={{ height: 4, background: c.border, borderRadius: 4, overflow: "hidden", marginBottom: 6 }}>
              <div style={{ width: `${Math.min(100, (stats.total_size / (1024 * 1024 * 1024 * 1024)) * 100)}%`, height: "100%", background: "#1A73E8", borderRadius: 4 }} />
            </div>
            <span style={{ fontSize: 12, color: c.textSecondary }}>{formatSize(stats.total_size)} of 15 GB used</span>
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
                    <SecuritySection c={c} stats={stats} />
                  ) : section === "trash" ? (
                    <EmptyState icon={<Trash2 size={64} />} title="Trash is empty" sub="Items deleted from Nexus are stored here temporarily." c={c} />
                  ) : files.length === 0 ? (
                    <EmptyState icon={<Search size={64} />} title="No files match your search" sub="Try a different search term." c={c} />
                  ) : (
                    <>
                      <p style={{ fontSize: 14, fontWeight: 500, color: c.textSecondary, marginBottom: 16 }}>
                        {section === "starred" ? "Starred" : "Suggested"}
                      </p>
                      {viewMode === "grid" ? (
                        <FileGrid files={files} onSelect={setSelected} selected={selected} c={c} dark={dark} />
                      ) : (
                        <FileList files={files} onSelect={setSelected} selected={selected} c={c} dark={dark} />
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
                  <DetailPanel file={selected} onClose={() => setSelected(null)} c={c} />
                </motion.div>
              )}
            </AnimatePresence>
          </div>
        </main>
      </div>

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
              initial={{ opacity: 0, scale: 0.96, y: 16 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.96, y: 16 }}
              transition={{ duration: 0.18 }}
              style={{
                position: "fixed", top: "50%", left: "50%",
                transform: "translate(-50%, -50%)",
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

function DetailPanel({ file, onClose, c }: { file: NFile; onClose: () => void; c: ColorSet }) {
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
        <button style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "#1A73E8", color: "white", border: "none", fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
          Open Shard
        </button>
        <button style={{ width: "100%", padding: "10px 16px", borderRadius: 10, background: "transparent", color: "#EA4335", border: `1px solid ${c.border}`, fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
          Delete
        </button>
      </div>
    </div>
  );
}

// ─── Task Overlay ─────────────────────────────────────────────────────────────

function TaskOverlay({ tasks, c }: { tasks: Record<string, BackendTask>; c: ColorSet }) {
  const activeTasks = Object.values(tasks).filter(t => t.Status !== "Completed" && !t.Status.startsWith("Error"));
  if (activeTasks.length === 0) return null;

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
        <span style={{ fontSize: 11, background: "#1A73E8", color: "white", padding: "2px 6px", borderRadius: 10 }}>{activeTasks.length}</span>
      </div>
      <div style={{ maxHeight: 240, overflowY: "auto" }}>
        {activeTasks.map(t => (
          <div key={t.ID} style={{ padding: "12px 16px", borderBottom: `1px solid ${c.border}` }}>
            <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 6 }}>
              <span style={{ fontSize: 12, fontWeight: 500, color: c.textPrimary, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>
                {t.FilePath.split('/').pop()}
              </span>
              <span style={{ fontSize: 11, color: c.textSecondary }}>{t.Status}</span>
            </div>
            <div style={{ height: 4, background: c.border, borderRadius: 2, overflow: "hidden" }}>
              <motion.div
                initial={{ width: 0 }}
                animate={{ width: `${t.Progress}%` }}
                style={{ height: "100%", background: "#1A73E8" }}
              />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// ─── Security Section ─────────────────────────────────────────────────────────

function SecuritySection({ c, stats }: { c: ColorSet; stats: Stats }) {
  const protocols = [
    { name: "XChaCha20-Poly1305 Encryption", detail: `${stats.file_count} files secured with unique keys`, active: true },
    { name: "Argon2id Key Derivation", detail: "64 MB memory, 3 passes — GPU resistant", active: true },
    { name: "SHA-256 + xxHash3 Integrity", detail: "Dual fingerprint verification on every shard", active: true },
    { name: "Tank Pixel Encoding (4×4 B&W)", detail: "High-resilience YouTube archival", active: true },
    { name: "Zero-Server Architecture", detail: "Local private index, no central database", active: true },
    { name: "Post-Quantum Cryptography", detail: "Kyber-768 planned for Phase 8", active: false },
  ];

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

function UploadModal({ onClose, onUpload, c }: { onClose: () => void; onUpload: () => void; c: ColorSet }) {
  const [mode, setMode] = useState<"tank" | "density">("tank");
  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "20px 24px", borderBottom: `1px solid ${c.border}` }}>
        <span style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary }}>Upload to Nexus</span>
        <button onClick={onClose} style={{ border: "none", background: "transparent", cursor: "pointer", color: c.textSecondary }}>
          <X size={20} />
        </button>
      </div>
      <div style={{ padding: 24, display: "flex", flexDirection: "column", gap: 20 }}>
        {/* Drop zone */}
        <div 
          onClick={onUpload}
          style={{
            border: `2px dashed ${c.border}`, borderRadius: 16,
            padding: "40px 24px", display: "flex", flexDirection: "column",
            alignItems: "center", gap: 12, cursor: "pointer", textAlign: "center",
          }}>
          <div style={{ width: 56, height: 56, borderRadius: 16, background: "#E8F0FE", display: "flex", alignItems: "center", justifyContent: "center" }}>
            <Upload size={28} color="#1A73E8" />
          </div>
          <p style={{ fontSize: 15, fontWeight: 500, color: c.textPrimary }}>Drop files here, or click to browse</p>
          <p style={{ fontSize: 13, color: c.textSecondary }}>Encrypted and sharded across the YouTube network</p>
        </div>
        {/* Mode */}
        <div>
          <p style={{ fontSize: 12, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 10 }}>Encoding Mode</p>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
            {[
              { id: "tank" as const, name: "Tank (Safe)", desc: "4×4 B&W — Maximum resilience" },
              { id: "density" as const, name: "Density (Fast)", desc: "2×2 nibbles — 4× throughput" },
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
          onClick={onUpload}
          style={{ width: "100%", padding: "13px 20px", borderRadius: 12, background: "#1A73E8", color: "white", border: "none", fontSize: 14, fontWeight: 500, cursor: "pointer" }}>
          Start Upload
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
