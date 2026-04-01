import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { 
  ArrowLeft, 
  Lock, 
  Info, 
  Shield, 
  AlertTriangle,
  ChevronRight,
  Check,
  RefreshCw,
  Eye,
  EyeOff
} from 'lucide-react';
import { motion, AnimatePresence } from 'framer-motion';

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
  const [activeTab, setActiveTab] = useState<'password' | 'about'>('password');
  const [showPasswordForm, setShowPasswordForm] = useState(false);
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [showNewPassword, setShowNewPassword] = useState(false);
  const [showConfirmPassword, setShowConfirmPassword] = useState(false);
  const [message, setMessage] = useState<{ text: string, type: 'success' | 'error' | '' }>({ text: '', type: '' });
  const [dark, setDark] = useState(document.documentElement.classList.contains('dark'));

  // Listen for dark mode changes
  useEffect(() => {
    const observer = new MutationObserver(() => {
      setDark(document.documentElement.classList.contains('dark'));
    });
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => observer.disconnect();
  }, []);

  const c = dark ? DARK : LIGHT;
  const hasPassword = !!localStorage.getItem('nexus_master_password');

  const showToast = (text: string, type: 'success' | 'error') => {
    setMessage({ text, type });
    setTimeout(() => setMessage({ text: '', type: '' }), 3000);
  };

  const handleSetPassword = async () => {
    if (newPassword.length < 12) {
      showToast('Password must be at least 12 characters', 'error');
      return;
    }
    if (newPassword !== confirmPassword) {
      showToast('Passwords do not match', 'error');
      return;
    }
    try {
      localStorage.setItem('nexus_master_password', newPassword);
      localStorage.setItem('nexus_recovery_salt', 'initialized');
      showToast('✅ Password set successfully!', 'success');
      setNewPassword('');
      setConfirmPassword('');
      setTimeout(() => setShowPasswordForm(false), 1500);
    } catch (err) {
      showToast('Failed to set password', 'error');
    }
  };

  const handleChangePassword = async () => {
    if (newPassword.length < 12) {
      showToast('Password must be at least 12 characters', 'error');
      return;
    }
    if (newPassword !== confirmPassword) {
      showToast('Passwords do not match', 'error');
      return;
    }
    try {
      localStorage.setItem('nexus_master_password', newPassword);
      showToast('✅ Password changed successfully', 'success');
      setNewPassword('');
      setConfirmPassword('');
      setTimeout(() => setShowPasswordForm(false), 1500);
    } catch (err) {
      showToast('Failed to change password', 'error');
    }
  };

  const navItems = [
    { id: 'password', label: 'Security & Password', icon: Lock },
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
      overflow: "hidden" 
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
                    padding: "10px 16px", borderRadius: 24,
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
          borderRadius: 16,
          border: `1px solid ${c.border}`,
          overflowY: "auto",
          padding: "32px",
          position: "relative"
        }}>
          <AnimatePresence mode="wait">
            {activeTab === 'password' && (
              <motion.div 
                key="password"
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -10 }}
                transition={{ duration: 0.2 }}
                style={{ maxWidth: 600 }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 24 }}>
                  <div style={{ width: 48, height: 48, borderRadius: 12, background: "#1A73E815", display: "flex", alignItems: "center", justifyContent: "center" }}>
                    <Shield size={24} color="#1A73E8" />
                  </div>
                  <div>
                    <h2 style={{ fontSize: 20, fontWeight: 600, color: c.textPrimary, margin: 0 }}>Master Password</h2>
                    <p style={{ fontSize: 14, color: c.textSecondary, margin: "4px 0 0 0" }}>Control how your files are encrypted</p>
                  </div>
                </div>

                {!showPasswordForm ? (
                  <div style={{ background: c.bgApp, padding: 24, borderRadius: 16, border: `1px solid ${c.border}` }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16 }}>
                      {hasPassword ? (
                        <Check size={20} color="#34A853" />
                      ) : (
                        <AlertTriangle size={20} color="#F9AB00" />
                      )}
                      <span style={{ fontSize: 15, fontWeight: 600, color: hasPassword ? "#34A853" : "#F9AB00" }}>
                        {hasPassword ? 'Password is active' : 'No password set'}
                      </span>
                    </div>
                    <p style={{ fontSize: 14, color: c.textSecondary, lineHeight: 1.6, marginBottom: 24 }}>
                      {hasPassword 
                        ? 'Your master password is used to derive encryption keys for all your file shards on YouTube. Changing it will only affect new uploads.'
                        : 'Set a master password to enable end-to-end encryption. Nexus uses Argon2id for key derivation and XChaCha20-Poly1305 for encryption.'}
                    </p>
                    <button 
                      onClick={() => setShowPasswordForm(true)}
                      style={{
                        padding: "10px 20px",
                        borderRadius: 10,
                        background: "#1A73E8",
                        color: "white",
                        border: "none",
                        fontSize: 14,
                        fontWeight: 600,
                        cursor: "pointer",
                        boxShadow: "0 2px 4px rgba(26,115,232,0.2)"
                      }}
                    >
                      {hasPassword ? 'Change Master Password' : 'Set Master Password'}
                    </button>
                  </div>
                ) : (
                  <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
                    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                      <label style={{ fontSize: 13, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", letterSpacing: 0.5 }}>New Password</label>
                      <div style={{ position: "relative" }}>
                        <input
                          type={showNewPassword ? "text" : "password"}
                          placeholder="Min. 12 characters recommended"
                          value={newPassword}
                          onChange={(e) => setNewPassword(e.target.value)}
                          style={{
                            width: "100%",
                            padding: "12px 44px 12px 16px",
                            borderRadius: 12,
                            background: c.bgApp,
                            border: `1px solid ${c.border}`,
                            color: c.textPrimary,
                            fontSize: 14,
                            outline: "none",
                            boxSizing: "border-box"
                          }}
                        />
                        <button
                          type="button"
                          onClick={() => setShowNewPassword(!showNewPassword)}
                          style={{
                            position: "absolute",
                            right: 12,
                            top: "50%",
                            transform: "translateY(-50%)",
                            background: "transparent",
                            border: "none",
                            cursor: "pointer",
                            color: c.textSecondary,
                            display: "flex",
                            alignItems: "center",
                            padding: 0
                          }}
                        >
                          {showNewPassword ? <EyeOff size={18} /> : <Eye size={18} />}
                        </button>
                      </div>
                      <div style={{ fontSize: 12, color: newPassword.length < 12 ? "#EA4335" : "#34A853", fontWeight: 500 }}>
                        {newPassword.length === 0 ? "" : (newPassword.length < 12 ? "Too short" : "Strength: Good")}
                      </div>
                    </div>

                    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                      <label style={{ fontSize: 13, fontWeight: 600, color: c.textSecondary, textTransform: "uppercase", letterSpacing: 0.5 }}>Confirm Password</label>
                      <div style={{ position: "relative" }}>
                        <input
                          type={showConfirmPassword ? "text" : "password"}
                          placeholder="Repeat password"
                          value={confirmPassword}
                          onChange={(e) => setConfirmPassword(e.target.value)}
                          style={{
                            width: "100%",
                            padding: "12px 44px 12px 16px",
                            borderRadius: 12,
                            background: c.bgApp,
                            border: `1px solid ${c.border}`,
                            color: c.textPrimary,
                            fontSize: 14,
                            outline: "none",
                            boxSizing: "border-box"
                          }}
                        />
                        <button
                          type="button"
                          onClick={() => setShowConfirmPassword(!showConfirmPassword)}
                          style={{
                            position: "absolute",
                            right: 12,
                            top: "50%",
                            transform: "translateY(-50%)",
                            background: "transparent",
                            border: "none",
                            cursor: "pointer",
                            color: c.textSecondary,
                            display: "flex",
                            alignItems: "center",
                            padding: 0
                          }}
                        >
                          {showConfirmPassword ? <EyeOff size={18} /> : <Eye size={18} />}
                        </button>
                      </div>
                    </div>

                    <div style={{ padding: 16, background: "#EA433510", border: "1px solid #EA433530", borderRadius: 12, display: "flex", gap: 12 }}>
                      <AlertTriangle size={20} color="#EA4335" style={{ flexShrink: 0 }} />
                      <p style={{ fontSize: 13, color: "#EA4335", margin: 0, lineHeight: 1.5 }}>
                        <strong>Warning:</strong> If you lose this password, your previously encrypted data cannot be recovered. Nexus does not store your password on any server.
                      </p>
                    </div>

                    <div style={{ display: "flex", gap: 12, marginTop: 12 }}>
                      <button 
                        onClick={() => { setShowPasswordForm(false); setNewPassword(''); setConfirmPassword(''); }}
                        style={{ flex: 1, padding: "12px", borderRadius: 12, background: "transparent", border: `1px solid ${c.border}`, color: c.textPrimary, cursor: "pointer", fontWeight: 500 }}
                      >
                        Cancel
                      </button>
                      <button 
                        onClick={hasPassword ? handleChangePassword : handleSetPassword}
                        style={{ flex: 1, padding: "12px", borderRadius: 12, background: "#1A73E8", color: "white", border: "none", cursor: "pointer", fontWeight: 600 }}
                      >
                        {hasPassword ? 'Update Password' : 'Save Password'}
                      </button>
                    </div>
                  </div>
                )}
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
                  <div style={{ width: 80, height: 80, borderRadius: 24, background: "#1A73E8", display: "flex", alignItems: "center", justifyContent: "center", margin: "0 auto 24px", boxShadow: "0 20px 40px rgba(26,115,232,0.2)" }}>
                    <RefreshCw size={44} color="white" />
                  </div>
                  <h2 style={{ fontSize: 28, fontWeight: 700, color: c.textPrimary, margin: "0 0 8px 0" }}>Nexus Storage</h2>
                  <p style={{ fontSize: 16, color: c.textSecondary, margin: 0 }}>v2.2.0 "Stellar Archival"</p>
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
                        <div key={f.title} style={{ padding: 16, background: c.bgApp, borderRadius: 12, border: `1px solid ${c.border}` }}>
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
                      href="https://github.com/KOUSSEMON-Aurel/Nexus-Storage" 
                      target="_blank" 
                      rel="noopener noreferrer"
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

      {/* ━━━━ TOAST ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ */}
      <AnimatePresence>
        {message.text && (
          <motion.div
            initial={{ opacity: 0, y: 50, scale: 0.9 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, scale: 0.9, y: 20 }}
            style={{
              position: "fixed", bottom: 24, left: "50%", transform: "translateX(-50%)",
              background: message.type === "error" ? "#EA4335" : "#323232",
              color: "white", padding: "12px 24px", borderRadius: 8,
              fontSize: 14, fontWeight: 500, boxShadow: "0 4px 12px rgba(0,0,0,0.15)",
              zIndex: 9999
            }}
          >
            {message.text}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
};

export default SettingsPage;
