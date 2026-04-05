import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react';

type InteractionMode = 'desktop' | 'selection';

interface SettingsContextType {
  dark: boolean;
  setDark: (dark: boolean) => void;
  persistentCheckboxes: boolean;
  setPersistentCheckboxes: (enabled: boolean) => void;
  interactionMode: InteractionMode;
  setInteractionMode: (mode: InteractionMode) => void;
}

const SettingsContext = createContext<SettingsContextType | undefined>(undefined);

export const SettingsProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  // Theme state
  const [dark, setDarkState] = useState<boolean>(() => {
    const saved = localStorage.getItem("nexus_theme");
    return saved === "dark" || (!saved && window.matchMedia("(prefers-color-scheme: dark)").matches);
  });

  // Selection settings state
  const [persistentCheckboxes, setPersistentCheckboxesState] = useState<boolean>(() => {
    return localStorage.getItem("nexus_persistent_checkboxes") === "true";
  });

  const [interactionMode, setInteractionModeState] = useState<InteractionMode>(() => {
    return (localStorage.getItem("nexus_interaction_mode") as InteractionMode) || 'desktop';
  });

  // Sync with localStorage and document body
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("nexus_theme", dark ? "dark" : "light");
  }, [dark]);

  useEffect(() => {
    localStorage.setItem("nexus_persistent_checkboxes", String(persistentCheckboxes));
  }, [persistentCheckboxes]);

  useEffect(() => {
    localStorage.setItem("nexus_interaction_mode", interactionMode);
  }, [interactionMode]);

  const setDark = (val: boolean) => setDarkState(val);
  const setPersistentCheckboxes = (val: boolean) => setPersistentCheckboxesState(val);
  const setInteractionMode = (val: InteractionMode) => setInteractionModeState(val);

  return (
    <SettingsContext.Provider value={{
      dark, setDark,
      persistentCheckboxes, setPersistentCheckboxes,
      interactionMode, setInteractionMode
    }}>
      {children}
    </SettingsContext.Provider>
  );
};

export const useSettings = () => {
  const context = useContext(SettingsContext);
  if (context === undefined) {
    throw new Error('useSettings must be used within a SettingsProvider');
  }
  return context;
};
