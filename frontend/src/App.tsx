import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  EVENT_IMPORTED,
  EVENT_LOG,
  EVENT_STATUS,
  api,
  errorMessage,
  on,
} from "./api";
import type {
  Answer,
  Health,
  ImportResult,
  LogLine,
  Profile,
  Settings,
  Status,
} from "./types";
import { ConnectionPanel } from "./components/ConnectionPanel";
import { CredentialDialog } from "./components/CredentialDialog";
import { LogDrawer } from "./components/LogDrawer";
import { SettingsDialog } from "./components/SettingsDialog";
import { Sidebar } from "./components/Sidebar";
import {
  ConfirmDialog,
  DropOverlay,
  EmptyState,
  RenameDialog,
  type Toast,
  Toasts,
} from "./components/Prompts";
import { AlertIcon, PlusIcon, SettingsIcon } from "./icons";

const idleStatus: Status = {
  phase: "idle",
  profileId: "",
  profileName: "",
  detail: "Not connected",
  since: "",
  connectedAt: "",
  localIp: "",
  remoteIp: "",
  remotePort: "",
  bytesIn: 0,
  bytesOut: 0,
  stalled: false,
};

const defaultSettings: Settings = {
  launchOnLogin: false,
  closeToTray: true,
  autoConnectProfileId: "",
  theme: "system",
  lastProfileId: "",
};

const defaultHealth: Health = {
  ready: true,
  problem: "",
  openvpnVersion: "",
  openvpnPath: "",
  profileDir: "",
  logDir: "",
  appVersion: "",
  platform: "",
};

/** logCap bounds the log kept in the browser; the file on disk has everything. */
const LOG_CAP = 1500;

