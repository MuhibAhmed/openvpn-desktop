import { useEffect, useRef, useState } from "react";

import { AlertIcon, BrandMark, CheckIcon, DownloadIcon } from "../icons";

/** RenameDialog asks for a new display name for a profile. */
export function RenameDialog({
  initial,
  onSubmit,
  onCancel,
}: {
  initial: string;
  onSubmit: (name: string) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => {
    field.current?.focus();
    field.current?.select();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancel();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div className="scrim">
      <form
        className="modal"
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) onSubmit(name.trim());
        }}
      >
        <div className="modal__head">
          <div className="modal__title">Rename profile</div>
        </div>
        <div className="modal__body">
          <label className="field">
            <span className="field__label">Name</span>
            <input
              ref={field}
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
        </div>
        <div className="modal__foot">
          <button type="button" className="btn btn--ghost" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="btn btn--primary" disabled={!name.trim()}>
            Rename
          </button>
        </div>
      </form>
    </div>
  );
}

/**
 * ConfirmDialog is for the one action that cannot be undone. Deleting a profile
 * takes its private key with it, so this says so rather than asking a bland
 * "are you sure".
 */
export function ConfirmDialog({
  title,
  body,
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  title: string;
  body: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancel();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div className="scrim">
      <div className="modal">
        <div className="modal__head">
          <div className="modal__title">{title}</div>
          <div className="modal__text">{body}</div>
        </div>
        <div className="modal__foot">
          <button className="btn btn--ghost" onClick={onCancel}>
            Cancel
          </button>
          <button className="btn btn--danger" onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

/** EmptyState is the whole-window invitation to add a first profile. */
export function EmptyState({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="empty">
      <BrandMark size={72} className="brand-hero" />
      <div className="empty__title">Drop a profile to get started</div>
      <div className="empty__text">
        Drag an <span className="mono">.ovpn</span> file anywhere in this window.
        If it comes with certificate files, drop the whole folder and they will be
        folded in.
      </div>
      <button className="btn btn--primary" onClick={onAdd}>
        Choose a file
      </button>
    </div>
  );
}

/** DropOverlay is the feedback shown while a drag is over the window. */
export function DropOverlay() {
  return (
    <div className="drop-overlay">
      <div className="drop-overlay__inner">
        <DownloadIcon size={34} />
        Drop to add this profile
      </div>
    </div>
  );
}

export type Toast = {
  id: number;
  kind: "good" | "bad";
  text: string;
};

/** Toasts reports the outcome of actions that do not have their own surface. */
export function Toasts({
  toasts,
  onDismiss,
}: {
  toasts: Toast[];
  onDismiss: (id: number) => void;
}) {
  return (
    <div className="toasts">
      {toasts.map((toast) => (
        <ToastItem key={toast.id} toast={toast} onDismiss={onDismiss} />
      ))}
    </div>
  );
}

function ToastItem({
  toast,
  onDismiss,
}: {
  toast: Toast;
  onDismiss: (id: number) => void;
}) {
  // Errors stay until dismissed; a success message that has been read is just
  // clutter.
  useEffect(() => {
    if (toast.kind === "bad") return;
    const id = window.setTimeout(() => onDismiss(toast.id), 4000);
    return () => window.clearTimeout(id);
  }, [toast, onDismiss]);

  return (
    <div
      className={`toast toast--${toast.kind}`}
      onClick={() => onDismiss(toast.id)}
      role="status"
    >
      <span className="toast__icon">
        {toast.kind === "good" ? <CheckIcon /> : <AlertIcon />}
      </span>
      <span className="toast__text">{toast.text}</span>
    </div>
  );
}
