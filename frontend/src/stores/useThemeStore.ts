import { create } from 'zustand';

export type Theme = 'light' | 'dark' | 'vaporwave';

interface ThemeState {
  theme: Theme;
  setTheme: (theme: Theme) => void;
}

export const useThemeStore = create<ThemeState>((set) => {
  // Initialize theme from localStorage if available (client-side only)
  let initialTheme: Theme = 'dark';
  if (typeof window !== 'undefined') {
    const saved = localStorage.getItem('telos-theme') as Theme;
    if (saved === 'light' || saved === 'dark' || saved === 'vaporwave') {
      initialTheme = saved;
      document.documentElement.setAttribute('data-theme', saved);
    } else {
      document.documentElement.setAttribute('data-theme', 'dark');
    }
  }

  return {
    theme: initialTheme,
    setTheme: (theme: Theme) => {
      if (typeof window !== 'undefined') {
        localStorage.setItem('telos-theme', theme);
        document.documentElement.setAttribute('data-theme', theme);
      }
      set({ theme });
    },
  };
});