export default function App() {
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [status, setStatus] = useState<Status>(idleStatus);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [health, setHealth] = useState<Health>(defaultHealth);
  const [settings, setSettings] = useState<Settings>(defaultSettings);

  const [logsOpen, setLogsOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [renaming, setRenaming] = useState<Profile | null>(null);
  const [deleting, setDeleting] = useState<Profile | null>(null);
  const [dragging, setDragging] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);

  const toastSeq = useRef(0);

  const notify = useCallback((kind: Toast["kind"], text: string) => {
    const id = ++toastSeq.current;
    setToasts((current) => [...current, { id, kind, text }]);
  }, []);

  const dismissToast = useCallback((id: number) => {
    setToasts((current) => current.filter((t) => t.id !== id));
  }, []);

  const refreshProfiles = useCallback(async () => {
    try {
      const list = await api.profiles.list();
      setProfiles(list);
      return list;
    } catch (err) {
      notify("bad", errorMessage(err));
      return [];
    }
  }, [notify]);

  // Initial load. The status comes from the backend rather than being assumed,
  // so reopening the window during a live connection shows the truth.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [list, current, prefs, info, existingLogs] = await Promise.all([
        api.profiles.list().catch(() => [] as Profile[]),
        api.connection.status().catch(() => idleStatus),
        api.app.settings().catch(() => defaultSettings),
        api.app.health().catch(() => defaultHealth),
        api.connection.logs().catch(() => [] as LogLine[]),
      ]);
      if (cancelled) return;

      setProfiles(list);
      setStatus(current);
      setSettings(prefs);
      setHealth(info);
      setLogs(existingLogs.slice(-LOG_CAP));

      // A remembered id that no longer exists must not win over the fallback:
      // a profile that has been renamed or deleted would otherwise leave the
      // sidebar showing profiles with none of them selected, which reads as
      // "my profile is gone".
      const known = (id: string | null | undefined) =>
        id && list.some((profile) => profile.id === id) ? id : null;
      setSelectedId(
        known(current.profileId) ??
          known(prefs.lastProfileId) ??
          (list.length > 0 ? list[0].id : null),
      );
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Backend events.
  useEffect(() => {
    const offStatus = on<Status>(EVENT_STATUS, (next) => {
      setStatus(next);
      // Follow the connection in the sidebar, so a tray-initiated connect
      // shows the right profile when the window opens.
      if (next.profileId) setSelectedId((id) => id ?? next.profileId);
    });

    const offLog = on<LogLine>(EVENT_LOG, (line) => {
      setLogs((current) => [...current, line].slice(-LOG_CAP));
    });

    const offImported = on<ImportResult>(EVENT_IMPORTED, (result) => {
      setDragging(false);
      const imported = result.imported ?? [];
      const errors = result.errors ?? [];
      refreshProfiles().then((list) => {
        if (imported.length > 0) {
          setSelectedId(imported[0].id);
          notify(
            "good",
            imported.length === 1
              ? `Added ${imported[0].name}`
              : `Added ${imported.length} profiles`,
          );
        } else if (list.length === 0 && errors.length === 0) {
          notify("bad", "Nothing in that drop looked like a profile.");
        }
        errors.forEach((message) => notify("bad", message));
      });
    });

    return () => {
      offStatus();
      offLog();
      offImported();
    };
  }, [notify, refreshProfiles]);

  // Drag feedback. The drop itself is handled in Go; these DOM events exist
  // only so the window can show that it is a target.
  useEffect(() => {
    let depth = 0;
    const onEnter = (e: DragEvent) => {
      if (!e.dataTransfer?.types.includes("Files")) return;
      depth += 1;
      setDragging(true);
    };
    const onLeave = () => {
      depth = Math.max(0, depth - 1);
      if (depth === 0) setDragging(false);
    };
    const onOver = (e: DragEvent) => e.preventDefault();
    const onDrop = () => {
      depth = 0;
      setDragging(false);
    };

    window.addEventListener("dragenter", onEnter);
    window.addEventListener("dragleave", onLeave);
    window.addEventListener("dragover", onOver);
    window.addEventListener("drop", onDrop);
    return () => {
      window.removeEventListener("dragenter", onEnter);
      window.removeEventListener("dragleave", onLeave);
      window.removeEventListener("dragover", onOver);
      window.removeEventListener("drop", onDrop);
    };
  }, []);

  // Theme. "system" means leave it to the stylesheet's media query.
  useEffect(() => {
    const root = document.documentElement;
    if (settings.theme === "system") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", settings.theme);
  }, [settings.theme]);

  const selected = useMemo(
    () => profiles.find((p) => p.id === selectedId) ?? null,
    [profiles, selectedId],
  );

  const addProfile = useCallback(async () => {
    try {
      const result = await api.profiles.browseAndImport();
      const imported = result.imported ?? [];
      const errors = result.errors ?? [];
      await refreshProfiles();
      if (imported.length > 0) {
        setSelectedId(imported[0].id);
        notify(
          "good",
          imported.length === 1
            ? `Added ${imported[0].name}`
            : `Added ${imported.length} profiles`,
        );
      }
      errors.forEach((message) => notify("bad", message));
    } catch (err) {
      notify("bad", errorMessage(err));
    }
  }, [notify, refreshProfiles]);

  const connect = useCallback(async () => {
    if (!selected) return;
    // Clear the previous session's log so the user is not reading old failures
    // while diagnosing a new one.
    setLogs([]);
    try {
      await api.connection.connect(selected.id);
    } catch (err) {
      // The backend also publishes this as a failed status; the toast is for
      // the case where the window is what the user is looking at.
      notify("bad", errorMessage(err));
    }
  }, [selected, notify]);

  const disconnect = useCallback(async () => {
    try {
      await api.connection.disconnect();
    } catch (err) {
      notify("bad", errorMessage(err));
    }
  }, [notify]);

  const submitCredentials = useCallback(
    async (answer: Answer) => {
      try {
        await api.connection.submitCredentials(answer);
        if (answer.remember) await refreshProfiles();
      } catch (err) {
        notify("bad", errorMessage(err));
      }
    },
    [notify, refreshProfiles],
  );

  const saveSettings = useCallback(
    async (next: Settings) => {
      setSettings(next);
      try {
        setSettings(await api.app.saveSettings(next));
      } catch (err) {
        notify("bad", errorMessage(err));
        // Put back what the backend actually has rather than leaving the UI
        // claiming a setting that did not stick.
        api.app.settings().then(setSettings).catch(() => {});
      }
    },
    [notify],
  );

  const doRename = useCallback(
    async (name: string) => {
      if (!renaming) return;
      const target = renaming;
      setRenaming(null);
      try {
        await api.profiles.rename(target.id, name);
        await refreshProfiles();
      } catch (err) {
        notify("bad", errorMessage(err));
      }
    },
    [renaming, notify, refreshProfiles],
  );

  const doDelete = useCallback(async () => {
    if (!deleting) return;
    const target = deleting;
    setDeleting(null);
    try {
      if (status.profileId === target.id) await api.connection.disconnect();
      await api.profiles.remove(target.id);
      const list = await refreshProfiles();
      if (selectedId === target.id) {
        setSelectedId(list.length > 0 ? list[0].id : null);
      }
      notify("good", `Deleted ${target.name}`);
    } catch (err) {
      notify("bad", errorMessage(err));
    }
  }, [deleting, status.profileId, selectedId, notify, refreshProfiles]);

  const forgetCredentials = useCallback(
    async (profile: Profile) => {
      try {
        await api.profiles.forgetCredentials(profile.id);
        await refreshProfiles();
        notify("good", `Forgot the saved sign-in for ${profile.name}`);
      } catch (err) {
        notify("bad", errorMessage(err));
      }
    },
    [notify, refreshProfiles],
  );

  const openLogFolder = useCallback(() => {
    api.app.openLogFolder().catch((err) => notify("bad", errorMessage(err)));
  }, [notify]);

  const openProfileFolder = useCallback(() => {
    api.app.openProfileFolder().catch((err) => notify("bad", errorMessage(err)));
  }, [notify]);

  return (
    <div className="app">
      <Sidebar
        profiles={profiles}
        selectedId={selectedId}
        status={status}
        onSelect={setSelectedId}
        onAdd={addProfile}
        onRename={setRenaming}
        onDelete={setDeleting}
        onForgetCredentials={forgetCredentials}
        onOpenProfileFolder={openProfileFolder}
      />

      <main className="main">
        <header className="titlebar">
          <div>
            <div style={{ fontWeight: 600 }}>
              {selected ? selected.name : "VPN Desktop"}
            </div>
            <div className="small faint">
              {selected
                ? selected.server || "No server address in this profile"
                : health.openvpnVersion}
            </div>
          </div>
          <div className="titlebar__actions">
            <button className="btn btn--ghost" onClick={addProfile}>
              <PlusIcon />
              Add profile
            </button>
            <button
              className="btn btn--ghost btn--icon"
              onClick={() => setSettingsOpen(true)}
              aria-label="Settings"
              title="Settings"
            >
              <SettingsIcon />
            </button>
          </div>
        </header>

        <div className="content">
          {!health.ready && (
            <div className="banner banner--bad" style={{ marginBottom: 18 }}>
              <AlertIcon className="banner__icon" />
              <div>
                <div className="banner__title">
                  Connections are not possible yet
                </div>
                <div className="selectable">{health.problem}</div>
              </div>
            </div>
          )}

          {profiles.length === 0 ? (
            <EmptyState onAdd={addProfile} />
          ) : (
            <ConnectionPanel
              profile={selected}
              status={status}
              onConnect={connect}
              onDisconnect={disconnect}
            />
          )}
        </div>

        <LogDrawer
          lines={logs}
          open={logsOpen}
          onToggle={() => setLogsOpen((v) => !v)}
          onOpenLogFolder={openLogFolder}
        />
      </main>

      {dragging && <DropOverlay />}

      {status.prompt && (
        // The key remounts the dialog for every prompt. Without it React reuses
        // the instance, and openvpn's two questions -- account credentials then
        // the key passphrase -- share one set of fields: the password typed for
        // the first is still sitting there as the answer to the second, and a
        // ticked "remember" carries over and saves the wrong secret.
        <CredentialDialog
          key={status.prompt.id}
          prompt={status.prompt}
          onSubmit={submitCredentials}
          onCancel={disconnect}
        />
      )}

      {renaming && (
        <RenameDialog
          initial={renaming.name}
          onSubmit={doRename}
          onCancel={() => setRenaming(null)}
        />
      )}

      {deleting && (
        <ConfirmDialog
          title={`Delete ${deleting.name}?`}
          body="This removes the profile and the certificate and key files stored with it. It cannot be undone."
          confirmLabel="Delete profile"
          onConfirm={doDelete}
          onCancel={() => setDeleting(null)}
        />
      )}

      {settingsOpen && (
        <SettingsDialog
          settings={settings}
          health={health}
          profiles={profiles}
          onChange={saveSettings}
          onClose={() => setSettingsOpen(false)}
          onOpenLogFolder={openLogFolder}
        />
      )}

      <Toasts toasts={toasts} onDismiss={dismissToast} />
    </div>
  );
}

