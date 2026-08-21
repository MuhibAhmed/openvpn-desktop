import { useState } from "react";

import type { Health, Profile, Settings } from "../types";
import { CloseIcon, FolderIcon } from "../icons";

type Props = {
  settings: Settings;
  health: Health;
  profiles: Profile[];
  onChange: (next: Settings) => void;
  onClose: () => void;
  onOpenLogFolder: () => void;
};

export function SettingsDialog({
  settings,
  health,
  profiles,
  onChange,
  onClose,
  onOpenLogFolder,
}: Props) {
  const [draft, setDraft] = useState(settings);

  // Preferences apply as they are toggled rather than behind a Save button:
  // there are few enough of them that a confirmation step is friction, not
  // safety.
  const update = (patch: Partial<Settings>) => {
    const next = { ...draft, ...patch };
    setDraft(next);
    onChange(next);
  };

  return (
    <div className="scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal modal--wide">
        <div className="modal__head">
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
            }}
          >
            <div className="modal__title">Settings</div>
            <button className="btn btn--ghost btn--icon" onClick={onClose} aria-label="Close">
              <CloseIcon />
            </button>
          </div>
        </div>

        <div className="modal__body" style={{ gap: 0 }}>
          <div className="row">
            <div>
              <div className="row__title">Start with Windows</div>
              <div className="row__hint">
                Launches minimised to the notification area when you sign in.
              </div>
            </div>
            <label className="check" style={{ padding: 0 }}>
              <input
                type="checkbox"
                checked={draft.launchOnLogin}
                onChange={(e) => update({ launchOnLogin: e.target.checked })}
              />
            </label>
          </div>

          <div className="row">
            <div>
              <div className="row__title">Keep running when closed</div>
              <div className="row__hint">
                Closing the window leaves the app in the notification area so the
                connection stays up.
              </div>
            </div>
            <label className="check" style={{ padding: 0 }}>
              <input
                type="checkbox"
                checked={draft.closeToTray}
                onChange={(e) => update({ closeToTray: e.target.checked })}
              />
            </label>
          </div>

          <div className="row">
            <div>
              <div className="row__title">Connect automatically</div>
              <div className="row__hint">
                Bring up a profile as soon as the app starts.
              </div>
            </div>
            <select
              className="select"
              value={draft.autoConnectProfileId}
              onChange={(e) => update({ autoConnectProfileId: e.target.value })}
            >
              <option value="">Do not connect</option>
              {profiles.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>

          <div className="row">
            <div>
              <div className="row__title">Appearance</div>
              <div className="row__hint">
                Follow Windows, or pick one and stay with it.
              </div>
            </div>
            <select
              className="select"
              value={draft.theme}
              onChange={(e) =>
                update({ theme: e.target.value as Settings["theme"] })
              }
            >
              <option value="system">Match Windows</option>
              <option value="dark">Dark</option>
              <option value="light">Light</option>
            </select>
          </div>

          <div className="row" style={{ display: "block" }}>
            <div className="row__title" style={{ marginBottom: 8 }}>
              About
            </div>
            <div className="detail-grid">
              <div>
                <div className="detail__label">App version</div>
                <div className="detail__value">{health.appVersion}</div>
              </div>
              <div>
                <div className="detail__label">OpenVPN</div>
                <div className="detail__value">
                  {health.openvpnVersion || "not detected"}
                </div>
              </div>
              <div>
                <div className="detail__label">Profiles</div>
                <div className="detail__value">{health.profileDir}</div>
              </div>
              <div>
                <div className="detail__label">Logs</div>
                <div className="detail__value">{health.logDir}</div>
              </div>
            </div>
            <button
              className="btn btn--ghost"
              style={{ marginTop: 10 }}
              onClick={onOpenLogFolder}
            >
              <FolderIcon />
              Open log folder
            </button>
          </div>
        </div>

        <div className="modal__foot">
          <button className="btn" onClick={onClose}>
            Done
          </button>
        </div>
      </div>
    </div>
  );
}
