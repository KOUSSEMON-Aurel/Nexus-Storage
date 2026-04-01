import React, { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { Lock, X } from 'lucide-react';

interface PasswordModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmit: (password: string) => Promise<void>;
  title?: string;
  description?: string;
  dark?: boolean;
  c?: any;
}

const PasswordModal: React.FC<PasswordModalProps> = ({
  isOpen,
  onClose,
  onSubmit,
  title = 'Enter Master Password',
  description = 'This operation requires your master password for encryption/decryption.',
  dark = false,
  c
}) => {
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const bgColor = c?.bgSurface || (dark ? '#1E1F20' : '#FFFFFF');
  const textColor = c?.textPrimary || (dark ? '#E3E3E3' : '#1F1F1F');
  const borderColor = c?.border || (dark ? '#3C4043' : '#E0E0E0');
  const bgApp = c?.bgApp || (dark ? '#131314' : '#F0F4F9');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    
    if (!password) {
      setError('Password required');
      return;
    }

    setLoading(true);
    setError('');

    try {
      await onSubmit(password);
      setPassword('');
      onClose();
    } catch (err: any) {
      setError(err?.message || 'Invalid password or operation failed');
    } finally {
      setLoading(false);
    }
  };

  const handleClose = () => {
    setPassword('');
    setError('');
    onClose();
  };

  return (
    <AnimatePresence>
      {isOpen && (
        <>
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={handleClose}
            style={{
              position: 'fixed',
              inset: 0,
              background: 'rgba(0,0,0,0.5)',
              zIndex: 300,
              userSelect: 'none'
            }}
          />
          <motion.div
            initial={{ opacity: 0, scale: 0.96, y: -20 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.96, y: -20 }}
            style={{
              position: 'fixed',
              top: '50%',
              left: '50%',
              transform: 'translate(-50%, -50%)',
              width: 420,
              zIndex: 301,
              background: bgColor,
              border: `1px solid ${borderColor}`,
              borderRadius: 24,
              boxShadow: '0 24px 60px rgba(0,0,0,0.3)',
              padding: 32,
              display: 'flex',
              flexDirection: 'column',
              gap: 24
            }}
          >
            {/* Header */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <div
                  style={{
                    width: 44,
                    height: 44,
                    borderRadius: 12,
                    background: 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center'
                  }}
                >
                  <Lock size={22} color="white" />
                </div>
                <div>
                  <h2 style={{ fontSize: 18, fontWeight: 600, color: textColor, margin: 0 }}>
                    {title}
                  </h2>
                </div>
              </div>
              <button
                onClick={handleClose}
                style={{
                  background: 'none',
                  border: 'none',
                  color: textColor,
                  cursor: 'pointer',
                  padding: 8,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center'
                }}
              >
                <X size={20} />
              </button>
            </div>

            {/* Description */}
            {description && (
              <p style={{
                fontSize: 14,
                color: c?.textSecondary || (dark ? '#9AA0A6' : '#444746'),
                margin: 0,
                lineHeight: 1.5
              }}>
                {description}
              </p>
            )}

            {/* Password Form */}
            <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                <label style={{
                  fontSize: 13,
                  fontWeight: 600,
                  color: textColor
                }}>
                  Master Password
                </label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => {
                    setPassword(e.target.value);
                    setError('');
                  }}
                  placeholder="Enter your master password"
                  autoFocus
                  style={{
                    width: '100%',
                    padding: '12px 14px',
                    borderRadius: 12,
                    border: `1px solid ${error ? '#EA4335' : borderColor}`,
                    background: bgApp,
                    color: textColor,
                    fontSize: 14,
                    fontFamily: 'inherit',
                    outline: 'none',
                    transition: 'border-color 0.2s',
                    boxSizing: 'border-box'
                  }}
                  onFocus={(e) => {
                    e.currentTarget.style.borderColor = error ? '#EA4335' : '#667eea';
                  }}
                  onBlur={(e) => {
                    e.currentTarget.style.borderColor = error ? '#EA4335' : borderColor;
                  }}
                />
              </div>

              {/* Error Message */}
              {error && (
                <div style={{
                  padding: '10px 12px',
                  borderRadius: 8,
                  background: '#EA433520',
                  border: '1px solid #EA433540',
                  color: '#EA4335',
                  fontSize: 13,
                  fontWeight: 500,
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8
                }}>
                  <span>❌</span>
                  <span>{error}</span>
                </div>
              )}

              {/* Actions */}
              <div style={{ display: 'flex', gap: 12, marginTop: 8 }}>
                <button
                  type="button"
                  onClick={handleClose}
                  disabled={loading}
                  style={{
                    flex: 1,
                    padding: '12px',
                    borderRadius: 12,
                    border: `1px solid ${borderColor}`,
                    background: bgApp,
                    color: textColor,
                    cursor: loading ? 'not-allowed' : 'pointer',
                    fontWeight: 500,
                    fontSize: 14,
                    opacity: loading ? 0.6 : 1,
                    transition: 'all 0.2s'
                  }}
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={loading || !password}
                  style={{
                    flex: 1,
                    padding: '12px',
                    borderRadius: 12,
                    border: 'none',
                    background: loading || !password ? '#667eea80' : 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)',
                    color: 'white',
                    cursor: loading || !password ? 'not-allowed' : 'pointer',
                    fontWeight: 600,
                    fontSize: 14,
                    transition: 'all 0.2s'
                  }}
                >
                  {loading ? '⏳ Verifying...' : 'Unlock'}
                </button>
              </div>
            </form>

            {/* Security Note */}
            <div style={{
              padding: '12px 14px',
              borderRadius: 8,
              background: 'transparent',
              border: `1px dashed ${borderColor}`,
              fontSize: 12,
              color: c?.textSecondary || (dark ? '#9AA0A6' : '#444746'),
              lineHeight: 1.5
            }}>
              🔐 <strong>Your password is never stored on servers</strong> - only used locally for encryption/decryption
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
};

export default PasswordModal;
