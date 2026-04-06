import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { 
  ArrowLeft, 
  Info,
  ChevronRight,
  RefreshCw,
  Lock,
  Shield,
  Key,
  Trash2,
  Clock,
  MousePointer2
} from 'lucide-react';
import { motion, AnimatePresence } from 'framer-motion';
import { openUrl } from '@tauri-apps/plugin-opener';
import { useSettings } from '../context/SettingsContext';

// ─── Color Palettes (Matching Dashboard) ─────────────────────────────────────

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

interface SettingsPageProps {
  onClose?: () => void;
}

const SettingsPage: React.FC<SettingsPageProps> = ({ onClose }) => {
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState<'encryption' | 'trash' | 'selection' | 'about'>('encryption');
  const { dark, persistentCheckboxes, setPersistentCheckboxes, interactionMode, setInteractionMode } = useSettings();
  const [trashRetentionDays, setTrashRetentionDays] = useState(30);

  const c = dark ? DARK : LIGHT;

  const navItems = [
    { id: 'encryption', label: 'Encryption & Security', icon: Shield },
    { id: 'selection', label: 'Selection & Interaction', icon: MousePointer2 },
    { id: 'trash', label: 'Trash & Storage', icon: Trash2 },
    { id: 'about', label: 'About Nexus', icon: Info },
  ];

  return (
    <div style={{ 
      background: c.bgApp, 
      color: c.textPrimary, 
      fontFamily: "'Inter', system-ui, sans-serif", 
      height: "100vh", 
      display: "flex", 
      flexDirection: "column", 
      overflow: "hidden",
      border: `1px solid ${c.border}`,
      boxShadow: "0 0 40px rgba(0,0,0,0.15)"
    }}>
      {/* ━━━━ HEADER ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <header style={{
        height: 64,
        display: "flex",
        alignItems: "center",
        padding: "0 24px",
        gap: 16,
        background: c.bgApp,
        flexShrink: 0,
        zIndex: 50,
      }}>
        <button
          onClick={() => onClose ? onClose() : navigate('/dashboard')}
          style={{
            width: 40,
            height: 40,
            borderRadius: "50%",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: "transparent",
            border: "none",
            cursor: "pointer",
            color: c.textPrimary,
            transition: "background 0.2s"
          }}
          onMouseEnter={e => (e.currentTarget as HTMLButtonElement).style.background = c.bgHover}
          onMouseLeave={e => (e.currentTarget as HTMLButtonElement).style.background = "transparent"}
        >
          <ArrowLeft size={22} />
        </button>
        <span style={{ fontSize: 22, fontWeight: 500, color: c.textPrimary, letterSpacing: -0.3 }}>Settings</span>
      </header>

      <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
        {/* ━━━━ SIDEBAR ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
        <aside style={{ 
          width: 256, flexShrink: 0, 
          background: c.bgApp,
          display: "flex", flexDirection: "column", paddingTop: 16, paddingBottom: 16, overflow: "hidden",
        }}>
          <nav style={{ flex: 1, padding: "0 8px", display: "flex", flexDirection: "column", gap: 2 }}>
            {navItems.map(item => {
              const Icon = item.icon;
              const active = activeTab === item.id;
              return (
                <div
                  key={item.id}
                  onClick={() => setActiveTab(item.id as any)}
                  style={{
                    display: "flex", alignItems: "center", gap: 14,
                    padding: "10px 16px", borderRadius: 12,
                    background: active ? c.bgActive : "transparent",
                    color: active ? c.textActive : c.textPrimary,
                    fontSize: 14, fontWeight: active ? 600 : 400,
                    cursor: "pointer", transition: "background 0.15s",
                    userSelect: "none",
                  }}
                  onMouseEnter={e => { if (!active) (e.currentTarget as HTMLDivElement).style.background = c.bgHover; }}
                  onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = active ? c.bgActive : "transparent"; }}
                >
                  <Icon size={20} color={active ? c.iconActive : c.textSecondary} />
                  {item.label}
                </div>
              );
            })}
          </nav>
        </aside>

        {/* ━━━━ MAIN CONTENT ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
        <main style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          background: c.bgSurface,
          margin: "8px 16px 8px 0",
          borderRadius: "var(--radius-lg)",
          border: `1px solid ${c.border}`,
          overflowY: "auto",
          padding: "32px",
          position: "relative"
        }}>
          <AnimatePresence mode="wait">
            {activeTab === 'selection' && (
              <motion.div 
                key="selection"
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -10 }}
                transition={{ duration: 0.2 }}
                style={{ maxWidth: 640 }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 24 }}>
                  <div style={{ width: 48, height: 48, borderRadius: "var(--radius-md)", background: "#1A73E815", display: "flex", alignItems: "center", justifyContent: "center" }}>
                    <MousePointer2 size={24} color="#1A73E8" />
                  </div>
                  <div>
                    <h2 style={{ fontSize: 20, fontWeight: 600, color: c.textPrimary, margin: 0 }}>Selection & Interaction</h2>
                    <p style={{ fontSize: 14, color: c.textSecondary, margin: "4px 0 0 0" }}>Configure how you manage multiple files</p>
                  </div>
                </div>

                <div style={{ display: "flex", flexDirection: "column", gap: 24 }}>
                  <section style={{ background: c.bgApp, padding: 20, borderRadius: 20, border: `1px solid ${c.border}` }}>
                    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 12 }}>
                      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                        <RefreshCw size={18} color={c.textSecondary} />
                        <h3 style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary, margin: 0 }}>Persistent Checkboxes</h3>
                      </div>
                      <div 
                        onClick={() => setPersistentCheckboxes(!persistentCheckboxes)}
                        style={{ 
                          width: 40, 
                          height: 20, 
                          borderRadius: 10, 
                          background: persistentCheckboxes ? "#34A853" : c.border, 
                          position: "relative", 
                          cursor: "pointer",
                          transition: "background 0.2s"
                        }}
                      >
                        <motion.div 
                          animate={{ x: persistentCheckboxes ? 22 : 2 }}
                          transition={{ type: "spring", stiffness: 500, damping: 30 }}
                          style={{ width: 16, height: 16, borderRadius: "50%", background: "white", position: "absolute", top: 2 }} 
                        />
                      </div>
                    </div>
                    <p style={{ fontSize: 14, color: c.textSecondary, margin: 0 }}>Always show selection checkboxes on items, even when nothing is selected.</p>
                  </section>

                  <section style={{ background: c.bgApp, padding: 20, borderRadius: 20, border: `1px solid ${c.border}` }}>
                    <h3 style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary, marginBottom: 16 }}>Interaction Mode</h3>
                    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
                      <div 
                        onClick={() => setInteractionMode('desktop')}
                        style={{ 
                          padding: 16, 
                          borderRadius: 12, 
                          background: c.bgSurface, 
                          border: `2px solid ${interactionMode === 'desktop' ? "#1A73E8" : "transparent"}`, 
                          outline: interactionMode === 'desktop' ? "none" : `1px solid ${c.border}`,
                          cursor: "pointer",
                          transition: "all 0.2s"
                        }}
                      >
                        <p style={{ fontSize: 14, fontWeight: 600, color: interactionMode === 'desktop' ? "#1A73E8" : c.textPrimary, margin: "0 0 4px 0" }}>Desktop (Default)</p>
                        <p style={{ fontSize: 12, color: c.textSecondary, margin: 0 }}>Standard Ctrl/Shift shortcuts</p>
                      </div>
                      <div 
                        onClick={() => setInteractionMode('selection')}
                        style={{ 
                          padding: 16, 
                          borderRadius: 12, 
                          background: c.bgSurface, 
                          border: `2px solid ${interactionMode === 'selection' ? "#1A73E8" : "transparent"}`, 
                          outline: interactionMode === 'selection' ? "none" : `1px solid ${c.border}`,
                          cursor: "pointer",
                          transition: "all 0.2s"
                        }}
                      >
                        <p style={{ fontSize: 14, fontWeight: 600, color: interactionMode === 'selection' ? "#1A73E8" : c.textPrimary, margin: "0 0 4px 0" }}>Selection Mode</p>
                        <p style={{ fontSize: 12, color: c.textSecondary, margin: 0 }}>Long-press to enter toggle mode</p>
                      </div>
                    </div>
                  </section>

                  {/* Keyboard Shortcuts Documentation */}
                  <section style={{ marginTop: 8 }}>
                    <h3 style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary, marginBottom: 16 }}>Keyboard Shortcuts</h3>
                    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                      {[
                        { key: "Ctrl + A", desc: "Select all files in current view" },
                        { key: "Escape", desc: "Clear selection & exit selection mode" },
                        { key: "Ctrl + Click", desc: "Toggle selection for a specific item" },
                        { key: "Shift + Click", desc: "Select a range of files" },
                        { key: "Enter", desc: "Confirm / Submit password modals" },
                        { key: "Empty Click", desc: "Click empty space to clear selection" },
                      ].map((item, i) => (
                        <div key={i} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 12px", background: c.bgApp, borderRadius: 10, border: `1px solid ${c.border}` }}>
                          <span style={{ fontSize: 13, color: c.textSecondary }}>{item.desc}</span>
                          <kbd style={{ 
                            padding: "2px 6px", 
                            background: c.bgSurface, 
                            border: `1px solid ${c.border}`, 
                            borderRadius: 4, 
                            fontSize: 11, 
                            fontFamily: "monospace", 
                            fontWeight: 600,
                            color: c.textPrimary,
                            boxShadow: `0 2px 0 ${c.border}`
                          }}>
                            {item.key}
                          </kbd>
                        </div>
                      ))}
                    </div>
                  </section>
                </div>
              </motion.div>
            )}
            {activeTab === 'encryption' && (
              <motion.div 
                key="encryption"
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -10 }}
                transition={{ duration: 0.2 }}
                style={{ maxWidth: 640 }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 24 }}>
                  <div style={{ width: 48, height: 48, borderRadius: "var(--radius-md)", background: "#34A85315", display: "flex", alignItems: "center", justifyContent: "center" }}>
                    <Lock size={24} color="#34A853" />
                  </div>
                  <div>
                    <h2 style={{ fontSize: 20, fontWeight: 600, color: c.textPrimary, margin: 0 }}>Encryption & Security</h2>
                    <p style={{ fontSize: 14, color: c.textSecondary, margin: "4px 0 0 0" }}>Your files protected by zero-knowledge encryption</p>
                  </div>
                </div>

                <div style={{ display: "flex", flexDirection: "column", gap: 24 }}>
                  {/* Auto-Encryption Section */}
                  <section style={{ background: c.bgApp, padding: 20, borderRadius: 20, border: `1px solid ${c.border}` }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
                      <Key size={20} color="#34A853" />
                      <h3 style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary, margin: 0 }}>Auto-Encryption (Google-Based)</h3>
                      <span style={{ fontSize: 11, background: "#34A85320", color: "#34A853", padding: "2px 8px", borderRadius: 6, fontWeight: 600, marginLeft: "auto" }}>ALWAYS ON</span>
                    </div>
                    <p style={{ fontSize: 14, color: c.textSecondary, lineHeight: 1.6, margin: "0 0 12px 0" }}>
                      Your files are automatically encrypted using your permanent Google identity. No passwords to remember, no secrets to manage.
                    </p>
                    <div style={{ background: c.bgSurface, padding: 12, borderRadius: 10, border: `1px solid ${c.border}`, fontSize: 13, color: c.textSecondary }}>
                      <p style={{ margin: "0 0 6px 0" }}>✅ <strong>Deterministic:</strong> Same encryption key across all devices</p>
                      <p style={{ margin: "0 0 6px 0" }}>✅ <strong>Zero-Knowledge:</strong> No passwords stored anywhere</p>
                      <p style={{ margin: 0 }}>✅ <strong>Instant:</strong> Works the moment you authenticate</p>
                    </div>
                  </section>

                  {/* Custom Override Section */}
                  <section style={{ background: c.bgApp, padding: 20, borderRadius: 20, border: `1px solid ${c.border}` }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
                      <Shield size={20} color="#1A73E8" />
                      <h3 style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary, margin: 0 }}>Custom Password (Optional)</h3>
                      <span style={{ fontSize: 11, background: "#1A73E820", color: "#1A73E8", padding: "2px 8px", borderRadius: 6, fontWeight: 600, marginLeft: "auto" }}>OPTIONAL</span>
                    </div>
                    <p style={{ fontSize: 14, color: c.textSecondary, lineHeight: 1.6, margin: "0 0 12px 0" }}>
                      Add an extra layer of protection to individual files during upload. Works alongside automatic encryption for maximum security.
                    </p>
                    <div style={{ background: c.bgSurface, padding: 12, borderRadius: 10, border: `1px solid ${c.border}`, fontSize: 13, color: c.textSecondary }}>
                      <p style={{ margin: "0 0 6px 0" }}>🔒 <strong>Per-File:</strong> Set different passwords for different files</p>
                      <p style={{ margin: "0 0 6px 0" }}>🔓 <strong>Backward Compatible:</strong> Works with old uploads too</p>
                      <p style={{ margin: 0 }}>⚡ <strong>Recommended:</strong> Leave empty for simplicity</p>
                    </div>
                  </section>

                  {/* Technical Details */}
                  <section>
                    <h3 style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary, marginBottom: 12 }}>How It Works</h3>
                    <div style={{ background: c.bgApp, padding: 16, borderRadius: "var(--radius-md)", border: `1px solid ${c.border}`, fontSize: 13, color: c.textSecondary, lineHeight: 1.7 }}>
                      <p style={{ margin: "0 0 12px 0" }}>
                        <strong style={{ color: c.textPrimary }}>Your Google Account</strong> contains a permanent, unique ID called "sub". Nexus uses this ID with cryptographic key derivation (PBKDF2-SHA256) to generate your encryption key.
                      </p>
                      <p style={{ margin: "0 0 12px 0" }}>
                        This means: <strong>same user = same key everywhere</strong>. You can restore your files on any device just by logging in. No recovery codes needed.
                      </p>
                      <p style={{ margin: 0 }}>
                        <strong style={{ color: c.textPrimary }}>Security:</strong> Different users get different keys automatically. Your encryption is only valid for your Google account.
                      </p>
                    </div>
                  </section>

                  {/* FAQ */}
                  <section>
                    <h3 style={{ fontSize: 16, fontWeight: 600, color: c.textPrimary, marginBottom: 12 }}>Frequently Asked</h3>
                    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
                      {[
                        { q: "What if I lose my Google account?", a: "Your encrypted files remain secure on YouTube, but will be inaccessible without your account." },
                        { q: "Can someone else decrypt my files?", a: "No. Your encryption key is unique to your Google account and never shared." },
                        { q: "Do I need to set a password?", a: "No. Auto-encryption is completely automatic. Passwords are optional for paranoid users." },
                        { q: "Can I change my encryption key?", a: "Your key is tied to your Google account. You can't change it, but you can download and re-upload files with a custom password if needed." },
                      ].map((item, i) => (
                        <div key={i} style={{ padding: 12, background: c.bgSurface, borderRadius: 10, border: `1px solid ${c.border}` }}>
                          <p style={{ fontSize: 13, fontWeight: 600, color: c.textPrimary, margin: "0 0 6px 0" }}>{item.q}</p>
                          <p style={{ fontSize: 12, color: c.textSecondary, margin: 0 }}>{item.a}</p>
                        </div>
                      ))}
                    </div>
                  </section>
                </div>
              </motion.div>
            )}

            {activeTab === 'trash' && (
              <motion.div 
                key="trash"
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -10 }}
                transition={{ duration: 0.2 }}
                style={{ maxWidth: 640 }}
              >
                <div style={{ padding: "20px 0" }}>
                  <section style={{ marginBottom: 32 }}>
                    <h2 style={{ fontSize: 16, fontWeight: 600, marginBottom: 16, display: "flex", alignItems: "center", gap: 10 }}>
                      <Trash2 size={20} /> Trash & Storage
                    </h2>
                    
                    <div style={{ background: c.bgSurface, padding: 16, borderRadius: "var(--radius-md)", border: `1px solid ${c.border}`, marginBottom: 16 }}>
                      <label style={{ fontSize: 12, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", display: "block" }}>
                        <Clock size={14} style={{ display: "inline", marginRight: 6 }} /> Auto-Empty Trash After (days)
                      </label>
                      <div style={{ display: "flex", alignItems: "center", gap: 12, marginTop: 12 }}>
                        <input 
                          type="range"
                          min="1"
                          max="90"
                          value={trashRetentionDays}
                          onChange={(e) => setTrashRetentionDays(parseInt(e.target.value))}
                          style={{ flex: 1, height: 6, borderRadius: 3 }}
                        />
                        <span style={{ fontSize: 14, fontWeight: 600, minWidth: 50 }}>{trashRetentionDays} days</span>
                      </div>
                      <p style={{ fontSize: 12, color: c.textSecondary, marginTop: 12 }}>
                        Files in trash will be permanently deleted after {trashRetentionDays} days of inactivity.
                      </p>
                    </div>

                    <div style={{ background: "#FEF3E2", padding: 12, borderRadius: 8, borderLeft: "4px solid #F59E0B", marginBottom: 16 }}>
                      <p style={{ fontSize: 12, color: "#92400E" }}>
                        ⚠️ <strong>Warning:</strong> Permanently deleted files cannot be recovered from trash. Ensure you have a backup before emptying.
                      </p>
                    </div>

                    <button style={{
                      width: "100%",
                      padding: "12px 16px",
                      borderRadius: "var(--radius-sm)",
                      background: "#EA4335",
                      color: "white",
                      border: "none",
                      fontSize: 14,
                      fontWeight: 500,
                      cursor: "pointer",
                      transition: "opacity 0.2s"
                    }}
                    onMouseEnter={(e) => (e.currentTarget as HTMLButtonElement).style.opacity = "0.9"}
                    onMouseLeave={(e) => (e.currentTarget as HTMLButtonElement).style.opacity = "1"}
                    >
                      Empty Trash Now
                    </button>
                  </section>
                </div>
              </motion.div>
            )}

            {activeTab === 'about' && (
              <motion.div 
                key="about"
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -10 }}
                transition={{ duration: 0.2 }}
                style={{ maxWidth: 640 }}
              >
                <div style={{ textAlign: "center", padding: "20px 0 40px" }}>
                  <div style={{ width: 80, height: 80, borderRadius: "var(--radius-xl)", background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", margin: "0 auto 24px", boxShadow: "0 20px 40px rgba(26,115,232,0.2)" }}>
                    <RefreshCw size={44} color="white" />
                  </div>
                  <h2 style={{ fontSize: 28, fontWeight: 700, color: c.textPrimary, margin: "0 0 8px 0" }}>Nexus Storage</h2>
                  <p style={{ fontSize: 16, color: c.textSecondary, margin: 0 }}>v5.0.0 "Nova Galactic"</p>
                </div>

                <div style={{ display: "flex", flexDirection: "column", gap: 32 }}>
                  <section>
                    <h3 style={{ fontSize: 18, fontWeight: 600, color: c.textPrimary, marginBottom: 12 }}>Our Mission</h3>
                    <p style={{ fontSize: 15, color: c.textSecondary, lineHeight: 1.7 }}>
                      Nexus transforms your sensitive files into high-density encrypted video streams, 
                      storing them across the most resilient video infrastructure on the planet. 
                      By leveraging YouTube as a decentralized archival layer, we provide 
                      unparalleled durability with zero-knowledge privacy.
                    </p>
                  </section>

                  <section>
                    <h3 style={{ fontSize: 18, fontWeight: 600, color: c.textPrimary, marginBottom: 16 }}>Key Features</h3>
                    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 16 }}>
                      {[
                        { title: "End-to-End Encryption", desc: "XChaCha20-Poly1305 security" },
                        { title: "Resilient Archival", desc: "Multi-region YouTube storage" },
                        { title: "Metadata Stealth", desc: "Private file names & sizes" },
                        { title: "Universal Access", desc: "Cloud-sync manifest recovery" },
                      ].map(f => (
                        <div key={f.title} style={{ padding: 16, background: c.bgApp, borderRadius: "var(--radius-md)", border: `1px solid ${c.border}` }}>
                          <p style={{ fontSize: 14, fontWeight: 600, color: c.textPrimary, margin: "0 0 4px 0" }}>{f.title}</p>
                          <p style={{ fontSize: 12, color: c.textSecondary, margin: 0 }}>{f.desc}</p>
                        </div>
                      ))}
                    </div>
                  </section>

                  <section style={{ padding: "24px 0", borderTop: `1px solid ${c.border}`, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                      <Info size={20} color={c.textPrimary} />
                      <span style={{ fontSize: 14, fontWeight: 500 }}>Open Source Project</span>
                    </div>
                    <a 
                      href="#"
                      onClick={(e) => {
                        e.preventDefault();
                        openUrl("https://github.com/KOUSSEMON-Aurel/Nexus-Storage");
                      }}
                      style={{ 
                        fontSize: 14, 
                        color: "#1A73E8", 
                        textDecoration: "none", 
                        fontWeight: 600,
                        display: "flex",
                        alignItems: "center",
                        gap: 4
                      }}
                    >
                      View on GitHub <ChevronRight size={16} />
                    </a>
                  </section>
                </div>
              </motion.div>
            )}
          </AnimatePresence>
        </main>
      </div>
    </div>
  );
};

export default SettingsPage;
