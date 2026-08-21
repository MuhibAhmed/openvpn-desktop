// Domain types mirroring the Go side.
//
// The generated bindings carry JSDoc types, but declaring the shapes we care
// about here keeps components reading cleanly and gives one place to look when
// the Go structs change.

export type Phase =
  | "idle"
  | "starting"
  | "connecting"
  | "authenticating"
  | "configuring"
  | "connected"
  | "reconnecting"
  | "disconnecting"
  | "failed";

export type PromptKind =
  | "credentials"
  | "passphrase"
  | "static-challenge"
  | "dynamic-challenge";

export interface Prompt {
  id: string;
  kind: PromptKind;
  realm: string;
  title: string;
  message: string;
  username: string;
  challengeText?: string;
  echoResponse: boolean;
  retry: boolean;
}

export interface Status {
  phase: Phase;
  profileId: string;
  profileName: string;
  detail: string;
  since: string;
  connectedAt: string;
  localIp: string;
  remoteIp: string;
  remotePort: string;
  bytesIn: number;
  bytesOut: number;
  prompt?: Prompt | null;
  error?: string;
  /** stalled marks a connection that stopped progressing without failing. */
  stalled: boolean;
}

export interface LogLine {
  at: string;
  level: string;
  text: string;
}

export interface Profile {
  id: string;
  name: string;
  server: string;
  protocol: string;
  importedAt: string;
  needsCredentials: boolean;
  hasSavedCredentials: boolean;
  routesAllTraffic: boolean;
  warnings: string[] | null;
}

export interface ImportResult {
  imported: Profile[] | null;
  errors: string[] | null;
}

export interface Health {
  ready: boolean;
  problem: string;
  openvpnVersion: string;
  openvpnPath: string;
  profileDir: string;
  logDir: string;
  appVersion: string;
  platform: string;
}

export interface Settings {
  launchOnLogin: boolean;
  closeToTray: boolean;
  autoConnectProfileId: string;
  theme: "system" | "light" | "dark";
  lastProfileId: string;
}

export interface Answer {
  promptId: string;
  username: string;
  password: string;
  response: string;
  remember: boolean;
}

/** busy reports whether a phase is a transient one worth showing a spinner for. */
export function isBusy(phase: Phase): boolean {
  return (
    phase === "starting" ||
    phase === "connecting" ||
    phase === "authenticating" ||
    phase === "configuring" ||
    phase === "reconnecting" ||
    phase === "disconnecting"
  );
}

/** isLive reports whether a tunnel exists, up or coming up. */
export function isLive(phase: Phase): boolean {
  return phase !== "idle" && phase !== "failed";
}
