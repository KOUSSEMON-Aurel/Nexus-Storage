// nexus-gui/src/pages/RecoveryPage.tsx
import React, { useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { useNavigate } from 'react-router-dom';
import '../styles/RecoveryPage.css';

interface DeriveKeyResponse {
  master_key: string;
}

interface RecoveryRestoreResponse {
  status: string;
  file_count: number;
  message: string;
}

const RecoveryPage: React.FC = () => {
  const [password, setPassword] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [step, setStep] = useState<'input' | 'processing' | 'success'>('input');
  const [recoveredFiles, setRecoveredFiles] = useState(0);
  const navigate = useNavigate();

  const handleRecover = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setSuccess('');

    if (!password) {
      setError('Please enter your password');
      return;
    }

    setIsLoading(true);
    setStep('processing');

    try {
      console.log('📥 Starting recovery process...');

      // Get stored recovery salt (this is public, stored locally)
      const storedSalt = localStorage.getItem('nexus_recovery_salt');
      if (!storedSalt) {
        setError('Recovery salt not found. Cannot proceed with recovery.');
        setStep('input');
        setIsLoading(false);
        return;
      }

      // Derive master key with entered password
      console.log('🔑 Deriving master key from password...');
      const keyResp = await invoke<DeriveKeyResponse>('derive_master_key', {
        password,
        salt: storedSalt,
      });
      const masterKey = keyResp.master_key;

      // Call recovery endpoint on daemon
      console.log('📡 Downloading encrypted manifest from Drive...');
      const restoreResp = await invoke<RecoveryRestoreResponse>('tauri_recovery_restore', {
        master_key_hex: masterKey,
      });

      console.log('✅ Recovery complete:', restoreResp.message);
      setSuccess(
        `✅ Recovery successful!\n\nRestored ${restoreResp.file_count} files to your local database.\n\nPlease restart the app to sync with YouTube.`
      );
      setRecoveredFiles(restoreResp.file_count);
      setStep('success');

      // Auto-redirect to dashboard after 3 seconds
      setTimeout(() => {
        navigate('/dashboard');
      }, 3000);
    } catch (err: any) {
      console.error('Recovery error:', err);
      setError(
        `Recovery failed: ${err.toString()}\n\nPossible causes:\n- Wrong password\n- Manifest not backed up to Drive\n- Network error`
      );
      setStep('input');
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="recovery-container">
      <div className="recovery-box">
        <h1>📥 Recover from Backup</h1>

        {step === 'input' && (
          <form onSubmit={handleRecover}>
            <p className="hint">
              To recover your files, enter the <strong>same password</strong> you used during setup.
              Your encrypted manifest will be downloaded from Google Drive and decrypted locally.
            </p>

            <div className="form-group">
              <label>Password</label>
              <input
                type="password"
                placeholder="Enter your password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={isLoading}
                required
              />
            </div>

            <div className="warning">
              ⏱️ Recovery may take several minutes if you have many files.
            </div>

            {error && <div className="error">{error}</div>}

            <button type="submit" disabled={isLoading} className="btn-primary">
              {isLoading ? '⏳ Recovering...' : '🚀 Start Recovery'}
            </button>

            <button
              type="button"
              onClick={() => navigate('/login')}
              className="btn-secondary"
              disabled={isLoading}
            >
              ← Back to Login
            </button>
          </form>
        )}

        {step === 'processing' && (
          <div className="processing">
            <div className="spinner"></div>
            <h2>🔄 Recovery in progress...</h2>
            <p>
              Downloading manifest from Drive<br />
              Decrypting with your password<br />
              Restoring files to database
            </p>
          </div>
        )}

        {step === 'success' && (
          <div className="success-box">
            <h2>✅ Recovery Successful!</h2>
            <p>{success}</p>
            <p className="info">Recovered files: <strong>{recoveredFiles}</strong></p>
            <button type="button" onClick={() => navigate('/dashboard')} className="btn-primary">
              🚀 Go to Dashboard
            </button>
          </div>
        )}
      </div>
    </div>
  );
};

export default RecoveryPage;
