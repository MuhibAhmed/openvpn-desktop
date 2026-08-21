import { useEffect, useRef, useState } from "react";

import type { Answer, Prompt } from "../types";
import { api } from "../api";

type Props = {
  prompt: Prompt;
  onSubmit: (answer: Answer) => void;
  onCancel: () => void;
};

/**
 * CredentialDialog answers whatever openvpn is asking for.
 *
 * The four prompt shapes differ enough that showing every field every time
 * would be confusing: a private key passphrase has no username, and a dynamic
 * challenge already knows who you are and only wants the code.
 */
export function CredentialDialog({ prompt, onSubmit, onCancel }: Props) {
  const [username, setUsername] = useState(prompt.username);
  const [password, setPassword] = useState("");
  const [response, setResponse] = useState("");
  const [remember, setRemember] = useState(false);
  const [fromLegacy, setFromLegacy] = useState(false);
  const firstField = useRef<HTMLInputElement>(null);

  const wantsUsername =
    prompt.kind === "credentials" || prompt.kind === "static-challenge";
  const wantsPassword = prompt.kind !== "dynamic-challenge";
  const wantsResponse =
    prompt.kind === "static-challenge" || prompt.kind === "dynamic-challenge";
  // A one-time code is never worth saving; everything else is, including a key
  // passphrase, which goes into its own vault entry.
  const canRemember = wantsPassword;

  // Offer whatever is already saved, so a known profile is one click.
  useEffect(() => {
    let cancelled = false;
    if (!canRemember) return;
    api.connection
      .savedCredentials()
      .then((saved) => {
        if (cancelled || !saved.found) return;
        setUsername((current) => current || saved.username);
        setPassword((current) => current || saved.password);
        setRemember(true);
        setFromLegacy(saved.fromLegacyGui);
      })
      .catch(() => {
        // Nothing saved, or the vault is unavailable. The user can still type.
      });
    return () => {
      cancelled = true;
    };
  }, [canRemember, prompt.id]);

  useEffect(() => {
    firstField.current?.focus();
  }, [prompt.id]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const submit = () => {
    onSubmit({
      promptId: prompt.id,
      username,
      password,
      response,
      remember: canRemember && remember,
    });
  };

  // The password is deliberately not required. openvpn supports blank passwords
  // (it substitutes its own marker for them), and plenty of servers authenticate
  // on the certificate and a username alone, so demanding one here would leave
  // those users unable to submit the form at all.
  const complete =
    (!wantsUsername || username.trim() !== "") &&
    (!wantsResponse || response.trim() !== "") &&
    (wantsUsername || wantsResponse || password !== "");

  return (
    <div className="scrim">
      <form
        className="modal"
        onSubmit={(e) => {
          e.preventDefault();
          if (complete) submit();
        }}
      >
        <div className="modal__head">
          <div className="modal__title">{prompt.title}</div>
          <div className="modal__text">
            {prompt.retry && prompt.kind !== "dynamic-challenge"
              ? "That did not work. Please try again."
              : prompt.message}
          </div>
        </div>

        <div className="modal__body">
          {fromLegacy && (
            // Filling in a secret the user never gave this app must not be
            // silent, even though it is their own credential.
            <div className="banner banner--warn">
              <span>
                Filled in from the passwords your OpenVPN GUI had saved. Tick
                below to keep it here instead, and this app will stop needing the
                old one.
              </span>
            </div>
          )}

          {wantsUsername && (
            <label className="field">
              <span className="field__label">Username</span>
              <input
                ref={firstField}
                className="input"
                type="text"
                autoComplete="off"
                spellCheck={false}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </label>
          )}

          {wantsPassword && (
            <label className="field">
              <span className="field__label">
                {prompt.kind === "passphrase" ? "Passphrase" : "Password"}
                {/* Say so rather than leaving people stuck on a field they
                    have nothing to type into. */}
                {prompt.kind !== "passphrase" && (
                  <span className="faint"> — optional</span>
                )}
              </span>
              <input
                ref={wantsUsername ? undefined : firstField}
                className="input"
                type="password"
                autoComplete="off"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              {prompt.kind !== "passphrase" && (
                <span className="check__hint">
                  Leave this blank if your VPN signs you in with the certificate
                  and username alone.
                </span>
              )}
            </label>
          )}

          {wantsResponse && (
            <label className="field">
              <span className="field__label">
                {prompt.challengeText || "Verification code"}
              </span>
              <input
                ref={prompt.kind === "dynamic-challenge" ? firstField : undefined}
                className="input"
                type={prompt.echoResponse ? "text" : "password"}
                autoComplete="one-time-code"
                inputMode="numeric"
                spellCheck={false}
                value={response}
                onChange={(e) => setResponse(e.target.value)}
              />
            </label>
          )}

          {canRemember && (
            <label className="check">
              <input
                type="checkbox"
                checked={remember}
                onChange={(e) => setRemember(e.target.checked)}
              />
              <span>
                <span className="check__text">
                  {prompt.kind === "passphrase"
                    ? "Remember this passphrase"
                    : "Remember these details"}
                </span>
                <br />
                <span className="check__hint">
                  Stored in the Windows Credential Manager, not in this app.
                </span>
              </span>
            </label>
          )}
        </div>

        <div className="modal__foot">
          <button type="button" className="btn btn--ghost" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="btn btn--primary" disabled={!complete}>
            Sign in
          </button>
        </div>
      </form>
    </div>
  );
}
