// nexus-gui/src/pages/LoginPage.tsx
import React, { useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { useNavigate } from 'react-router-dom';
import '../styles/LoginPage.css';

interface GenerateSaltResponse {
  salt: string;
}

interface DeriveKeyResponse {
  master_key: string;
}

interface SessionResponse {
  status: string;
  message: string;
}

const LoginPage: React.FC = () => {
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const navigate = useNavigate();

  // Called on first app launch - generate recovery salt + set password
  const handleInitialSetup = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');

    if (password.length < 12) {
      setError('Password must be at least 12 characters');
      return;
    }

    if (password !== confirmPassword) {
      setError('Passwords do not match');
      return;
    }

    setIsLoading(true);
    try {
      // Generate recovery salt
      console.log('🔐 Generating recovery salt...');
      const saltResp = await invoke<GenerateSaltResponse>('generate_recovery_salt');
      const salt = saltResp.salt;

      // Derive master key with this password + salt
      console.log('🔑 Deriving master key from password...');
      const keyResp = await invoke<DeriveKeyResponse>('derive_master_key', {
        password,
        salt,
      });
      const masterKey = keyResp.master_key;

      // Start session on daemon
      console.log('📡 Starting session with daemon...');
      const sessionResp = await invoke<SessionResponse>('tauri_session_start', {
        master_key_hex: masterKey,
        action: 'init_setup',
      });

      console.log('✅ Setup complete:', sessionResp.message);

      // Store salt locally (not sensitive - recovery salt is public)
      localStorage.setItem('nexus_recovery_salt', salt);
      localStorage.setItem('nexus_initialized', 'true');

      // Redirect to main app
      navigate('/dashboard');
    } catch (err: any) {
      setError(err.toString());
      console.error('Setup error:', err);
    } finally {
      setIsLoading(false);
    }
  };

  // Called on subsequent launches - derive key with stored salt
  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');

    if (!password) {
      setError('Please enter your password');
      return;
    }

    setIsLoading(true);
    try {
      const storedSalt = localStorage.getItem('nexus_recovery_salt');
      if (!storedSalt) {
        setError('Recovery salt not found. Please reinstall or recover from backup.');
        return;
      }

      console.log('🔑 Deriving master key from password...');
      const keyResp = await invoke<DeriveKeyResponse>('derive_master_key', {
        password,
        salt: storedSalt,
      });
      const masterKey = keyResp.master_key;

      console.log('📡 Starting session with daemon...');
      const sessionResp = await invoke<SessionResponse>('tauri_session_start', {
        master_key_hex: masterKey,
      });

      console.log('✅ Logged in:', sessionResp.message);
      navigate('/dashboard');
    } catch (err: any) {
      setError('Login failed: wrong password or corrupted local state');
      console.error('Login error:', err);
    } finally {
      setIsLoading(false);
    }
  };

  const isInitialized = localStorage.getItem('nexus_initialized') === 'true';

  return (
    <div className="login-container">
      <div className="login-box">
        <h1>🔐 NexusStorage</h1>

        {!isInitialized ? (
          <form onSubmit={handleInitialSetup}>
            <h2>Initial Setup</h2>
            <p className="hint">
              Create a <strong>strong password</strong> (≥12 chars). This will be used to derive your encryption master key via Argon2.
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

            <div className="form-group">
              <label>Confirm Password</label>
              <input
                type="password"
                placeholder="Confirm password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                disabled={isLoading}
                required
              />
            </div>

            <div className="warning">
              ⚠️ <strong>Remember this password!</strong> It cannot be recovered. If lost, your encrypted data is permanently inaccessible.
            </div>

            {error && <div className="error">{error}</div>}

            <button type="submit" disabled={isLoading} className="btn-primary">
              {isLoading ? '⏳ Setting up...' : '🚀 Create Account'}
            </button>
          </form>
        ) : (
          <form onSubmit={handleLogin}>
            <h2>Welcome Back</h2>

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

            {error && <div className="error">{error}</div>}

            <button type="submit" disabled={isLoading} className="btn-primary">
              {isLoading ? '⏳ Logging in...' : '🔓 Login'}
            </button>

            <button
              type="button"
              onClick={() => navigate('/recovery')}
              className="btn-secondary"
              disabled={isLoading}
            >
              📥 Recover from Backup
            </button>
          </form>
        )}
      </div>
    </div>
  );
};

export default LoginPage;
