// Memory fallback map for web environment (never localStorage/sessionStorage)
const memoryStore = new Map<string, string>();

interface WindowTauriStore {
  __TAURI_OS_PLUGIN_STORE__?: {
    get: (key: string) => Promise<string | null>;
    set: (key: string, value: string) => Promise<void>;
    delete: (key: string) => Promise<void>;
  };
}

function getTauriStore() {
  if (typeof window === "undefined") return null;
  const w = window as unknown as WindowTauriStore;
  return w.__TAURI_OS_PLUGIN_STORE__ ?? null;
}

export async function getSecret(key: string): Promise<string | null> {
  const tauri = getTauriStore();
  if (tauri) {
    try {
      return await tauri.get(key);
    } catch {
      return memoryStore.get(key) ?? null;
    }
  }
  return memoryStore.get(key) ?? null;
}

export async function setSecret(key: string, value: string): Promise<void> {
  const tauri = getTauriStore();
  if (tauri) {
    try {
      await tauri.set(key, value);
      return;
    } catch {
      memoryStore.set(key, value);
      return;
    }
  }
  memoryStore.set(key, value);
}

export async function deleteSecret(key: string): Promise<void> {
  const tauri = getTauriStore();
  if (tauri) {
    try {
      await tauri.delete(key);
    } catch {
      memoryStore.delete(key);
    }
  }
  memoryStore.delete(key);
}
