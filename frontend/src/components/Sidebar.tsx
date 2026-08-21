import { useEffect, useRef, useState } from "react";

import type { Profile, Status } from "../types";
import { isBusy } from "../types";
import {
  BrandMark,
  FolderIcon,
  KeyIcon,
  MoreIcon,
  PencilIcon,
  PlusIcon,
  TrashIcon,
} from "../icons";

type Props = {
  profiles: Profile[];
  selectedId: string | null;
  status: Status;
  onSelect: (id: string) => void;
  onAdd: () => void;
  onRename: (profile: Profile) => void;
  onDelete: (profile: Profile) => void;
  onForgetCredentials: (profile: Profile) => void;
  onOpenProfileFolder: () => void;
};

/** dotClass picks the status dot for a profile row. */
function dotClass(profile: Profile, status: Status): string {
  if (status.profileId !== profile.id) return "profile__dot";
  if (status.phase === "connected") return "profile__dot profile__dot--on";
  if (status.phase === "failed") return "profile__dot profile__dot--bad";
  if (isBusy(status.phase)) return "profile__dot profile__dot--busy";
  return "profile__dot";
}

export function Sidebar({
  profiles,
  selectedId,
  status,
  onSelect,
  onAdd,
  onRename,
  onDelete,
  onForgetCredentials,
  onOpenProfileFolder,
}: Props) {
  const [menuFor, setMenuFor] = useState<string | null>(null);

  return (
    <aside className="sidebar">
      <div className="sidebar__head">
        <BrandMark size={30} className="brand-mark" />
        <div>
          <div className="brand-name">VPN Desktop</div>
          <div className="brand-sub">OpenVPN client</div>
        </div>
      </div>

      <div className="sidebar__label">
        <span>Profiles</span>
        <span className="faint">{profiles.length || ""}</span>
      </div>

      <div className="sidebar__list">
        {profiles.length === 0 && (
          <div className="small faint" style={{ padding: "8px 10px" }}>
            No profiles yet. Drop an .ovpn file anywhere in this window.
          </div>
        )}

        {profiles.map((profile) => (
          <ProfileRow
            key={profile.id}
            profile={profile}
            dot={dotClass(profile, status)}
            active={profile.id === selectedId}
            menuOpen={menuFor === profile.id}
            onSelect={() => onSelect(profile.id)}
            onToggleMenu={() =>
              setMenuFor(menuFor === profile.id ? null : profile.id)
            }
            onCloseMenu={() => setMenuFor(null)}
            onRename={() => onRename(profile)}
            onDelete={() => onDelete(profile)}
            onForgetCredentials={() => onForgetCredentials(profile)}
          />
        ))}
      </div>

      <div className="sidebar__foot">
        <button className="dropzone" onClick={onAdd}>
          <span className="inline">
            <PlusIcon size={15} />
            Add a profile
          </span>
        </button>
        <button className="btn btn--ghost btn--block" onClick={onOpenProfileFolder}>
          <FolderIcon />
          Profile folder
        </button>
      </div>
    </aside>
  );
}

type RowProps = {
  profile: Profile;
  dot: string;
  active: boolean;
  menuOpen: boolean;
  onSelect: () => void;
  onToggleMenu: () => void;
  onCloseMenu: () => void;
  onRename: () => void;
  onDelete: () => void;
  onForgetCredentials: () => void;
};

function ProfileRow({
  profile,
  dot,
  active,
  menuOpen,
  onSelect,
  onToggleMenu,
  onCloseMenu,
  onRename,
  onDelete,
  onForgetCredentials,
}: RowProps) {
  const wrapper = useRef<HTMLDivElement>(null);

  // Close the row menu on any click elsewhere, which is what every other
  // desktop menu does.
  useEffect(() => {
    if (!menuOpen) return;
    const onDocumentClick = (e: MouseEvent) => {
      if (!wrapper.current?.contains(e.target as Node)) onCloseMenu();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCloseMenu();
    };
    document.addEventListener("mousedown", onDocumentClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDocumentClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [menuOpen, onCloseMenu]);

  return (
    <div ref={wrapper} style={{ position: "relative" }}>
      <button
        className={active ? "profile profile--active" : "profile"}
        onClick={onSelect}
        title={profile.server || profile.name}
      >
        <span className={dot} />
        <span style={{ minWidth: 0 }}>
          <span className="profile__name">{profile.name}</span>
          <span className="profile__server">
            {profile.server || "No server address"}
          </span>
        </span>
        <span
          className="profile__menu"
          role="button"
          aria-expanded={menuOpen}
          aria-label={`Options for ${profile.name}`}
          onClick={(e) => {
            e.stopPropagation();
            onToggleMenu();
          }}
        >
          <MoreIcon />
        </span>
      </button>

      {menuOpen && (
        <div className="menu" style={{ top: "calc(100% - 4px)", right: 6 }}>
          <button
            className="menu__item"
            onClick={() => {
              onCloseMenu();
              onRename();
            }}
          >
            <PencilIcon />
            Rename
          </button>
          {profile.hasSavedCredentials && (
            <button
              className="menu__item"
              onClick={() => {
                onCloseMenu();
                onForgetCredentials();
              }}
            >
              <KeyIcon />
              Forget saved sign-in
            </button>
          )}
          <div className="menu__sep" />
          <button
            className="menu__item menu__item--danger"
            onClick={() => {
              onCloseMenu();
              onDelete();
            }}
          >
            <TrashIcon />
            Delete profile
          </button>
        </div>
      )}
    </div>
  );
}
